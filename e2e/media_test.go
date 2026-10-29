//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// The bot of these tests is started with MEDIA_MAX_SIZE=1048576.
const mediaLimit = 1 << 20

type apiAttachment struct {
	Name       string `json:"name"`
	MIMEType   string `json:"mime_type"`
	Size       int64  `json:"size"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	DurationMS int64  `json:"duration_ms"`
	Voice      bool   `json:"voice"`
	URL        string `json:"url"`
}

type apiFull struct {
	EventID       string         `json:"event_id"`
	Kind          string         `json:"kind"`
	Body          string         `json:"body"`
	FormattedBody string         `json:"formatted_body"`
	Attachment    *apiAttachment `json:"attachment"`
	Location      *struct {
		GeoURI string `json:"geo_uri"`
		MapURL string `json:"map_url"`
	} `json:"location"`
	ReplyTo    string `json:"reply_to"`
	ThreadRoot string `json:"thread_root"`
}

func (u *user) upload(data []byte, mime string, encrypted bool) (id.ContentURIString, *event.EncryptedFileInfo) {
	u.t.Helper()
	if encrypted {
		file := attachment.NewEncryptedFile()
		resp, err := u.client.UploadBytes(context.Background(), file.Encrypt(data), "application/octet-stream")
		if err != nil {
			u.t.Fatal(err)
		}
		return "", &event.EncryptedFileInfo{EncryptedFile: *file, URL: resp.ContentURI.CUString()}
	}
	resp, err := u.client.UploadBytes(context.Background(), data, mime)
	if err != nil {
		u.t.Fatal(err)
	}
	return resp.ContentURI.CUString(), nil
}

func (u *user) sendEvent(room id.RoomID, t event.Type, content any) id.EventID {
	u.t.Helper()
	resp, err := u.client.SendMessageEvent(context.Background(), room, t, content)
	if err != nil {
		u.t.Fatalf("send %s: %v", t.Type, err)
	}
	return resp.EventID
}

func (u *user) sendFile(room id.RoomID, encrypted bool, msgType event.MessageType, body, name, mime string, data []byte, tweak func(*event.MessageEventContent)) id.EventID {
	u.t.Helper()
	url, file := u.upload(data, mime, encrypted)
	content := &event.MessageEventContent{
		MsgType: msgType, Body: body, FileName: name, URL: url, File: file,
		Info: &event.FileInfo{MimeType: mime, Size: len(data)},
	}
	if tweak != nil {
		tweak(content)
	}
	return u.sendEvent(room, event.EventMessage, content)
}

// fileData is the content of a test file; every file differs so that mix-ups are noticed.
func fileData(label string, size int) []byte {
	return bytes.Repeat([]byte(label+"|"), size/len(label+"|")+1)[:size]
}

// sendEverything sends one message of every kind and returns the event IDs by label.
func sendEverything(t *testing.T, room id.RoomID, encrypted bool, phase string) map[string]id.EventID {
	t.Helper()
	ids := map[string]id.EventID{}
	ids["image"] = alice.sendFile(room, encrypted, event.MsgImage, "image-"+phase, "cat-"+phase+".png", "image/png", fileData("image-"+phase, 3000),
		func(c *event.MessageEventContent) { c.Info.Width, c.Info.Height = 640, 480 })
	ids["voice"] = alice.sendFile(room, encrypted, event.MsgAudio, "voice-"+phase, "voice-"+phase+".ogg", "audio/ogg", fileData("voice-"+phase, 2000),
		func(c *event.MessageEventContent) {
			c.MSC3245Voice = &event.MSC3245Voice{}
			c.MSC1767Audio = &event.MSC1767Audio{Duration: 12340}
			c.Info.Duration = 12340
		})
	ids["video"] = alice.sendFile(room, encrypted, event.MsgVideo, "video-"+phase, "clip-"+phase+".mp4", "video/mp4", fileData("video-"+phase, 5000),
		func(c *event.MessageEventContent) { c.Info.Width, c.Info.Height, c.Info.Duration = 1280, 720, 5000 })
	ids["file"] = alice.sendFile(room, encrypted, event.MsgFile, "file-"+phase, "report-"+phase+".pdf", "application/pdf", fileData("file-"+phase, 4000), nil)

	url, file := alice.upload(fileData("sticker-"+phase, 1500), "image/webp", encrypted)
	ids["sticker"] = alice.sendEvent(room, event.EventSticker, &event.MessageEventContent{
		Body: "sticker-" + phase, URL: url, File: file, Info: &event.FileInfo{MimeType: "image/webp", Width: 256, Height: 256, Size: 1500},
	})

	ids["location"] = alice.sendEvent(room, event.EventMessage, &event.MessageEventContent{
		MsgType: event.MsgLocation, Body: "location-" + phase, GeoURI: "geo:52.5200,13.4050",
	})
	ids["poll"] = alice.sendEvent(room, event.Type{Type: "org.matrix.msc3381.poll.start", Class: event.MessageEventType}, map[string]any{
		"org.matrix.msc3381.poll.start": map[string]any{
			"kind":           "org.matrix.msc3381.poll.disclosed",
			"max_selections": 1,
			"question":       map[string]any{"org.matrix.msc1767.text": "poll-" + phase + "?"},
			"answers": []any{
				map[string]any{"id": "pizza", "org.matrix.msc1767.text": "Pizza"},
				map[string]any{"id": "sushi", "org.matrix.msc1767.text": "Sushi"},
			},
		},
		"org.matrix.msc1767.text": "poll-" + phase + "?\n1. Pizza\n2. Sushi",
	})
	ids["formatted"] = alice.sendEvent(room, event.EventMessage, &event.MessageEventContent{
		MsgType: event.MsgText, Body: "formatted-" + phase, Format: event.FormatHTML,
		FormattedBody: "<b>bold</b> formatted-" + phase + `<script>alert(1)</script><a href="javascript:alert(2)">x</a>`,
	})
	ids["reply"] = alice.sendEvent(room, event.EventMessage, &event.MessageEventContent{
		MsgType: event.MsgText, Body: "> <@alice:localhost> image-" + phase + "\n\nreply-" + phase,
		Format:        event.FormatHTML,
		FormattedBody: "<mx-reply><blockquote>image</blockquote></mx-reply>reply-" + phase,
		RelatesTo:     &event.RelatesTo{InReplyTo: &event.InReplyTo{EventID: ids["image"]}},
	})
	ids["thread"] = alice.sendEvent(room, event.EventMessage, &event.MessageEventContent{
		MsgType: event.MsgText, Body: "thread-" + phase,
		RelatesTo: &event.RelatesTo{Type: event.RelThread, EventID: ids["image"], InReplyTo: &event.InReplyTo{EventID: ids["image"]}, IsFallingBack: true},
	})
	ids["custom"] = alice.sendEvent(room, event.EventMessage, &event.MessageEventContent{MsgType: "com.example.custom", Body: "custom-" + phase})
	return ids
}

func apiMessages(t *testing.T, room id.RoomID) map[string]apiFull {
	t.Helper()
	var page struct {
		Messages []apiFull `json:"messages"`
	}
	if err := json.Unmarshal([]byte(messagesOf(t, room, "?limit=200")), &page); err != nil {
		t.Fatal(err)
	}
	byID := map[string]apiFull{}
	for _, m := range page.Messages {
		byID[m.EventID] = m
	}
	return byID
}

func download(t *testing.T, url, bearer string) (int, string, map[string]string) {
	t.Helper()
	status, body := httpGet(t, strings.TrimPrefix(url, apiBase), bearer)
	return status, body, nil
}

func scenarioMedia(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		name := map[bool]string{false: "plain room", true: "encrypted room"}[encrypted]
		t.Run(name, func(t *testing.T) { checkMediaRoom(t, encrypted) })
	}
}

func checkMediaRoom(t *testing.T, encrypted bool) {
	room := alice.createRoom(fmt.Sprintf("Media %v", encrypted), encrypted, botID)
	alice.waitJoined(room, botID)

	// "before" reaches the bot as history; "after" is recorded live.
	before := sendEverything(t, room, encrypted, "before")
	feed := feedURL(t, alice.ask(room, "!rss on", "/feeds/"))
	feedToken := feed[strings.LastIndex(feed, "/")+1:]
	time.Sleep(500 * time.Millisecond)
	after := sendEverything(t, room, encrypted, "after")
	eventually(t, "the last live message to be stored", 30*time.Second, func() bool {
		return strings.Contains(storedBodies(t, room), "custom-after")
	})

	messages := apiMessages(t, room)
	for phase, ids := range map[string]map[string]id.EventID{"before": before, "after": after} {
		phase, ids := phase, ids
		t.Run(phase, func(t *testing.T) {
			get := func(label string) apiFull {
				m, ok := messages[string(ids[label])]
				if !ok {
					t.Fatalf("%s is missing from the API (%d messages)", label, len(messages))
				}
				return m
			}

			for _, c := range []struct {
				label, kind, mime string
				data              []byte
			}{
				{"image", "image", "image/png", fileData("image-"+phase, 3000)},
				{"voice", "audio", "audio/ogg", fileData("voice-"+phase, 2000)},
				{"video", "video", "video/mp4", fileData("video-"+phase, 5000)},
				{"file", "file", "application/pdf", fileData("file-"+phase, 4000)},
				{"sticker", "sticker", "image/webp", fileData("sticker-"+phase, 1500)},
			} {
				m := get(c.label)
				if m.Kind != c.kind || m.Attachment == nil || m.Attachment.MIMEType != c.mime || m.Attachment.URL == "" {
					t.Errorf("%s: %+v / %+v", c.label, m, m.Attachment)
					continue
				}
				status, body, _ := download(t, m.Attachment.URL, rootToken)
				if status != 200 || body != string(c.data) {
					t.Errorf("%s (encrypted=%v): downloaded %d bytes with status %d, want %d identical bytes", c.label, encrypted, len(body), status, len(c.data))
				}
			}

			if a := get("image").Attachment; a.Width != 640 || a.Height != 480 || a.Size != 3000 || !strings.HasPrefix(a.Name, "cat-") {
				t.Errorf("image metadata: %+v", a)
			}
			if a := get("voice").Attachment; !a.Voice || a.DurationMS != 12340 {
				t.Errorf("voice message: %+v", a)
			}
			if a := get("video").Attachment; a.Width != 1280 || a.Height != 720 || a.DurationMS != 5000 {
				t.Errorf("video metadata: %+v", a)
			}
			if a := get("file").Attachment; a.Name != "report-"+phase+".pdf" {
				t.Errorf("file name: %+v", a)
			}

			if l := get("location").Location; l == nil || l.GeoURI != "geo:52.5200,13.4050" || !strings.Contains(l.MapURL, "openstreetmap.org") {
				t.Errorf("location: %+v", l)
			}
			if p := get("poll"); p.Kind != "poll" || p.Body != "poll-"+phase+"?\n1. Pizza\n2. Sushi" {
				t.Errorf("poll: %+v", p)
			}
			f := get("formatted")
			if !strings.Contains(f.FormattedBody, "<b>bold</b>") || strings.Contains(f.FormattedBody, "script") || strings.Contains(f.FormattedBody, "javascript") {
				t.Errorf("formatted text must be kept and sanitized: %q", f.FormattedBody)
			}
			if r := get("reply"); r.ReplyTo != string(ids["image"]) || r.Body != "reply-"+phase || strings.Contains(r.FormattedBody, "blockquote") {
				t.Errorf("reply: %+v", r)
			}
			if th := get("thread"); th.ThreadRoot != string(ids["image"]) || th.ReplyTo != "" {
				t.Errorf("thread: %+v", th)
			}
			if c := get("custom"); c.Kind != "other" || c.Body != "custom-"+phase {
				t.Errorf("unknown message type: %+v", c)
			}
		})
	}

	t.Run("feed readers get the files with their token", func(t *testing.T) {
		_, rss := fetch(t, feed+"?limit=100")
		for _, want := range []string{"[image] image-after", "[voice message] voice-after", "[location] location-after", "[poll] poll-after?", "<enclosure"} {
			if !strings.Contains(rss, want) {
				t.Errorf("%q missing from the feed", want)
			}
		}
		image := messages[string(after["image"])].Attachment
		if !strings.Contains(rss, `type="image/png"`) || !strings.Contains(rss, `length="3000"`) {
			t.Errorf("enclosure details missing")
		}
		status, body := fetch(t, apiBase+"/feeds/"+feedToken+"/media/"+string(after["image"]))
		if status != 200 || body != string(fileData("image-after", 3000)) || image == nil {
			t.Errorf("feed media: status %d, %d bytes", status, len(body))
		}
	})

	t.Run("a file above the limit is described but not offered", func(t *testing.T) {
		big := alice.sendFile(room, encrypted, event.MsgFile, "big", "big.bin", "application/octet-stream", fileData("big", 2*mediaLimit), nil)
		eventually(t, "the big file stored", 30*time.Second, func() bool { return strings.Contains(storedBodies(t, room), "big") })
		m := apiMessages(t, room)[string(big)]
		if m.Attachment == nil || m.Attachment.Size != 2*mediaLimit || m.Attachment.URL != "" {
			t.Fatalf("big file: %+v / %+v", m, m.Attachment)
		}
		status, _ := httpGet(t, "/api/v1/rooms/"+roomPath(room)+"/messages/"+string(big)+"/media", rootToken)
		if status != 413 {
			t.Fatalf("status %d, want 413", status)
		}
	})

	t.Run("a deleted message takes its file with it", func(t *testing.T) {
		target := after["sticker"]
		url := messages[string(target)].Attachment.URL
		alice.redact(room, target, "oops")
		eventually(t, "the file to become unavailable", 20*time.Second, func() bool {
			status, _, _ := download(t, url, rootToken)
			return status == 404
		})
		status, _ := fetch(t, apiBase+"/feeds/"+feedToken+"/media/"+string(target))
		if status != 404 {
			t.Fatalf("readers of the feed must not get it either, got %d", status)
		}
		deleted := messagesOf(t, room, "?deleted=only")
		if !strings.Contains(deleted, string(target)) || strings.Contains(deleted, `"attachment"`) {
			t.Fatalf("the record stays, the file reference goes: %s", deleted)
		}
	})
}
