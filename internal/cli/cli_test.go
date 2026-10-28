package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

type fakeFeeds struct{ err error }

func (f fakeFeeds) ListEnabled(context.Context) ([]domain.Feed, error) {
	return []domain.Feed{{RoomID: "!a:hs", Title: "Room A"}}, f.err
}
func (f fakeFeeds) Disable(context.Context, string) error { return f.err }

type fakeAccess struct {
	err        error
	keyRequest domain.KeyRequest
	revoked    []uint64
	issued     []string
}

func (f *fakeAccess) RotateFeedTokens(context.Context, string, string) (domain.FeedToken, string, error) {
	return domain.FeedToken{}, "new", f.err
}
func (f *fakeAccess) IssueFeedToken(_ context.Context, roomID, name, by string) (domain.FeedToken, string, error) {
	f.issued = append(f.issued, roomID+"/"+name+"/"+by)
	return domain.FeedToken{ID: 4, Name: name}, "issued", f.err
}
func (f *fakeAccess) ListFeedTokens(context.Context, string) ([]domain.FeedToken, error) {
	return []domain.FeedToken{{ID: 1, Name: "default", Hint: "abcd1234", CreatedBy: "@a:hs"}}, f.err
}
func (f *fakeAccess) RevokeFeedToken(_ context.Context, id uint64) error {
	f.revoked = append(f.revoked, id)
	return f.err
}
func (f *fakeAccess) CreateAPIKey(_ context.Context, req domain.KeyRequest) (domain.APIKey, string, error) {
	f.keyRequest = req
	return domain.APIKey{ID: 2, Name: req.Name}, "mxk_secret", f.err
}
func (f *fakeAccess) ListAPIKeys(context.Context) ([]domain.APIKey, error) {
	return []domain.APIKey{{ID: 2, Name: "grafana", Hint: "mxk_abcd", Rooms: []string{"!a:hs"}}}, f.err
}
func (f *fakeAccess) RevokeAPIKey(_ context.Context, id uint64) error {
	f.revoked = append(f.revoked, id)
	return f.err
}

type fakeMessages struct{ query domain.MessageQuery }

func (f *fakeMessages) Page(_ context.Context, _ string, q domain.MessageQuery) ([]domain.Message, error) {
	f.query = q
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	return []domain.Message{
		{Seq: 2, Sender: "@b:hs", SenderName: "Bob", Body: "second", SentAt: at, Deletion: &domain.Deletion{At: at, By: "@mod:hs", Reason: "spam"}},
		{Seq: 1, Sender: "@a:hs", Body: "first\nline", SentAt: at},
	}, nil
}

func (f *fakeMessages) Revisions(context.Context, string, string) ([]domain.Revision, error) {
	return []domain.Revision{{Body: "v1"}, {Body: "v2"}}, nil
}

func newRunner(feeds fakeFeeds, messages *fakeMessages) (*Runner, *bytes.Buffer, *bytes.Buffer) {
	runner, out, errOut, _ := newRunnerWithAccess(feeds, messages, &fakeAccess{})
	return runner, out, errOut
}

func newRunnerWithAccess(feeds fakeFeeds, messages *fakeMessages, access *fakeAccess) (*Runner, *bytes.Buffer, *bytes.Buffer, *fakeAccess) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	return &Runner{
		Feeds: feeds, Access: access, Messages: messages, Out: out, Err: errOut,
		FeedURL: func(token string) string { return "https://rss/feeds/" + token },
	}, out, errOut, access
}

func TestIsServe(t *testing.T) {
	if !IsServe(nil) || !IsServe([]string{"serve"}) || IsServe([]string{"rooms"}) {
		t.Fatal("unexpected IsServe result")
	}
}

func TestRooms(t *testing.T) {
	runner, out, _ := newRunner(fakeFeeds{}, &fakeMessages{})
	if err := runner.Run(context.Background(), []string{"rooms"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "!a:hs") || !strings.Contains(got, "Room A") {
		t.Fatalf("unexpected output:\n%s", got)
	}
}

func TestMessagesPrintsOldestFirstAndPassesFlags(t *testing.T) {
	messages := &fakeMessages{}
	runner, out, errOut := newRunner(fakeFeeds{}, messages)

	err := runner.Run(context.Background(), []string{"messages", "-limit", "5", "-before", "1000-9", "-deleted", "include", "!a:hs"})
	if err != nil {
		t.Fatal(err)
	}
	if messages.query.Limit != 5 || messages.query.Before == nil || messages.query.Before.Seq != 9 || messages.query.Deleted != domain.IncludeDeleted {
		t.Fatalf("flags not passed: %+v", messages.query)
	}
	lines := out.String()
	if strings.Index(lines, "first") > strings.Index(lines, "second") || !strings.Contains(lines, "Bob: second") || !strings.Contains(lines, "deleted") || !strings.Contains(lines, "spam") {
		t.Fatalf("expected chronological output:\n%s", lines)
	}
	if !strings.Contains(errOut.String(), "-before ") {
		t.Fatalf("expected paging hint, got %q", errOut.String())
	}
}

func TestHistoryPrintsRevisions(t *testing.T) {
	runner, out, _ := newRunner(fakeFeeds{}, &fakeMessages{})
	if err := runner.Run(context.Background(), []string{"history", "!a:hs", "$e"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Index(got, "v1") > strings.Index(got, "v2") || !strings.Contains(got, "v2") {
		t.Fatalf("unexpected output:\n%s", got)
	}
}

func TestUsageErrors(t *testing.T) {
	runner, _, errOut := newRunner(fakeFeeds{}, &fakeMessages{})
	for _, args := range [][]string{nil, {"bogus"}, {"rotate"}, {"disable", "a", "b"}, {"messages"}, {"history", "!a:hs"}, {"messages", "-deleted", "x", "!a:hs"}} {
		if err := runner.Run(context.Background(), args); !errors.Is(err, ErrUsage) {
			t.Errorf("%v: want ErrUsage, got %v", args, err)
		}
	}
	if !strings.Contains(errOut.String(), "Usage:") {
		t.Fatal("usage text missing")
	}
}

func TestMissingFeed(t *testing.T) {
	runner, _, _, _ := newRunnerWithAccess(fakeFeeds{}, &fakeMessages{}, &fakeAccess{err: domain.ErrFeedNotFound})
	err := runner.Run(context.Background(), []string{"rotate", "!x:hs"})
	if err == nil || err.Error() != "room has no feed" {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestRotateAndTokenCommandsPrintSecretsOnStdoutOnce(t *testing.T) {
	runner, out, errOut, access := newRunnerWithAccess(fakeFeeds{}, &fakeMessages{}, &fakeAccess{})
	ctx := context.Background()

	if err := runner.Run(ctx, []string{"rotate", "!a:hs"}); err != nil || strings.TrimSpace(out.String()) != "https://rss/feeds/new" {
		t.Fatalf("rotate: %q, %v", out, err)
	}
	out.Reset()
	if err := runner.Run(ctx, []string{"token", "create", "!a:hs", "bob"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "https://rss/feeds/issued" || access.issued[0] != "!a:hs/bob/cli" || strings.Contains(errOut.String(), "issued\n") {
		t.Fatalf("token create: out %q, err %q, issued %v", out, errOut, access.issued)
	}

	out.Reset()
	if err := runner.Run(ctx, []string{"token", "list", "!a:hs"}); err != nil || !strings.Contains(out.String(), "abcd1234") || strings.Contains(out.String(), "issued") {
		t.Fatalf("token list: %q, %v", out, err)
	}
	if err := runner.Run(ctx, []string{"token", "revoke", "1"}); err != nil || len(access.revoked) != 1 || access.revoked[0] != 1 {
		t.Fatalf("token revoke: %v, %v", access.revoked, err)
	}
}

func TestKeyCommands(t *testing.T) {
	runner, out, _, access := newRunnerWithAccess(fakeFeeds{}, &fakeMessages{}, &fakeAccess{})
	ctx := context.Background()

	err := runner.Run(ctx, []string{"key", "create", "-rooms", "!a:hs,!b:hs", "-expires", "30d", "grafana"})
	if err != nil || strings.TrimSpace(out.String()) != "mxk_secret" {
		t.Fatalf("create: %q, %v", out, err)
	}
	req := access.keyRequest
	if req.Name != "grafana" || len(req.Rooms) != 2 || req.CreatedBy != "cli" || req.ExpiresAt == nil || time.Until(*req.ExpiresAt) < 29*24*time.Hour {
		t.Fatalf("unexpected request %+v", req)
	}

	out.Reset()
	if err := runner.Run(ctx, []string{"key", "list"}); err != nil || !strings.Contains(out.String(), "grafana") || strings.Contains(out.String(), "mxk_secret") {
		t.Fatalf("list: %q, %v", out, err)
	}
	if err := runner.Run(ctx, []string{"key", "revoke", "2"}); err != nil || access.revoked[0] != 2 {
		t.Fatalf("revoke: %v, %v", access.revoked, err)
	}
}

func TestCredentialErrors(t *testing.T) {
	runner, _, _, _ := newRunnerWithAccess(fakeFeeds{}, &fakeMessages{}, &fakeAccess{err: domain.ErrCredentialNotFound})
	err := runner.Run(context.Background(), []string{"key", "revoke", "9"})
	if err == nil || !strings.Contains(err.Error(), "no key or token") {
		t.Fatalf("unexpected error %v", err)
	}

	for _, args := range [][]string{
		{"key"}, {"key", "bogus"}, {"key", "create"}, {"key", "create", "-expires", "soon", "k"}, {"key", "revoke", "x"},
		{"token"}, {"token", "create", "!a:hs"}, {"token", "list"}, {"token", "revoke"},
	} {
		if err := runner.Run(context.Background(), args); !errors.Is(err, ErrUsage) {
			t.Errorf("%v: want ErrUsage, got %v", args, err)
		}
	}
}
