package matrix

import (
	"strings"
	"time"

	"maunium.net/go/mautrix/event"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

var kinds = map[event.MessageType]domain.MessageKind{
	event.MsgText:   domain.KindText,
	event.MsgNotice: domain.KindNotice,
	event.MsgEmote:  domain.KindEmote,
	event.MsgImage:  domain.KindImage,
	event.MsgVideo:  domain.KindVideo,
	event.MsgAudio:  domain.KindAudio,
	event.MsgFile:   domain.KindFile,
}

// editFallbackPrefix starts the body of an edit event for clients that do not understand edits.
const editFallbackPrefix = "* "

// toMessage converts a room message event. It returns false for events that are not feed material:
// unsupported message types and edits, which are handled by toEdit.
// A message that arrives already deleted, as in fetched history, becomes a record without text.
func toMessage(evt *event.Event, senderName string, mediaURL func(event.MessageEventContent) string) (domain.Message, bool) {
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

	content := evt.Content.AsMessage()
	if content == nil || isEdit(content) {
		return domain.Message{}, false
	}
	kind, ok := kinds[content.MsgType]
	if !ok {
		return domain.Message{}, false
	}
	msg.Kind = kind
	msg.Body = content.Body
	msg.MediaURL = mediaURL(*content)
	return msg, true
}

// toEdit converts an event that replaces the text of an earlier message.
func toEdit(evt *event.Event) (domain.Edit, bool) {
	content := evt.Content.AsMessage()
	if content == nil || !isEdit(content) {
		return domain.Edit{}, false
	}
	body := strings.TrimPrefix(content.Body, editFallbackPrefix)
	if content.NewContent != nil {
		body = content.NewContent.Body
	}
	return domain.Edit{
		EventID:       evt.ID.String(),
		TargetEventID: content.OptionalGetRelatesTo().EventID.String(),
		RoomID:        evt.RoomID.String(),
		Sender:        evt.Sender.String(),
		Body:          body,
		At:            toTime(evt.Timestamp),
	}, true
}

func isEdit(content *event.MessageEventContent) bool {
	rel := content.OptionalGetRelatesTo()
	return rel != nil && rel.Type == event.RelReplace && rel.EventID != ""
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
