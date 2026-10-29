package matrix

import (
	"encoding/json"
	"strings"
	"time"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

var kinds = map[event.MessageType]domain.MessageKind{
	event.MsgText:     domain.KindText,
	event.MsgNotice:   domain.KindNotice,
	event.MsgEmote:    domain.KindEmote,
	event.MsgImage:    domain.KindImage,
	event.MsgVideo:    domain.KindVideo,
	event.MsgAudio:    domain.KindAudio,
	event.MsgFile:     domain.KindFile,
	event.MsgLocation: domain.KindLocation,
}

// msgTypeVerification is a request to verify a device, which is traffic between clients and no content.
const msgTypeVerification event.MessageType = "m.key.verification.request"

// Poll start events, in the stable and the unstable form.
var pollStartTypes = []event.Type{
	{Type: "m.poll.start", Class: event.MessageEventType},
	{Type: "org.matrix.msc3381.poll.start", Class: event.MessageEventType},
}

// editFallbackPrefix starts the body of an edit event for clients that do not understand edits.
const editFallbackPrefix = "* "

// mediaRef is what a stored attachment needs to be fetched again: the plain URL, or for encrypted
// files the URL together with the keys.
type mediaRef struct {
	URL  id.ContentURIString      `json:"url,omitempty"`
	File *event.EncryptedFileInfo `json:"file,omitempty"`
}

// toMessage converts a room event into a message. It returns false for events that are not content:
// edits, which toEdit handles, and message types that are traffic between clients.
// A message that arrives already deleted, as in fetched history, becomes a record without text.
func toMessage(evt *event.Event, senderName string) (domain.Message, bool) {
	msg := domain.Message{
		EventID:    evt.ID.String(),
		RoomID:     evt.RoomID.String(),
		Sender:     evt.Sender.String(),
		SenderName: senderName,
		SentAt:     toTime(evt.Timestamp),
	}
	if because := evt.Unsigned.RedactedBecause; because != nil {
		msg.Kind = domain.KindRedacted
		msg.Deletion = &domain.Deletion{At: toTime(because.Timestamp), By: because.Sender.String(), Reason: redactionReason(because)}
		return msg, true
	}

	// Homeservers do not always honour the type filter of a history request, so anything that is not
	// a message, such as the state events of the room, is rejected here.
	switch {
	case isPollStart(evt.Type):
		return pollMessage(evt, msg)
	case evt.Type == event.EventSticker:
		return stickerMessage(evt, msg)
	case evt.Type == event.EventMessage:
		return roomMessage(evt, msg)
	}
	return domain.Message{}, false
}

func roomMessage(evt *event.Event, msg domain.Message) (domain.Message, bool) {
	content := evt.Content.AsMessage()
	if content == nil || isEdit(content) || content.MsgType == msgTypeVerification {
		return domain.Message{}, false
	}
	msg.Kind = kinds[content.MsgType]
	if msg.Kind == "" {
		msg.Kind = domain.KindOther
	}
	fillText(&msg, content)
	fillRelations(&msg, content)

	switch msg.Kind {
	case domain.KindImage, domain.KindVideo, domain.KindAudio, domain.KindFile:
		msg.Attachment = attachmentOf(content)
	case domain.KindLocation:
		msg.Location = content.GeoURI
	}
	return msg, true
}

func stickerMessage(evt *event.Event, msg domain.Message) (domain.Message, bool) {
	content := evt.Content.AsMessage()
	if content == nil {
		return domain.Message{}, false
	}
	msg.Kind = domain.KindSticker
	fillText(&msg, content)
	fillRelations(&msg, content)
	msg.Attachment = attachmentOf(content)
	return msg, true
}

func fillText(msg *domain.Message, content *event.MessageEventContent) {
	content.RemoveReplyFallback()
	msg.Body = content.Body
	if content.Format == event.FormatHTML && content.FormattedBody != "" {
		msg.Formatted = sanitizeHTML(event.TrimReplyFallbackHTML(content.FormattedBody))
	}
}

func fillRelations(msg *domain.Message, content *event.MessageEventContent) {
	rel := content.OptionalGetRelatesTo()
	msg.ReplyTo = rel.GetNonFallbackReplyTo().String()
	msg.ThreadRoot = rel.GetThreadParent().String()
}

// attachmentOf describes the file of a message; it is nil when the message carries no file.
func attachmentOf(content *event.MessageEventContent) *domain.Attachment {
	ref := mediaRef{URL: content.URL}
	if content.File != nil {
		ref = mediaRef{File: content.File}
	}
	if ref.URL == "" && ref.File == nil {
		return nil
	}
	encoded, err := json.Marshal(ref)
	if err != nil {
		return nil
	}

	attachment := &domain.Attachment{
		Name:  content.GetFileName(),
		Voice: content.MSC3245Voice != nil,
		Ref:   string(encoded),
	}
	if info := content.Info; info != nil {
		attachment.MIMEType = info.MimeType
		attachment.Size = int64(info.Size)
		attachment.Width, attachment.Height = info.Width, info.Height
		attachment.Duration = time.Duration(info.Duration) * time.Millisecond
	}
	if audio := content.MSC1767Audio; audio != nil && attachment.Duration == 0 {
		attachment.Duration = time.Duration(audio.Duration) * time.Millisecond
	}
	return attachment
}

// toEdit converts an event that replaces the text of an earlier message.
func toEdit(evt *event.Event) (domain.Edit, bool) {
	content := evt.Content.AsMessage()
	if content == nil || !isEdit(content) {
		return domain.Edit{}, false
	}
	edit := domain.Edit{
		EventID:       evt.ID.String(),
		TargetEventID: content.OptionalGetRelatesTo().EventID.String(),
		RoomID:        evt.RoomID.String(),
		Sender:        evt.Sender.String(),
		Body:          strings.TrimPrefix(content.Body, editFallbackPrefix),
		At:            toTime(evt.Timestamp),
	}
	if replacement := content.NewContent; replacement != nil {
		edit.Body = replacement.Body
		if replacement.Format == event.FormatHTML && replacement.FormattedBody != "" {
			edit.Formatted = sanitizeHTML(replacement.FormattedBody)
		}
	}
	return edit, true
}

func isEdit(content *event.MessageEventContent) bool {
	rel := content.OptionalGetRelatesTo()
	return rel != nil && rel.Type == event.RelReplace && rel.EventID != ""
}

func isPollStart(t event.Type) bool {
	for _, known := range pollStartTypes {
		if t.Type == known.Type {
			return true
		}
	}
	return false
}

func redactionReason(redaction *event.Event) string {
	_ = redaction.Content.ParseRaw(redaction.Type)
	if content := redaction.Content.AsRedaction(); content != nil {
		return content.Reason
	}
	return ""
}

func toTime(millis int64) time.Time {
	return time.UnixMilli(millis).UTC()
}
