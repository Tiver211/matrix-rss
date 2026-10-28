// Package matrix connects the application to a Matrix homeserver, including encrypted rooms.
package matrix

import (
	"context"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

// FeedManager is what the bot needs from the feed use cases.
type FeedManager interface {
	Enable(ctx context.Context, roomID, title string) (domain.Feed, error)
	Disable(ctx context.Context, roomID string) error
}

// FeedLister lists the published rooms.
type FeedLister interface {
	ListEnabled(ctx context.Context) ([]domain.Feed, error)
}

// AccessManager is what the bot needs to issue and revoke credentials.
// Secrets it returns are shown to the admin once.
type AccessManager interface {
	EnsureDefaultFeedToken(ctx context.Context, roomID, createdBy string) (domain.FeedToken, string, error)
	RotateFeedTokens(ctx context.Context, roomID, createdBy string) (domain.FeedToken, string, error)
	IssueFeedToken(ctx context.Context, roomID, name, createdBy string) (domain.FeedToken, string, error)
	ListFeedTokens(ctx context.Context, roomID string) ([]domain.FeedToken, error)
	FindFeedToken(ctx context.Context, id uint64) (domain.FeedToken, error)
	RevokeFeedToken(ctx context.Context, id uint64) error
	CreateAPIKey(ctx context.Context, req domain.KeyRequest) (domain.APIKey, string, error)
	ListAPIKeys(ctx context.Context) ([]domain.APIKey, error)
	FindAPIKey(ctx context.Context, id uint64) (domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, id uint64) error
}

// RoomAuthority tells whether a user administers a room, i.e. is a member with a high enough power level.
type RoomAuthority interface {
	IsRoomAdmin(ctx context.Context, roomID, userID string) bool
}

// PrivateChats tells whether a room is a private chat between the bot and one user.
type PrivateChats interface {
	IsPrivateChat(ctx context.Context, roomID, userID string) bool
}

// MessageRecorder is what the bot needs from the message use cases.
type MessageRecorder interface {
	Record(ctx context.Context, msg domain.Message) error
	Edit(ctx context.Context, edit domain.Edit) error
	Delete(ctx context.Context, roomID, eventID string, deletion domain.Deletion) error
}

// RoomNamer resolves a human-readable room title.
type RoomNamer interface {
	RoomName(ctx context.Context, roomID string) string
}
