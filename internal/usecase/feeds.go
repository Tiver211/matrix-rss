package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

// FeedService manages which rooms are published as feeds. Who may read a feed is up to AccessService.
type FeedService struct {
	feeds FeedRepository
	now   func() time.Time
}

func NewFeedService(feeds FeedRepository) *FeedService {
	return &FeedService{feeds: feeds, now: time.Now}
}

// Enable publishes the room, creating the feed on first use.
func (s *FeedService) Enable(ctx context.Context, roomID, title string) (domain.Feed, error) {
	feed, err := s.feeds.FindByRoom(ctx, roomID)
	if errors.Is(err, domain.ErrFeedNotFound) {
		return s.create(ctx, roomID, title)
	}
	if err != nil {
		return domain.Feed{}, err
	}

	feed.Enabled = true
	if title != "" {
		feed.Title = title
	}
	return feed, s.feeds.Save(ctx, feed)
}

// Disable stops recording the room and hides its feed. Stored messages are kept.
func (s *FeedService) Disable(ctx context.Context, roomID string) error {
	feed, err := s.feeds.FindByRoom(ctx, roomID)
	if err != nil {
		return err
	}
	feed.Enabled = false
	return s.feeds.Save(ctx, feed)
}

func (s *FeedService) Find(ctx context.Context, roomID string) (domain.Feed, error) {
	return s.feeds.FindByRoom(ctx, roomID)
}

func (s *FeedService) ListEnabled(ctx context.Context) ([]domain.Feed, error) {
	return s.feeds.ListEnabled(ctx)
}

func (s *FeedService) create(ctx context.Context, roomID, title string) (domain.Feed, error) {
	if title == "" {
		title = roomID
	}
	feed := domain.Feed{RoomID: roomID, Title: title, Enabled: true, CreatedAt: s.now()}
	return feed, s.feeds.Save(ctx, feed)
}
