package gormstore

import (
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

type feedModel struct {
	RoomID    string `gorm:"primaryKey;size:255"`
	Title     string `gorm:"not null"`
	Enabled   bool   `gorm:"not null"`
	CreatedAt time.Time
}

func (feedModel) TableName() string { return "feeds" }

func newFeedModel(f domain.Feed) feedModel {
	return feedModel{RoomID: f.RoomID, Title: f.Title, Enabled: f.Enabled, CreatedAt: f.CreatedAt}
}

func (m feedModel) toDomain() domain.Feed {
	return domain.Feed{RoomID: m.RoomID, Title: m.Title, Enabled: m.Enabled, CreatedAt: m.CreatedAt}
}

type messageModel struct {
	Seq          uint64    `gorm:"primaryKey;autoIncrement;index:idx_messages_room_time,priority:3"`
	EventID      string    `gorm:"not null;size:255;uniqueIndex:idx_messages_room_event"`
	RoomID       string    `gorm:"not null;size:255;uniqueIndex:idx_messages_room_event;index:idx_messages_room_time,priority:1"`
	Sender       string    `gorm:"not null"`
	SenderName   string    `gorm:"not null"`
	Kind         string    `gorm:"not null"`
	Body         string    `gorm:"not null"`
	MediaURL     string    `gorm:"not null"`
	SentAt       time.Time `gorm:"not null;index;index:idx_messages_room_time,priority:2"`
	EditedAt     *time.Time
	DeletedAt    *time.Time
	DeletedBy    string `gorm:"not null;default:''"`
	DeleteReason string `gorm:"not null;default:''"`
}

func (messageModel) TableName() string { return "messages" }

func newMessageModel(m domain.Message) messageModel {
	model := messageModel{
		EventID: m.EventID, RoomID: m.RoomID, Sender: m.Sender, SenderName: m.SenderName,
		Kind: string(m.Kind), Body: m.Body, MediaURL: m.MediaURL, SentAt: m.SentAt, EditedAt: m.EditedAt,
	}
	if m.Deletion != nil {
		model.DeletedAt, model.DeletedBy, model.DeleteReason = &m.Deletion.At, m.Deletion.By, m.Deletion.Reason
	}
	return model
}

func (m messageModel) toDomain() domain.Message {
	msg := domain.Message{
		Seq: m.Seq, EventID: m.EventID, RoomID: m.RoomID, Sender: m.Sender, SenderName: m.SenderName,
		Kind: domain.MessageKind(m.Kind), Body: m.Body, MediaURL: m.MediaURL, SentAt: m.SentAt, EditedAt: m.EditedAt,
	}
	if m.DeletedAt != nil {
		msg.Deletion = &domain.Deletion{At: *m.DeletedAt, By: m.DeletedBy, Reason: m.DeleteReason}
	}
	return msg
}

// revisionModel keeps one version of a message text. The original text is stored as the first revision
// when the message is edited for the first time; its RevisionID equals the message's event ID.
type revisionModel struct {
	Seq        uint64    `gorm:"primaryKey;autoIncrement"`
	RoomID     string    `gorm:"not null;size:255;uniqueIndex:idx_revisions_room_revision;index:idx_revisions_room_event,priority:1"`
	EventID    string    `gorm:"not null;size:255;index:idx_revisions_room_event,priority:2"`
	RevisionID string    `gorm:"not null;size:255;uniqueIndex:idx_revisions_room_revision"`
	Body       string    `gorm:"not null"`
	CreatedAt  time.Time `gorm:"not null"`
}

func (revisionModel) TableName() string { return "message_revisions" }

func (m revisionModel) toDomain() domain.Revision {
	return domain.Revision{EventID: m.RevisionID, Body: m.Body, CreatedAt: m.CreatedAt}
}

type apiKeyModel struct {
	ID        uint64   `gorm:"primaryKey;autoIncrement"`
	Name      string   `gorm:"not null"`
	Hash      string   `gorm:"not null;size:64;uniqueIndex"`
	Hint      string   `gorm:"not null"`
	Rooms     []string `gorm:"serializer:json;type:text"`
	ExpiresAt *time.Time
	CreatedAt time.Time `gorm:"not null"`
	CreatedBy string    `gorm:"not null"`
	RevokedAt *time.Time
}

func (apiKeyModel) TableName() string { return "api_keys" }

func (m apiKeyModel) toDomain() domain.APIKey {
	return domain.APIKey{
		ID: m.ID, Name: m.Name, Hint: m.Hint, Rooms: m.Rooms, ExpiresAt: m.ExpiresAt,
		CreatedAt: m.CreatedAt, CreatedBy: m.CreatedBy, RevokedAt: m.RevokedAt,
	}
}

type feedTokenModel struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	RoomID    string    `gorm:"not null;size:255;index"`
	Name      string    `gorm:"not null"`
	Hash      string    `gorm:"not null;size:64;uniqueIndex"`
	Hint      string    `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null"`
	CreatedBy string    `gorm:"not null"`
	RevokedAt *time.Time
}

func (feedTokenModel) TableName() string { return "feed_tokens" }

func (m feedTokenModel) toDomain() domain.FeedToken {
	return domain.FeedToken{
		ID: m.ID, RoomID: m.RoomID, Name: m.Name, Hint: m.Hint,
		CreatedAt: m.CreatedAt, CreatedBy: m.CreatedBy, RevokedAt: m.RevokedAt,
	}
}
