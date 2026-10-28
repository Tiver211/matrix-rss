package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

type memFeeds map[string]domain.Feed

func (m memFeeds) Save(_ context.Context, f domain.Feed) error { m[f.RoomID] = f; return nil }
func (m memFeeds) FindByRoom(_ context.Context, id string) (domain.Feed, error) {
	if f, ok := m[id]; ok {
		return f, nil
	}
	return domain.Feed{}, domain.ErrFeedNotFound
}
func (m memFeeds) ListEnabled(context.Context) ([]domain.Feed, error) { return nil, nil }

func TestFeedLifecycle(t *testing.T) {
	ctx := context.Background()
	svc := NewFeedService(memFeeds{})

	created, err := svc.Enable(ctx, "!r:hs", "")
	if err != nil || created.Title != "!r:hs" || !created.Enabled {
		t.Fatalf("enable: %+v, %v", created, err)
	}
	if err := svc.Disable(ctx, "!r:hs"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	again, _ := svc.Enable(ctx, "!r:hs", "Room")
	if again.Title != "Room" || !again.Enabled {
		t.Fatalf("re-enable must update the feed: %+v", again)
	}
	if err := svc.Disable(ctx, "!none:hs"); !errors.Is(err, domain.ErrFeedNotFound) {
		t.Fatalf("want ErrFeedNotFound, got %v", err)
	}
}

// expiryRecorder implements only what the retention service uses.
type expiryRecorder struct {
	MessageRepository
	cutoff time.Time
}

func (e *expiryRecorder) DeleteOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	e.cutoff = cutoff
	return 1, nil
}

func TestRetentionPurge(t *testing.T) {
	store := &expiryRecorder{}
	svc := NewRetentionService(store, 48*time.Hour)
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	if _, err := svc.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(-48 * time.Hour); !store.cutoff.Equal(want) {
		t.Fatalf("cutoff = %v, want %v", store.cutoff, want)
	}
	if NewRetentionService(store, 0).Enabled() {
		t.Fatal("zero max age must disable retention")
	}
}
