package usecase

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

const (
	// backfillBatch is how many events one request to the homeserver asks for.
	backfillBatch = 100
	// maxBackfillRounds bounds the requests made for one API call, e.g. when a room is full of
	// events that cannot be used (state changes, messages that cannot be decrypted).
	maxBackfillRounds = 5
)

// HistoryCursor says where to continue reading room history backwards.
// Start with the event the stored history begins at (empty: the newest events of the room),
// then continue with the Next cursor of the previous page.
type HistoryCursor struct {
	EventID string
	Token   string
}

// HistoryPage is a batch of older room events converted to domain objects.
type HistoryPage struct {
	Messages []domain.Message
	Edits    []domain.Edit
	// Next continues the history; its Token is empty once the start of the room was reached.
	Next HistoryCursor
}

// HistorySource reads room history from the homeserver.
type HistorySource interface {
	Older(ctx context.Context, roomID string, from HistoryCursor, limit int) (HistoryPage, error)
}

// HistoryService serves message history for the API. The database is the cache: pages are answered
// from it, and when it holds too few messages the missing older ones are fetched from the homeserver
// and stored. Only published rooms are fetched.
type HistoryService struct {
	feeds    FeedRepository
	messages MessageRepository
	service  *MessageService
	source   HistorySource
	log      *slog.Logger

	batchSize int
	group     singleflight.Group
	mu        sync.Mutex
	exhausted map[string]string // room ID -> oldest stored event that has nothing older on the homeserver
}

// NewHistoryService returns a service that never contacts the homeserver when source is nil.
func NewHistoryService(feeds FeedRepository, messages MessageRepository, service *MessageService, source HistorySource, log *slog.Logger) *HistoryService {
	return &HistoryService{
		feeds: feeds, messages: messages, service: service, source: source, log: log,
		batchSize: backfillBatch,
		exhausted: map[string]string{},
	}
}

// WithBatchSize sets how many events one request to the homeserver asks for.
func (s *HistoryService) WithBatchSize(n int) *HistoryService {
	s.batchSize = n
	return s
}

// History returns a page of messages, newest first. If the homeserver cannot be reached the
// stored messages are returned.
func (s *HistoryService) History(ctx context.Context, roomID string, query domain.MessageQuery) ([]domain.Message, error) {
	query = normalize(query)
	feed, err := s.feeds.FindByRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	page, err := s.messages.List(ctx, roomID, query)
	if err != nil || !s.shouldBackfill(feed, query, len(page)) {
		return page, err
	}

	if err := s.backfill(ctx, roomID, query); err != nil {
		s.log.WarnContext(ctx, "cannot fetch history from the homeserver", "room_id", roomID, "error", err)
	}
	return s.messages.List(ctx, roomID, query)
}

func (s *HistoryService) shouldBackfill(feed domain.Feed, query domain.MessageQuery, found int) bool {
	// Deleted messages are rare, so a short "only deleted" page says nothing about missing history.
	return s.source != nil && feed.Enabled && found < query.Limit && query.Deleted != domain.OnlyDeleted
}

func (s *HistoryService) backfill(ctx context.Context, roomID string, query domain.MessageQuery) error {
	anchor, err := s.oldestEventID(ctx, roomID)
	if err != nil || s.isExhausted(roomID, anchor) {
		return err
	}
	_, err, _ = s.group.Do(roomID+"|"+anchor, func() (any, error) {
		return nil, s.fetchOlder(ctx, roomID, anchor, query)
	})
	return err
}

func (s *HistoryService) fetchOlder(ctx context.Context, roomID, anchor string, query domain.MessageQuery) error {
	var edits []domain.Edit
	// Edits may be fetched before their originals, so they are applied once everything is stored.
	defer func() { s.applyEdits(ctx, edits) }()

	cursor := HistoryCursor{EventID: anchor}
	for round := 0; round < maxBackfillRounds; round++ {
		page, err := s.source.Older(ctx, roomID, cursor, s.batchSize)
		if err != nil {
			return err
		}
		// The page is newest first. Storing it oldest first keeps messages with the same timestamp in order.
		for i := len(page.Messages) - 1; i >= 0; i-- {
			if err := s.service.Record(ctx, page.Messages[i]); err != nil {
				return err
			}
		}
		edits = append(edits, page.Edits...)

		if page.Next.Token == "" {
			// The stored history now begins further back; remember that nothing lies before that.
			oldest, err := s.oldestEventID(ctx, roomID)
			s.markExhausted(roomID, oldest)
			return err
		}
		cursor = page.Next
		if enough, err := s.hasEnough(ctx, roomID, query); err != nil || enough {
			return err
		}
	}
	return nil
}

func (s *HistoryService) hasEnough(ctx context.Context, roomID string, query domain.MessageQuery) (bool, error) {
	page, err := s.messages.List(ctx, roomID, query)
	return len(page) >= query.Limit, err
}

func (s *HistoryService) applyEdits(ctx context.Context, edits []domain.Edit) {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].At.Before(edits[j].At) })
	for _, edit := range edits {
		if err := s.service.Edit(ctx, edit); err != nil {
			s.log.WarnContext(ctx, "cannot apply fetched edit", "event_id", edit.EventID, "error", err)
		}
	}
}

// oldestEventID returns the event the stored history begins at, or "" for a room without stored messages.
func (s *HistoryService) oldestEventID(ctx context.Context, roomID string) (string, error) {
	oldest, err := s.messages.Oldest(ctx, roomID)
	if errors.Is(err, domain.ErrMessageNotFound) {
		return "", nil
	}
	return oldest.EventID, err
}

func (s *HistoryService) isExhausted(roomID, anchor string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	known, ok := s.exhausted[roomID]
	return ok && known == anchor
}

func (s *HistoryService) markExhausted(roomID, anchor string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exhausted[roomID] = anchor
}
