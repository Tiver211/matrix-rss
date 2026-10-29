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
	return domain.Feed{RoomID: m.RoomID, Title: m.Title, Enabled: m.Enabled, CreatedAt: m.CreatedAt.UTC()}
}

type messageModel struct {
	Seq          uint64            `gorm:"primaryKey;autoIncrement;index:idx_messages_room_time,priority:3"`
	EventID      string            `gorm:"not null;size:255;uniqueIndex:idx_messages_room_event"`
	RoomID       string            `gorm:"not null;size:255;uniqueIndex:idx_messages_room_event;index:idx_messages_room_time,priority:1"`
	Sender       string            `gorm:"not null"`
	SenderName   string            `gorm:"not null"`
	Kind         string            `gorm:"not null"`
	Body         string            `gorm:"not null"`
	Formatted    string            `gorm:"not null;default:''"`
	Attachment   *attachmentRecord `gorm:"serializer:json;type:text"`
	Location     string            `gorm:"not null;default:''"`
	ReplyTo      string            `gorm:"not null;default:''"`
	ThreadRoot   string            `gorm:"not null;default:''"`
	SentAt       time.Time         `gorm:"not null;index;index:idx_messages_room_time,priority:2"`
	EditedAt     *time.Time
	DeletedAt    *time.Time
	DeletedBy    string `gorm:"not null;default:''"`
	DeleteReason string `gorm:"not null;default:''"`
}

func (messageModel) TableName() string { return "messages" }

func newMessageModel(m domain.Message) messageModel {
	model := messageModel{
		EventID: m.EventID, RoomID: m.RoomID, Sender: m.Sender, SenderName: m.SenderName,
		Kind: string(m.Kind), Body: m.Body, Formatted: m.Formatted, Attachment: newAttachmentRecord(m.Attachment),
		Location: m.Location, ReplyTo: m.ReplyTo, ThreadRoot: m.ThreadRoot, SentAt: m.SentAt, EditedAt: m.EditedAt,
	}
	if m.Deletion != nil {
		model.DeletedAt, model.DeletedBy, model.DeleteReason = &m.Deletion.At, m.Deletion.By, m.Deletion.Reason
	}
	return model
}

func (m messageModel) toDomain() domain.Message {
	msg := domain.Message{
		Seq: m.Seq, EventID: m.EventID, RoomID: m.RoomID, Sender: m.Sender, SenderName: m.SenderName,
		Kind: domain.MessageKind(m.Kind), Body: m.Body, Formatted: m.Formatted, Attachment: m.Attachment.toDomain(),
		Location: m.Location, ReplyTo: m.ReplyTo, ThreadRoot: m.ThreadRoot, SentAt: m.SentAt.UTC(), EditedAt: utcOf(m.EditedAt),
	}
	if m.DeletedAt != nil {
		msg.Deletion = &domain.Deletion{At: m.DeletedAt.UTC(), By: m.DeletedBy, Reason: m.DeleteReason}
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
	return domain.Revision{EventID: m.RevisionID, Body: m.Body, CreatedAt: m.CreatedAt.UTC()}
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
		ID: m.ID, Name: m.Name, Hint: m.Hint, Rooms: m.Rooms, ExpiresAt: utcOf(m.ExpiresAt),
		CreatedAt: m.CreatedAt.UTC(), CreatedBy: m.CreatedBy, RevokedAt: utcOf(m.RevokedAt),
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
		CreatedAt: m.CreatedAt.UTC(), CreatedBy: m.CreatedBy, RevokedAt: utcOf(m.RevokedAt),
	}
}

// attachmentRecord is how an attachment is stored, as JSON in one column.
type attachmentRecord struct {
	Name       string `json:"name,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Voice      bool   `json:"voice,omitempty"`
	Ref        string `json:"ref,omitempty"`
}

func newAttachmentRecord(a *domain.Attachment) *attachmentRecord {
	if a == nil {
		return nil
	}
	return &attachmentRecord{
		Name: a.Name, MIMEType: a.MIMEType, Size: a.Size, Width: a.Width, Height: a.Height,
		DurationMS: a.Duration.Milliseconds(), Voice: a.Voice, Ref: a.Ref,
	}
}

func (r *attachmentRecord) toDomain() *domain.Attachment {
	if r == nil {
		return nil
	}
	return &domain.Attachment{
		Name: r.Name, MIMEType: r.MIMEType, Size: r.Size, Width: r.Width, Height: r.Height,
		Duration: time.Duration(r.DurationMS) * time.Millisecond, Voice: r.Voice, Ref: r.Ref,
	}
}

// utcOf expresses an optional time in UTC: some databases return times in the zone of the server.
func utcOf(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}
