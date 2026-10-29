package usecase

import (
	"context"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

// maxConcurrentDownloads bounds how many files are held in memory at once.
const maxConcurrentDownloads = 4

// MediaSource fetches the file behind an attachment reference, decrypted. It returns
// domain.ErrMediaTooLarge for files above maxSize and domain.ErrMediaUnavailable for files the
// homeserver cannot deliver.
type MediaSource interface {
	Fetch(ctx context.Context, ref string, maxSize int64) ([]byte, error)
}

// MediaService hands out the files of stored messages. Files are not copied to disk: every request
// is answered from the homeserver, so a deleted message or a purged file is gone for readers too.
type MediaService struct {
	messages MessageRepository
	source   MediaSource
	maxSize  int64
	slots    chan struct{}
}

// NewMediaService returns a service that serves nothing when source is nil or maxSize is not positive.
func NewMediaService(messages MessageRepository, source MediaSource, maxSize int64) *MediaService {
	return &MediaService{
		messages: messages, source: source, maxSize: maxSize,
		slots: make(chan struct{}, maxConcurrentDownloads),
	}
}

// Enabled reports whether files can be served.
func (s *MediaService) Enabled() bool {
	return s != nil && s.source != nil && s.maxSize > 0
}

// Serves reports whether the attachment can be downloaded: files are served, it has a reference and
// it is not above the size limit. Readers are only pointed to files for which this holds.
func (s *MediaService) Serves(a *domain.Attachment) bool {
	return s.Enabled() && a != nil && a.Ref != "" && a.Size <= s.maxSize
}

// Open returns the message and the content of its attachment. Deleted messages and messages
// without a file look alike to the caller.
func (s *MediaService) Open(ctx context.Context, roomID, eventID string) (domain.Message, []byte, error) {
	if !s.Enabled() {
		return domain.Message{}, nil, domain.ErrMediaUnavailable
	}
	msg, err := s.messages.Get(ctx, roomID, eventID)
	if err != nil {
		return domain.Message{}, nil, err
	}
	if msg.Deletion != nil {
		return domain.Message{}, nil, domain.ErrMessageNotFound
	}
	if msg.Attachment == nil || msg.Attachment.Ref == "" {
		return domain.Message{}, nil, domain.ErrMediaUnavailable
	}
	if msg.Attachment.Size > s.maxSize {
		return domain.Message{}, nil, domain.ErrMediaTooLarge
	}

	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return domain.Message{}, nil, ctx.Err()
	}
	data, err := s.source.Fetch(ctx, msg.Attachment.Ref, s.maxSize)
	if err != nil {
		return domain.Message{}, nil, err
	}
	return msg, data, nil
}
