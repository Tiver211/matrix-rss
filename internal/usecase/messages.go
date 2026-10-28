package usecase

import (
	"context"
	"errors"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// MessageOptions tune how messages are kept.
type MessageOptions struct {
	// KeepDeletedContent keeps the text of deleted messages. Otherwise only the deletion record
	// (who, when, why) is kept and the text is erased.
	KeepDeletedContent bool
}

// MessageService records room messages with their edits and deletions and serves them back from the database.
type MessageService struct {
	feeds    FeedRepository
	messages MessageRepository
	options  MessageOptions
}

func NewMessageService(feeds FeedRepository, messages MessageRepository, options MessageOptions) *MessageService {
	return &MessageService{feeds: feeds, messages: messages, options: options}
}

// Record stores the message if its room is currently published; otherwise it is silently dropped.
func (s *MessageService) Record(ctx context.Context, msg domain.Message) error {
	published, err := s.published(ctx, msg.RoomID)
	if err != nil || !published {
		return err
	}
	return s.messages.Add(ctx, msg)
}

// Edit applies an edit to a stored message. Edits of unknown or deleted messages and edits made
// by someone other than the author are ignored, as the Matrix specification requires.
func (s *MessageService) Edit(ctx context.Context, edit domain.Edit) error {
	published, err := s.published(ctx, edit.RoomID)
	if err != nil || !published {
		return err
	}
	original, err := s.messages.Get(ctx, edit.RoomID, edit.TargetEventID)
	if errors.Is(err, domain.ErrMessageNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if original.Sender != edit.Sender || original.Deletion != nil {
		return nil
	}

	err = s.messages.SaveEdit(ctx, edit)
	if errors.Is(err, domain.ErrMessageNotFound) {
		return nil
	}
	return err
}

// Delete records that a message was deleted in the room.
func (s *MessageService) Delete(ctx context.Context, roomID, eventID string, deletion domain.Deletion) error {
	return s.messages.MarkDeleted(ctx, roomID, eventID, deletion, !s.options.KeepDeletedContent)
}

// Page returns a page of stored messages, newest first. Disabled rooms stay readable.
func (s *MessageService) Page(ctx context.Context, roomID string, query domain.MessageQuery) ([]domain.Message, error) {
	if _, err := s.feeds.FindByRoom(ctx, roomID); err != nil {
		return nil, err
	}
	return s.messages.List(ctx, roomID, normalize(query))
}

// Recent returns the newest messages of a room for its feed, without deleted ones.
func (s *MessageService) Recent(ctx context.Context, roomID string, limit int) ([]domain.Message, error) {
	return s.messages.List(ctx, roomID, normalize(domain.MessageQuery{Limit: limit}))
}

// Revisions returns the versions of a message text, oldest first. A message that was never edited
// has a single revision; a message deleted without keeping its content has an empty one.
func (s *MessageService) Revisions(ctx context.Context, roomID, eventID string) ([]domain.Revision, error) {
	msg, err := s.messages.Get(ctx, roomID, eventID)
	if err != nil {
		return nil, err
	}
	revisions, err := s.messages.Revisions(ctx, roomID, eventID)
	if err != nil {
		return nil, err
	}
	if len(revisions) == 0 {
		revisions = []domain.Revision{{EventID: msg.EventID, Body: msg.Body, CreatedAt: msg.SentAt}}
	}
	return revisions, nil
}

func (s *MessageService) published(ctx context.Context, roomID string) (bool, error) {
	feed, err := s.feeds.FindByRoom(ctx, roomID)
	if errors.Is(err, domain.ErrFeedNotFound) {
		return false, nil
	}
	return feed.Enabled, err
}

func normalize(query domain.MessageQuery) domain.MessageQuery {
	switch {
	case query.Limit <= 0:
		query.Limit = DefaultPageSize
	case query.Limit > MaxPageSize:
		query.Limit = MaxPageSize
	}
	return query
}
