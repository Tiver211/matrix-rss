package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MessageKind is the transport-independent type of a chat message.
type MessageKind string

const (
	KindText   MessageKind = "text"
	KindNotice MessageKind = "notice"
	KindEmote  MessageKind = "emote"
	KindImage  MessageKind = "image"
	KindVideo  MessageKind = "video"
	KindAudio  MessageKind = "audio"
	KindFile   MessageKind = "file"
	// KindSticker, KindLocation and KindPoll are events other than plain chat messages.
	KindSticker  MessageKind = "sticker"
	KindLocation MessageKind = "location"
	KindPoll     MessageKind = "poll"
	// KindOther is a message of a type the bot does not know; its text is kept.
	KindOther MessageKind = "other"
	// KindRedacted marks a message that was already deleted when the bot first saw it, so its type is unknown.
	KindRedacted MessageKind = "redacted"
)

// Attachment describes the file of an image, video, audio, voice message, file or sticker.
type Attachment struct {
	// Name is the file name.
	Name     string
	MIMEType string
	// Size is in bytes, 0 if unknown.
	Size          int64
	Width, Height int
	Duration      time.Duration
	// Voice marks a voice message.
	Voice bool
	// Ref is an opaque reference that the media source resolves to the file, including the keys of an
	// encrypted one. It is empty when the file cannot be fetched.
	Ref string
}

// Message is a single chat message. Body and Formatted always hold the current version.
type Message struct {
	// Seq is a local, unique row number.
	Seq        uint64
	EventID    string
	RoomID     string
	Sender     string
	SenderName string
	Kind       MessageKind
	// Body is the plain text, or the caption or file name of an attachment.
	Body string
	// Formatted is the text as sanitized HTML; empty for plain messages.
	Formatted  string
	Attachment *Attachment
	// Location is the geo: URI of a location message.
	Location string
	// ReplyTo is the event this message answers; ThreadRoot the first message of its thread.
	ReplyTo    string
	ThreadRoot string
	SentAt     time.Time
	// EditedAt is set when the message was edited; the previous versions are available as revisions.
	EditedAt *time.Time
	// Deletion is set when the message was deleted in the room.
	Deletion *Deletion
}

// Deletion records who deleted a message and when.
type Deletion struct {
	At     time.Time
	By     string
	Reason string
}

// Edit is a replacement of the text of an earlier message.
type Edit struct {
	EventID       string
	TargetEventID string
	RoomID        string
	Sender        string
	Body          string
	Formatted     string
	At            time.Time
}

// Revision is one version of a message text; the first one is the original.
type Revision struct {
	EventID   string
	Body      string
	CreatedAt time.Time
}

// DeletedFilter says how deleted messages take part in a listing.
type DeletedFilter int

const (
	ExcludeDeleted DeletedFilter = iota
	IncludeDeleted
	OnlyDeleted
)

// ParseDeletedFilter understands "exclude" (also empty), "include" and "only".
func ParseDeletedFilter(value string) (DeletedFilter, error) {
	switch value {
	case "", "exclude":
		return ExcludeDeleted, nil
	case "include":
		return IncludeDeleted, nil
	case "only":
		return OnlyDeleted, nil
	}
	return ExcludeDeleted, fmt.Errorf("unknown deleted filter %q", value)
}

// Cursor marks a position in the chronological message order.
type Cursor struct {
	SentAt time.Time
	Seq    uint64
}

// NewCursor returns the position of the message.
func NewCursor(msg Message) *Cursor {
	return &Cursor{SentAt: msg.SentAt, Seq: msg.Seq}
}

// String encodes the cursor as "<unix milliseconds>-<seq>".
func (c Cursor) String() string {
	return strconv.FormatInt(c.SentAt.UnixMilli(), 10) + "-" + strconv.FormatUint(c.Seq, 10)
}

// ParseCursor is the inverse of Cursor.String.
func ParseCursor(value string) (*Cursor, error) {
	millis, seq, ok := strings.Cut(value, "-")
	if !ok {
		return nil, fmt.Errorf("invalid cursor %q", value)
	}
	ms, err := strconv.ParseInt(millis, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor %q", value)
	}
	n, err := strconv.ParseUint(seq, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor %q", value)
	}
	return &Cursor{SentAt: time.UnixMilli(ms).UTC(), Seq: n}, nil
}

// MessageQuery selects a page of messages, newest first.
// A nil Before means "from the newest message".
type MessageQuery struct {
	Limit   int
	Before  *Cursor
	Deleted DeletedFilter
}
