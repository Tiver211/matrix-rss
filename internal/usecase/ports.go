// Package usecase contains the application logic. It talks to the outside world
// only through the interfaces declared here.
package usecase

import (
	"context"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

// FeedRepository persists feeds. Lookups return domain.ErrFeedNotFound when nothing matches.
type FeedRepository interface {
	// Save inserts the feed or updates the existing one with the same room ID.
	Save(ctx context.Context, feed domain.Feed) error
	FindByRoom(ctx context.Context, roomID string) (domain.Feed, error)
	ListEnabled(ctx context.Context) ([]domain.Feed, error)
}

// MessageRepository persists messages together with their edit and deletion history.
// Lookups return domain.ErrMessageNotFound when nothing matches.
type MessageRepository interface {
	// Add stores the message; a message with an already known event ID is ignored.
	Add(ctx context.Context, msg domain.Message) error
	Get(ctx context.Context, roomID, eventID string) (domain.Message, error)
	// Oldest returns the chronologically first stored message of the room, deleted ones included.
	Oldest(ctx context.Context, roomID string) (domain.Message, error)
	// List returns messages of a room in reverse chronological order.
	List(ctx context.Context, roomID string, query domain.MessageQuery) ([]domain.Message, error)
	// SaveEdit records the edit as a revision and makes it the current text unless a newer edit exists.
	// The first edit also stores the original text as the first revision. Known edits are ignored.
	SaveEdit(ctx context.Context, edit domain.Edit) error
	Revisions(ctx context.Context, roomID, eventID string) ([]domain.Revision, error)
	// MarkDeleted records the deletion. With wipeContent the text, the media link and the revisions are erased too.
	// A message that is unknown or already deleted is left alone.
	MarkDeleted(ctx context.Context, roomID, eventID string, deletion domain.Deletion, wipeContent bool) error
	// DeleteOlderThan removes messages sent before the cutoff with their revisions and returns their count.
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// APIKeyRepository persists API keys by the hash of their secret.
// Unknown IDs and hashes give domain.ErrCredentialNotFound.
type APIKeyRepository interface {
	// Create stores the key and sets its ID.
	Create(ctx context.Context, key *domain.APIKey, secretHash string) error
	// FindByHash also returns revoked and expired keys; the caller decides.
	FindByHash(ctx context.Context, secretHash string) (domain.APIKey, error)
	Get(ctx context.Context, id uint64) (domain.APIKey, error)
	List(ctx context.Context) ([]domain.APIKey, error)
	// Revoke is idempotent: a key that is already revoked keeps its first revocation time.
	Revoke(ctx context.Context, id uint64, at time.Time) error
}

// FeedTokenRepository persists feed tokens by the hash of their secret.
// Unknown IDs and hashes give domain.ErrCredentialNotFound.
type FeedTokenRepository interface {
	// Create stores the token and sets its ID.
	Create(ctx context.Context, token *domain.FeedToken, secretHash string) error
	// FindByHash also returns revoked tokens; the caller decides.
	FindByHash(ctx context.Context, secretHash string) (domain.FeedToken, error)
	Get(ctx context.Context, id uint64) (domain.FeedToken, error)
	ListByRoom(ctx context.Context, roomID string) ([]domain.FeedToken, error)
	Revoke(ctx context.Context, id uint64, at time.Time) error
	RevokeAllForRoom(ctx context.Context, roomID string, at time.Time) error
}

// TokenGenerator produces unguessable secrets.
type TokenGenerator interface {
	New() (string, error)
}
