package matrix

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

func messageEvent(content *event.MessageEventContent) *event.Event {
	return &event.Event{
		ID: "$e", RoomID: "!r:hs", Sender: "@u:hs", Timestamp: 1_700_000_000_000,
		Type: event.EventMessage, Content: event.Content{Parsed: content},
	}
}

func convert(t *testing.T, evt *event.Event) domain.Message {
	t.Helper()
	msg, ok := toMessage(evt, "U")
	if !ok {
		t.Fatalf("the event must be recorded: %+v", evt)
	}
	return msg
}

func TestEditIsConvertedToEditNotMessage(t *testing.T) {
	evt := messageEvent(&event.MessageEventContent{
		MsgType:    event.MsgText,
		Body:       "* fixed",
		NewContent: &event.MessageEventContent{MsgType: event.MsgText, Body: "fixed", Format: event.FormatHTML, FormattedBody: "<b>fixed</b><script>x()</script>"},
		RelatesTo:  &event.RelatesTo{Type: event.RelReplace, EventID: "$orig"},
	})

	if _, ok := toMessage(evt, "U"); ok {
		t.Fatal("an edit must not be recorded as a new message")
	}
	edit, ok := toEdit(evt)
	if !ok || edit.TargetEventID != "$orig" || edit.Body != "fixed" || edit.Sender != "@u:hs" {
		t.Fatalf("unexpected edit: %+v, %v", edit, ok)
	}
	if edit.Formatted != "<b>fixed</b>" {
		t.Fatalf("the new formatting must be kept and sanitized: %q", edit.Formatted)
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

func TestPlainTextMessage(t *testing.T) {
	msg := convert(t, messageEvent(&event.MessageEventContent{MsgType: event.MsgText, Body: "hi"}))
	if msg.Kind != domain.KindText || msg.Body != "hi" || msg.Formatted != "" || msg.Attachment != nil || msg.ReplyTo != "" {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if _, isEdit := toEdit(messageEvent(&event.MessageEventContent{MsgType: event.MsgText, Body: "hi"})); isEdit {
		t.Fatal("a plain message is not an edit")
	}
}

func decodeRef(t *testing.T, a *domain.Attachment) mediaRef {
	t.Helper()
	var ref mediaRef
	if err := json.Unmarshal([]byte(a.Ref), &ref); err != nil {
		t.Fatalf("bad reference %q: %v", a.Ref, err)
	}
	return ref
}

func TestImageKeepsItsMetadataAndAReference(t *testing.T) {
	msg := convert(t, messageEvent(&event.MessageEventContent{
		MsgType: event.MsgImage, Body: "cat.png", URL: "mxc://hs/cat",
		Info: &event.FileInfo{MimeType: "image/png", Size: 2048, Width: 640, Height: 480},
	}))
	a := msg.Attachment
	if msg.Kind != domain.KindImage || a == nil || a.Name != "cat.png" || a.MIMEType != "image/png" || a.Size != 2048 || a.Width != 640 || a.Height != 480 || a.Voice {
		t.Fatalf("unexpected attachment: %+v", a)
	}
	if ref := decodeRef(t, a); ref.URL != "mxc://hs/cat" || ref.File != nil {
		t.Fatalf("unexpected reference: %+v", ref)
	}
}

func TestACaptionIsNotTheFileName(t *testing.T) {
	msg := convert(t, messageEvent(&event.MessageEventContent{
		MsgType: event.MsgImage, Body: "Look at this cat", FileName: "cat.png", URL: "mxc://hs/cat",
	}))
	if msg.Body != "Look at this cat" || msg.Attachment.Name != "cat.png" {
		t.Fatalf("caption and file name must stay apart: %+v / %+v", msg, msg.Attachment)
	}
}

func TestEncryptedFileKeepsItsKeys(t *testing.T) {
	file := attachment.NewEncryptedFile()
	msg := convert(t, messageEvent(&event.MessageEventContent{
		MsgType: event.MsgFile, Body: "report.pdf",
		File: &event.EncryptedFileInfo{EncryptedFile: *file, URL: "mxc://hs/enc"},
		Info: &event.FileInfo{MimeType: "application/pdf", Size: 10},
	}))
	ref := decodeRef(t, msg.Attachment)
	if ref.File == nil || ref.File.URL != "mxc://hs/enc" || ref.File.Key.Key == "" || ref.File.InitVector == "" || ref.URL != "" {
		t.Fatalf("the reference must carry the keys: %+v", ref)
	}
}

func TestAnImageWithoutAFileHasNoAttachment(t *testing.T) {
	msg := convert(t, messageEvent(&event.MessageEventContent{MsgType: event.MsgImage, Body: "broken"}))
	if msg.Kind != domain.KindImage || msg.Attachment != nil {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestVoiceMessage(t *testing.T) {
	msg := convert(t, messageEvent(&event.MessageEventContent{
		MsgType: event.MsgAudio, Body: "Voice message", URL: "mxc://hs/v",
		Info:         &event.FileInfo{MimeType: "audio/ogg", Size: 999},
		MSC3245Voice: &event.MSC3245Voice{},
		MSC1767Audio: &event.MSC1767Audio{Duration: 12340},
	}))
	a := msg.Attachment
	if msg.Kind != domain.KindAudio || !a.Voice || a.Duration != 12340*time.Millisecond {
		t.Fatalf("unexpected voice message: %+v", a)
	}
}

func TestSticker(t *testing.T) {
	evt := messageEvent(&event.MessageEventContent{Body: "Waving", URL: "mxc://hs/wave", Info: &event.FileInfo{MimeType: "image/webp", Width: 256, Height: 256}})
	evt.Type = event.EventSticker

	msg := convert(t, evt)
	if msg.Kind != domain.KindSticker || msg.Body != "Waving" || msg.Attachment == nil || msg.Attachment.MIMEType != "image/webp" {
		t.Fatalf("unexpected sticker: %+v", msg)
	}
}

func TestLocation(t *testing.T) {
	msg := convert(t, messageEvent(&event.MessageEventContent{MsgType: event.MsgLocation, Body: "Berlin", GeoURI: "geo:52.52,13.40"}))
	if msg.Kind != domain.KindLocation || msg.Location != "geo:52.52,13.40" || msg.Body != "Berlin" {
		t.Fatalf("unexpected location: %+v", msg)
	}
}

func TestPolls(t *testing.T) {
	stable := map[string]any{"m.poll": map[string]any{
		"question": map[string]any{"m.text": []any{map[string]any{"body": "Lunch?"}}},
		"answers": []any{
			map[string]any{"id": "a", "m.text": []any{map[string]any{"body": "Pizza"}}},
			map[string]any{"id": "b", "m.text": []any{map[string]any{"body": "Sushi"}}},
		},
	}}
	unstable := map[string]any{"org.matrix.msc3381.poll.start": map[string]any{
		"question": map[string]any{"org.matrix.msc1767.text": "Lunch?"},
		"answers": []any{
			map[string]any{"id": "a", "org.matrix.msc1767.text": "Pizza"},
			map[string]any{"id": "b", "org.matrix.msc1767.text": "Sushi"},
		},
	}}
	fallback := map[string]any{"org.matrix.msc1767.text": "Lunch?\n1. Pizza\n2. Sushi"}

	for name, raw := range map[string]map[string]any{"stable": stable, "unstable": unstable, "text fallback only": fallback} {
		evt := &event.Event{ID: "$p", RoomID: "!r:hs", Sender: "@u:hs", Timestamp: 1, Type: event.Type{Type: "m.poll.start", Class: event.MessageEventType}, Content: event.Content{Raw: raw}}
		msg := convert(t, evt)
		if msg.Kind != domain.KindPoll || msg.Body != "Lunch?\n1. Pizza\n2. Sushi" {
			t.Errorf("%s: unexpected poll: %+v", name, msg)
		}
	}
	empty := &event.Event{Type: event.Type{Type: "m.poll.start", Class: event.MessageEventType}, Content: event.Content{Raw: map[string]any{}}}
	if _, ok := toMessage(empty, "U"); ok {
		t.Error("a poll without any text is not recorded")
	}
}

func TestRepliesAndThreads(t *testing.T) {
	reply := convert(t, messageEvent(&event.MessageEventContent{
		MsgType: event.MsgText, Format: event.FormatHTML,
		Body:          "> <@a:hs> original text\n\nmy answer",
		FormattedBody: "<mx-reply><blockquote>original text</blockquote></mx-reply>my <b>answer</b>",
		RelatesTo:     &event.RelatesTo{InReplyTo: &event.InReplyTo{EventID: "$orig"}},
	}))
	if reply.Body != "my answer" || reply.Formatted != "my <b>answer</b>" || reply.ReplyTo != "$orig" {
		t.Fatalf("the quoted fallback must go and the reply be kept: %+v", reply)
	}

	thread := convert(t, messageEvent(&event.MessageEventContent{
		MsgType: event.MsgText, Body: "in the thread",
		RelatesTo: &event.RelatesTo{Type: event.RelThread, EventID: "$root", InReplyTo: &event.InReplyTo{EventID: "$last"}, IsFallingBack: true},
	}))
	if thread.ThreadRoot != "$root" || thread.ReplyTo != "" {
		t.Fatalf("a thread message is not a reply: %+v", thread)
	}
	answer := convert(t, messageEvent(&event.MessageEventContent{
		MsgType: event.MsgText, Body: "answers one",
		RelatesTo: &event.RelatesTo{Type: event.RelThread, EventID: "$root", InReplyTo: &event.InReplyTo{EventID: "$one"}},
	}))
	if answer.ThreadRoot != "$root" || answer.ReplyTo != "$one" {
		t.Fatalf("a real reply inside a thread keeps both: %+v", answer)
	}
}

func TestFormattedTextIsSanitized(t *testing.T) {
	msg := convert(t, messageEvent(&event.MessageEventContent{
		MsgType: event.MsgText, Body: "x", Format: event.FormatHTML,
		FormattedBody: `<b>bold</b> <a href="https://example.org/a">link</a> <a href="javascript:alert(1)">bad</a>` +
			`<script>alert(1)</script><img src="mxc://hs/x" onerror="x()"><p onclick="x()">para</p><code class="language-go">x</code>`,
	}))
	for _, bad := range []string{"script", "javascript:", "onerror", "onclick", "<img", "alert"} {
		if strings.Contains(msg.Formatted, bad) {
			t.Errorf("%q survived: %s", bad, msg.Formatted)
		}
	}
	for _, good := range []string{"<b>bold</b>", `href="https://example.org/a"`, "<p>para</p>", `class="language-go"`} {
		if !strings.Contains(msg.Formatted, good) {
			t.Errorf("%q was lost: %s", good, msg.Formatted)
		}
	}
}

func TestOtherAndClientTraffic(t *testing.T) {
	other := convert(t, messageEvent(&event.MessageEventContent{MsgType: "com.example.custom", Body: "something new"}))
	if other.Kind != domain.KindOther || other.Body != "something new" {
		t.Fatalf("an unknown type keeps its text: %+v", other)
	}
	if _, ok := toMessage(messageEvent(&event.MessageEventContent{MsgType: msgTypeVerification, Body: "wants to verify"}), "U"); ok {
		t.Fatal("a verification request is client traffic")
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

	msg := convert(t, evt)
	if msg.Kind != "redacted" || msg.Deletion == nil || msg.Deletion.By != "@mod:hs" || msg.Deletion.Reason != "spam" {
		t.Fatalf("unexpected tombstone: %+v", msg)
	}
	if !msg.Deletion.At.After(msg.SentAt) {
		t.Fatalf("deletion time %v must follow send time %v", msg.Deletion.At, msg.SentAt)
	}
}

func TestOnlyMessagesAreRecorded(t *testing.T) {
	for _, evt := range []*event.Event{
		{Type: event.StateMember, StateKey: new(string), Content: event.Content{Parsed: &event.MemberEventContent{Membership: event.MembershipJoin}}},
		{Type: event.StateCreate, Content: event.Content{Parsed: &event.CreateEventContent{}}},
		{Type: event.StatePowerLevels, Content: event.Content{Parsed: &event.PowerLevelsEventContent{}}},
		{Type: event.EventReaction, Content: event.Content{Raw: map[string]any{}}},
		{Type: event.Type{Type: "m.poll.response", Class: event.MessageEventType}, Content: event.Content{Raw: map[string]any{"org.matrix.msc1767.text": "vote"}}},
	} {
		if msg, ok := toMessage(evt, "U"); ok {
			t.Errorf("%s must not be recorded, got %+v", evt.Type.Type, msg)
		}
	}
}
