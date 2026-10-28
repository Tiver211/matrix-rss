package matrix

import (
	"testing"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func noMedia(event.MessageEventContent) string { return "" }

func messageEvent(content *event.MessageEventContent) *event.Event {
	return &event.Event{
		ID: "$e", RoomID: "!r:hs", Sender: "@u:hs", Timestamp: 1_700_000_000_000,
		Type: event.EventMessage, Content: event.Content{Parsed: content},
	}
}

func TestEditIsConvertedToEditNotMessage(t *testing.T) {
	evt := messageEvent(&event.MessageEventContent{
		MsgType:    event.MsgText,
		Body:       "* fixed",
		NewContent: &event.MessageEventContent{MsgType: event.MsgText, Body: "fixed"},
		RelatesTo:  &event.RelatesTo{Type: event.RelReplace, EventID: "$orig"},
	})

	if _, ok := toMessage(evt, "U", noMedia); ok {
		t.Fatal("an edit must not be recorded as a new message")
	}
	edit, ok := toEdit(evt)
	if !ok || edit.TargetEventID != "$orig" || edit.Body != "fixed" || edit.Sender != "@u:hs" {
		t.Fatalf("unexpected edit: %+v, %v", edit, ok)
	}
}

func TestEditWithoutNewContentStripsFallbackPrefix(t *testing.T) {
	evt := messageEvent(&event.MessageEventContent{
		MsgType:   event.MsgText,
		Body:      "* fixed",
		RelatesTo: &event.RelatesTo{Type: event.RelReplace, EventID: "$orig"},
	})
	if edit, ok := toEdit(evt); !ok || edit.Body != "fixed" {
		t.Fatalf("unexpected edit: %+v, %v", edit, ok)
	}
}

func TestPlainMessage(t *testing.T) {
	evt := messageEvent(&event.MessageEventContent{MsgType: event.MsgImage, Body: "cat.png"})

	msg, ok := toMessage(evt, "Alice", func(event.MessageEventContent) string { return "http://media" })
	if !ok || msg.Kind != "image" || msg.Body != "cat.png" || msg.MediaURL != "http://media" || msg.SenderName != "Alice" {
		t.Fatalf("unexpected message: %+v, %v", msg, ok)
	}
	if _, isEdit := toEdit(evt); isEdit {
		t.Fatal("a plain message is not an edit")
	}
}

func TestAlreadyRedactedEventBecomesTombstone(t *testing.T) {
	evt := &event.Event{
		ID: "$e", RoomID: "!r:hs", Sender: "@u:hs", Timestamp: 1_700_000_000_000, Type: event.EventMessage,
		Unsigned: event.Unsigned{RedactedBecause: &event.Event{
			Type: event.EventRedaction, Sender: id.UserID("@mod:hs"), Timestamp: 1_700_000_100_000,
			Content: event.Content{Parsed: &event.RedactionEventContent{Reason: "spam"}},
		}},
	}

	msg, ok := toMessage(evt, "U", noMedia)
	if !ok || msg.Kind != "redacted" || msg.Deletion == nil || msg.Deletion.By != "@mod:hs" || msg.Deletion.Reason != "spam" {
		t.Fatalf("unexpected tombstone: %+v, %v", msg, ok)
	}
	if !msg.Deletion.At.After(msg.SentAt) {
		t.Fatalf("deletion time %v must follow send time %v", msg.Deletion.At, msg.SentAt)
	}
}
