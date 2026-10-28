package usecase

import (
	"context"
	"time"
)

// RetentionService deletes messages that are older than the configured age.
type RetentionService struct {
	messages MessageRepository
	maxAge   time.Duration
	now      func() time.Time
}

// NewRetentionService returns a service that keeps everything when maxAge is zero.
func NewRetentionService(messages MessageRepository, maxAge time.Duration) *RetentionService {
	return &RetentionService{messages: messages, maxAge: maxAge, now: time.Now}
}

func (s *RetentionService) Enabled() bool {
	return s.maxAge > 0
}

// Purge removes expired messages and returns how many were deleted.
func (s *RetentionService) Purge(ctx context.Context) (int64, error) {
	if !s.Enabled() {
		return 0, nil
	}
	return s.messages.DeleteOlderThan(ctx, s.now().Add(-s.maxAge))
}
