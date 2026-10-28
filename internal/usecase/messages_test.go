//go:build sqlite || !postgres

package usecase_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/adapter/storage/gormstore"
	"github.com/greatgoodtiver/matrixrss/internal/domain"
	"github.com/greatgoodtiver/matrixrss/internal/usecase"
)

const room = "!room:hs"

var usecaseOptions = usecase.MessageOptions{}

// countingSecrets hands out predictable secrets: s1, s2, ...
type countingSecrets struct{ n int }

func (c *countingSecrets) New() (string, error) {
	c.n++
	return fmt.Sprintf("s%d", c.n), nil
}

type env struct {
	store    *gormstore.Store
	feeds    *usecase.FeedService
	messages *usecase.MessageService
	access   *usecase.AccessService
}

func newEnv(t *testing.T, options usecase.MessageOptions) env {
	t.Helper()
	store, err := gormstore.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return env{
		store:    store,
		feeds:    usecase.NewFeedService(store.Feeds()),
		messages: usecase.NewMessageService(store.Feeds(), store.Messages(), options),
		access:   usecase.NewAccessService(store.APIKeys(), store.FeedTokens(), store.Feeds(), &countingSecrets{}),
	}
}

func (e env) enable(t *testing.T) {
	t.Helper()
	if _, err := e.feeds.Enable(context.Background(), room, "Room"); err != nil {
		t.Fatal(err)
	}
}

func minute(n int) time.Time { return time.Date(2026, 1, 1, 10, n, 0, 0, time.UTC) }

func TestRecordOnlyEnabledRooms(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, usecase.MessageOptions{})
	_ = e.messages.Record(ctx, domain.Message{EventID: "$before", RoomID: room, SentAt: minute(0)})
	e.enable(t)
	_ = e.messages.Record(ctx, domain.Message{EventID: "$on", RoomID: room, SentAt: minute(1)})
	_ = e.feeds.Disable(ctx, room)
	_ = e.messages.Record(ctx, domain.Message{EventID: "$off", RoomID: room, SentAt: minute(2)})

	page, err := e.messages.Page(ctx, room, domain.MessageQuery{})
	if err != nil || len(page) != 1 || page[0].EventID != "$on" {
		t.Fatalf("only the message sent while enabled must be stored: %+v, %v", page, err)
	}
}

func TestEditRules(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, usecase.MessageOptions{})
	e.enable(t)
	_ = e.messages.Record(ctx, domain.Message{EventID: "$m", RoomID: room, Sender: "@author:hs", Body: "v1", SentAt: minute(0)})
	edit := func(id, sender, body string, at int) domain.Edit {
		return domain.Edit{EventID: id, TargetEventID: "$m", RoomID: room, Sender: sender, Body: body, At: minute(at)}
	}

	for _, ed := range []domain.Edit{
		edit("$e1", "@intruder:hs", "hacked", 1), // not the author
		edit("$e2", "@author:hs", "v2", 2),
		{EventID: "$e3", TargetEventID: "$unknown", RoomID: room, Sender: "@author:hs", Body: "x", At: minute(3)},
	} {
		if err := e.messages.Edit(ctx, ed); err != nil {
			t.Fatalf("edit %s: %v", ed.EventID, err)
		}
	}

	revisions, err := e.messages.Revisions(ctx, room, "$m")
	if err != nil || len(revisions) != 2 || revisions[0].Body != "v1" || revisions[1].Body != "v2" {
		t.Fatalf("unexpected revisions: %+v, %v", revisions, err)
	}

	_ = e.messages.Delete(ctx, room, "$m", domain.Deletion{At: minute(4), By: "@author:hs"})
	if err := e.messages.Edit(ctx, edit("$e4", "@author:hs", "after delete", 5)); err != nil {
		t.Fatal(err)
	}
	all, _ := e.messages.Page(ctx, room, domain.MessageQuery{Deleted: domain.IncludeDeleted})
	if len(all) != 1 || all[0].Body == "after delete" {
		t.Fatalf("deleted messages must not be edited: %+v", all)
	}
}

func TestRevisionsOfUneditedMessage(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, usecase.MessageOptions{})
	e.enable(t)
	_ = e.messages.Record(ctx, domain.Message{EventID: "$m", RoomID: room, Body: "only", SentAt: minute(0)})

	revisions, err := e.messages.Revisions(ctx, room, "$m")
	if err != nil || len(revisions) != 1 || revisions[0].Body != "only" {
		t.Fatalf("unexpected revisions: %+v, %v", revisions, err)
	}
	if _, err := e.messages.Revisions(ctx, room, "$none"); err != domain.ErrMessageNotFound {
		t.Fatalf("want ErrMessageNotFound, got %v", err)
	}
}

func TestDeleteKeepsOrErasesContent(t *testing.T) {
	for _, keep := range []bool{false, true} {
		ctx := context.Background()
		e := newEnv(t, usecase.MessageOptions{KeepDeletedContent: keep})
		e.enable(t)
		_ = e.messages.Record(ctx, domain.Message{EventID: "$m", RoomID: room, Body: "secret", SentAt: minute(0)})
		_ = e.messages.Delete(ctx, room, "$m", domain.Deletion{At: minute(1), By: "@mod:hs", Reason: "oops"})

		deleted, err := e.messages.Page(ctx, room, domain.MessageQuery{Deleted: domain.OnlyDeleted})
		if err != nil || len(deleted) != 1 || deleted[0].Deletion.Reason != "oops" {
			t.Fatalf("keep=%v: deletion record missing: %+v, %v", keep, deleted, err)
		}
		if kept := deleted[0].Body == "secret"; kept != keep {
			t.Fatalf("keep=%v but body is %q", keep, deleted[0].Body)
		}
		if live, _ := e.messages.Page(ctx, room, domain.MessageQuery{}); len(live) != 0 {
			t.Fatalf("deleted message must not be listed by default: %+v", live)
		}
	}
}

func TestDisabledRoomsStayReadableFromTheDatabase(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, usecase.MessageOptions{})
	e.enable(t)
	_ = e.messages.Record(ctx, domain.Message{EventID: "$m", RoomID: room, SentAt: minute(0)})
	_ = e.feeds.Disable(ctx, room)

	if page, err := e.messages.Page(ctx, room, domain.MessageQuery{}); err != nil || len(page) != 1 {
		t.Fatalf("disabled room must stay readable: %v, %v", page, err)
	}
	if _, err := e.messages.Page(ctx, "!none:hs", domain.MessageQuery{}); err != domain.ErrFeedNotFound {
		t.Fatalf("want ErrFeedNotFound, got %v", err)
	}
}

func TestRecentClampsLimitAndHidesDeleted(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, usecase.MessageOptions{})
	e.enable(t)
	for i := 0; i < usecase.MaxPageSize+5; i++ {
		_ = e.messages.Record(ctx, domain.Message{EventID: fmt.Sprintf("$m%d", i), RoomID: room, SentAt: minute(0).Add(time.Duration(i) * time.Second)})
	}
	_ = e.messages.Delete(ctx, room, "$m204", domain.Deletion{At: minute(59)})

	recent, err := e.messages.Recent(ctx, room, 10_000)
	if err != nil || len(recent) != usecase.MaxPageSize || recent[0].EventID != "$m203" {
		t.Fatalf("want %d messages without the deleted one, got %d (first %v), %v", usecase.MaxPageSize, len(recent), recent[0].EventID, err)
	}
}
