//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/id"
)

func scenarioEncrypted(t *testing.T) {
	var room id.RoomID
	var feed string

	t.Run("commands and messages work in an encrypted room", func(t *testing.T) {
		room = alice.createRoom("Encrypted room", true, botID)
		alice.waitJoined(room, botID)

		// Sent before the room is published: the bot receives the keys but records nothing.
		alice.say(room, "x1 before publishing")
		alice.say(room, "x2 before publishing")
		time.Sleep(1 * time.Second)

		reply := alice.ask(room, "!rss on", "/feeds/") // encrypted command, encrypted reply
		feed = feedURL(t, reply)

		alice.say(room, "e1 secret news")
		eventually(t, "the decrypted message in the feed", 20*time.Second, func() bool {
			_, body := fetch(t, feed)
			return strings.Contains(body, "e1 secret news")
		})
	})

	t.Run("edits and deletions of encrypted messages", func(t *testing.T) {
		e2 := alice.say(room, "e2 to be edited")
		eventually(t, "e2 recorded", 20*time.Second, func() bool {
			return strings.Contains(storedBodies(t, room), "e2 to be edited")
		})
		alice.edit(room, e2, "e2 edited")
		eventually(t, "the edit applied", 20*time.Second, func() bool {
			return strings.Contains(storedBodies(t, room), "e2 edited")
		})

		e3 := alice.say(room, "e3 to be deleted")
		eventually(t, "e3 recorded", 20*time.Second, func() bool {
			return strings.Contains(storedBodies(t, room), "e3 to be deleted")
		})
		alice.redact(room, e3, "mistake")
		eventually(t, "e3 gone from the feed", 20*time.Second, func() bool {
			_, body := fetch(t, feed)
			return !strings.Contains(body, "e3 to be deleted")
		})
		if deleted := messagesOf(t, room, "?deleted=only"); !strings.Contains(deleted, `"reason":"mistake"`) || strings.Contains(deleted, "e3 to be deleted") {
			t.Fatalf("deletion record: %s", deleted)
		}
	})

	t.Run("history sent while the bot was in the room is decrypted on demand", func(t *testing.T) {
		got := listBodies(t, room, "?limit=50")
		t.Logf("API bodies: %q", got)
		joined := strings.Join(got, "|")
		for _, want := range []string{"x1 before publishing", "x2 before publishing", "e1 secret news", "e2 edited"} {
			if !strings.Contains(joined, want) {
				t.Errorf("missing %q in %q", want, joined)
			}
		}
		if strings.Contains(joined, "/feeds/") || strings.Contains(joined, "!rss") {
			t.Errorf("bot traffic leaked: %q", joined)
		}
	})

	t.Run("messages from before the bot joined cannot be read and are skipped quietly", func(t *testing.T) {
		late := alice.createRoom("Late bot", true)
		alice.say(late, "sent before the bot existed here")
		alice.invite(late, botID)
		alice.waitJoined(late, botID)
		alice.ask(late, "!rss on", "/feeds/")
		time.Sleep(500 * time.Millisecond)
		alice.say(late, "sent after")
		eventually(t, "the live message recorded", 20*time.Second, func() bool {
			return strings.Contains(storedBodies(t, late), "sent after")
		})

		got := listBodies(t, late, "?limit=50")
		joined := strings.Join(got, "|")
		t.Logf("API bodies: %q", got)
		if !strings.Contains(joined, "sent after") || strings.Contains(joined, "sent before the bot") {
			t.Fatalf("unexpected: %q", joined)
		}
	})
}
