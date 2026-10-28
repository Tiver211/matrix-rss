//go:build sqlite || !postgres

package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"testing"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
	"github.com/greatgoodtiver/matrixrss/internal/usecase"
)

// fakeSource serves a fixed room history, newest first, with Matrix-like tokens: the last page
// before the start of the room is empty and carries no continuation token.
type fakeSource struct {
	events []domain.Message
	edits  []domain.Edit
	calls  int
	err    error
}

func (f *fakeSource) Older(_ context.Context, _ string, from usecase.HistoryCursor, limit int) (usecase.HistoryPage, error) {
	f.calls++
	if f.err != nil {
		return usecase.HistoryPage{}, f.err
	}
	start := 0
	if from.Token != "" {
		start, _ = strconv.Atoi(from.Token)
	} else if from.EventID != "" {
		for i, msg := range f.events {
			if msg.EventID == from.EventID {
				start = i + 1
			}
		}
	}
	end := min(start+limit, len(f.events))
	if start >= end {
		return usecase.HistoryPage{}, nil
	}
	return usecase.HistoryPage{
		Messages: f.events[start:end],
		Edits:    f.edits,
		Next:     usecase.HistoryCursor{Token: strconv.Itoa(end)},
	}, nil
}

// roomWithHistory has messages m1..m10 (10 is the newest); the bot recorded only m9 and m10 live.
func roomWithHistory(t *testing.T) (env, *fakeSource, *usecase.HistoryService) {
	t.Helper()
	e := newEnv(t, usecase.MessageOptions{})
	e.enable(t)

	source := &fakeSource{}
	for n := 10; n >= 1; n-- {
		source.events = append(source.events, domain.Message{
			EventID: fmt.Sprintf("$m%d", n), RoomID: room, Sender: "@a:hs", Body: fmt.Sprintf("m%d", n), SentAt: minute(n),
		})
	}
	ctx := context.Background()
	for _, msg := range source.events[:2] {
		if err := e.messages.Record(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	history := usecase.NewHistoryService(e.store.Feeds(), e.store.Messages(), e.messages, source, log).WithBatchSize(3)
	return e, source, history
}

func bodies(messages []domain.Message) []string {
	result := make([]string, len(messages))
	for i, msg := range messages {
		result[i] = msg.Body
	}
	return result
}

func TestHistoryFetchesMissingMessagesOnceThenServesFromDatabase(t *testing.T) {
	ctx := context.Background()
	_, source, history := roomWithHistory(t)

	page, err := history.History(ctx, room, domain.MessageQuery{Limit: 4})
	if err != nil || fmt.Sprint(bodies(page)) != "[m10 m9 m8 m7]" {
		t.Fatalf("first page: %v, %v", bodies(page), err)
	}
	callsAfterFirst := source.calls

	again, _ := history.History(ctx, room, domain.MessageQuery{Limit: 4})
	if fmt.Sprint(bodies(again)) != "[m10 m9 m8 m7]" || source.calls != callsAfterFirst {
		t.Fatalf("cached page must not hit the homeserver: %v, calls %d -> %d", bodies(again), callsAfterFirst, source.calls)
	}

	older, _ := history.History(ctx, room, domain.MessageQuery{Limit: 4, Before: domain.NewCursor(page[3])})
	if fmt.Sprint(bodies(older)) != "[m6 m5 m4 m3]" {
		t.Fatalf("second page: %v", bodies(older))
	}
}

func TestHistoryUsesSeveralRoundsAndRemembersTheStartOfTheRoom(t *testing.T) {
	ctx := context.Background()
	_, source, history := roomWithHistory(t)

	page, err := history.History(ctx, room, domain.MessageQuery{Limit: 50})
	if err != nil || len(page) != 10 {
		t.Fatalf("want all 10 messages, got %v, %v", bodies(page), err)
	}
	if source.calls < 3 {
		t.Fatalf("batch size 3 needs several rounds, got %d calls", source.calls)
	}

	calls := source.calls
	if _, err := history.History(ctx, room, domain.MessageQuery{Limit: 50}); err != nil || source.calls != calls {
		t.Fatalf("start of the room must be remembered: calls %d -> %d, %v", calls, source.calls, err)
	}
}

func TestHistoryAppliesFetchedEditsAndTombstones(t *testing.T) {
	ctx := context.Background()
	_, source, history := roomWithHistory(t)
	source.edits = []domain.Edit{{EventID: "$edit", TargetEventID: "$m5", RoomID: room, Sender: "@a:hs", Body: "m5 edited", At: minute(9)}}
	source.events[7] = domain.Message{ // m3 was deleted before the bot saw it
		EventID: "$m3", RoomID: room, Kind: domain.KindRedacted, SentAt: minute(3),
		Deletion: &domain.Deletion{At: minute(4), By: "@mod:hs"},
	}

	page, _ := history.History(ctx, room, domain.MessageQuery{Limit: 50})
	if len(page) != 9 {
		t.Fatalf("the deleted message must be hidden by default, got %v", bodies(page))
	}
	for _, msg := range page {
		if msg.EventID == "$m5" && (msg.Body != "m5 edited" || msg.EditedAt == nil) {
			t.Fatalf("fetched edit not applied: %+v", msg)
		}
	}
	deleted, _ := history.History(ctx, room, domain.MessageQuery{Deleted: domain.OnlyDeleted})
	if len(deleted) != 1 || deleted[0].Deletion.By != "@mod:hs" {
		t.Fatalf("tombstone missing: %+v", deleted)
	}
}

func TestHistoryFallsBackToStoredMessagesWhenHomeserverFails(t *testing.T) {
	_, source, history := roomWithHistory(t)
	source.err = errors.New("homeserver down")

	page, err := history.History(context.Background(), room, domain.MessageQuery{Limit: 10})
	if err != nil || fmt.Sprint(bodies(page)) != "[m10 m9]" {
		t.Fatalf("want stored messages, got %v, %v", bodies(page), err)
	}
}

func TestHistoryOnlyFetchesForEnabledRooms(t *testing.T) {
	ctx := context.Background()
	e, source, history := roomWithHistory(t)
	_ = e.feeds.Disable(ctx, room)

	page, err := history.History(ctx, room, domain.MessageQuery{Limit: 10})
	if err != nil || len(page) != 2 || source.calls != 0 {
		t.Fatalf("disabled room must be served from the database only: %v, calls %d, %v", bodies(page), source.calls, err)
	}
	if _, err := history.History(ctx, "!unknown:hs", domain.MessageQuery{}); !errors.Is(err, domain.ErrFeedNotFound) {
		t.Fatalf("want ErrFeedNotFound, got %v", err)
	}
}

func TestHistoryKeepsMessagesWithTheSameTimestampInOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, usecase.MessageOptions{})
	e.enable(t)
	source := &fakeSource{}
	for n := 5; n >= 1; n-- { // five messages sent within the same millisecond
		source.events = append(source.events, domain.Message{
			EventID: fmt.Sprintf("$m%d", n), RoomID: room, Body: fmt.Sprintf("m%d", n), SentAt: minute(1),
		})
	}
	history := usecase.NewHistoryService(e.store.Feeds(), e.store.Messages(), e.messages, source, slog.New(slog.NewTextHandler(io.Discard, nil)))

	page, err := history.History(ctx, room, domain.MessageQuery{Limit: 10})
	if err != nil || fmt.Sprint(bodies(page)) != "[m5 m4 m3 m2 m1]" {
		t.Fatalf("order of equal timestamps: %v, %v", bodies(page), err)
	}
}
