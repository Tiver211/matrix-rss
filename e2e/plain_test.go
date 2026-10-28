//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/id"
)

var (
	alice, bob, carol *user
	plainRoom         id.RoomID
	plainFeed         string
)

func scenarioPlain(t *testing.T) {
	t.Run("01 invitations: only bot admins can invite the bot", func(t *testing.T) {
		carolRoom := carol.createRoom("Carol's room", false, botID)
		plainRoom = alice.createRoom("Plain room", false, botID)
		alice.waitJoined(plainRoom, botID)

		time.Sleep(3 * time.Second)
		if _, joined := carol.members(carolRoom)[botID]; joined {
			t.Fatal("the bot joined a room it was invited to by a non-admin")
		}
	})

	t.Run("02 !rss on publishes the room and prints the feed URL once", func(t *testing.T) {
		reply := alice.ask(plainRoom, "!rss on", "/feeds/")
		plainFeed = feedURL(t, reply)
		t.Logf("bot: %s", strings.ReplaceAll(reply, plainFeed, "<feed url>"))

		status, body := fetch(t, plainFeed)
		if status != 200 || !strings.Contains(body, "<rss") {
			t.Fatalf("feed: %d %s", status, body)
		}
		if strings.Contains(body, "!rss") || strings.Contains(body, "/feeds/") {
			t.Fatalf("commands and the bot's replies must not be in the feed:\n%s", body)
		}
	})

	t.Run("03 messages show up in the feed and the API", func(t *testing.T) {
		alice.say(plainRoom, "hello world")
		alice.say(plainRoom, "second message")
		eventually(t, "both messages in the feed", 15*time.Second, func() bool {
			_, body := fetch(t, plainFeed)
			return strings.Contains(body, "hello world") && strings.Contains(body, "second message")
		})

		body := messagesOf(t, plainRoom, "")
		t.Logf("API: %s", body)
		if strings.Index(body, "second message") > strings.Index(body, "hello world") {
			t.Fatalf("newest must come first: %s", body)
		}
		if !strings.Contains(body, `"sender":"@alice:localhost"`) {
			t.Fatalf("sender missing: %s", body)
		}
	})

	t.Run("04 edits are applied and kept as revisions", func(t *testing.T) {
		first := firstEventID(t, plainRoom, "hello world")
		alice.edit(plainRoom, id.EventID(first), "hello EDITED")
		eventually(t, "edit in the feed", 15*time.Second, func() bool {
			_, body := fetch(t, plainFeed)
			return strings.Contains(body, "hello EDITED") && !strings.Contains(body, "hello world")
		})
		status, body := httpGet(t, "/api/v1/rooms/"+roomPath(plainRoom)+"/messages/"+first+"/history", rootToken)
		t.Logf("revisions: %d %s", status, body)
		if status != 200 || !strings.Contains(body, "hello world") || !strings.Contains(body, "hello EDITED") {
			t.Fatalf("both versions expected: %s", body)
		}
		if !strings.Contains(messagesOf(t, plainRoom, ""), `"edited_at"`) {
			t.Fatal("edited_at missing")
		}
	})

	t.Run("05 an edit by someone else is ignored", func(t *testing.T) {
		alice.invite(plainRoom, bob.id)
		bob.join(plainRoom)
		bob.say(plainRoom, "bob wrote this")
		eventually(t, "bob's message recorded", 15*time.Second, func() bool {
			return strings.Contains(messagesOf(t, plainRoom, ""), "bob wrote this")
		})
		target := firstEventID(t, plainRoom, "bob wrote this")
		alice.edit(plainRoom, id.EventID(target), "alice rewrote bob")
		time.Sleep(3 * time.Second)
		if body := messagesOf(t, plainRoom, ""); strings.Contains(body, "alice rewrote bob") {
			t.Fatalf("a foreign edit was applied: %s", body)
		}
	})

	t.Run("06 deletions leave a record without the text", func(t *testing.T) {
		second := firstEventID(t, plainRoom, "second message")
		alice.redact(plainRoom, id.EventID(second), "oops")
		eventually(t, "message gone from the feed", 15*time.Second, func() bool {
			_, body := fetch(t, plainFeed)
			return !strings.Contains(body, "second message")
		})
		deleted := messagesOf(t, plainRoom, "?deleted=only")
		t.Logf("deleted=only: %s", deleted)
		if !strings.Contains(deleted, `"by":"@alice:localhost"`) || !strings.Contains(deleted, `"reason":"oops"`) || strings.Contains(deleted, "second message") {
			t.Fatalf("deletion record wrong: %s", deleted)
		}
		if strings.Contains(messagesOf(t, plainRoom, ""), "second message") {
			t.Fatal("deleted message listed by default")
		}
	})
}

// firstEventID finds the event ID of the message with the given text through the API.
func firstEventID(t *testing.T, room id.RoomID, text string) string {
	t.Helper()
	body := messagesOf(t, room, "?deleted=include")
	idx := strings.Index(body, text)
	if idx < 0 {
		t.Fatalf("%q not found in %s", text, body)
	}
	// {"seq":1,"event_id":"$...","room_id" ... "body":"text"} : take the closest event_id before the text.
	before := body[:idx]
	start := strings.LastIndex(before, `"event_id":"`) + len(`"event_id":"`)
	end := start + strings.Index(before[start:], `"`)
	return before[start:end]
}
