//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/id"
)

type apiMessage struct {
	EventID string `json:"event_id"`
	Sender  string `json:"sender"`
	Kind    string `json:"kind"`
	Body    string `json:"body"`
}

func listBodies(t *testing.T, room id.RoomID, query string) []string {
	t.Helper()
	var page struct {
		Messages   []apiMessage `json:"messages"`
		NextBefore string       `json:"next_before"`
	}
	if err := json.Unmarshal([]byte(messagesOf(t, room, query)), &page); err != nil {
		t.Fatal(err)
	}
	bodies := make([]string, len(page.Messages))
	for i, m := range page.Messages {
		bodies[i] = m.Body
	}
	return bodies
}

func scenarioBackfill(t *testing.T) {
	var room id.RoomID

	t.Run("history sent before the room was published is fetched from the homeserver", func(t *testing.T) {
		room = alice.createRoom("Backfill room", false, botID)
		alice.waitJoined(room, botID)
		for _, text := range []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7"} {
			alice.say(room, text)
		}
		alice.ask(room, "!rss on", "/feeds/")
		time.Sleep(500 * time.Millisecond)
		alice.say(room, "m8")
		eventually(t, "m8 recorded live", 15*time.Second, func() bool {
			return strings.Contains(messagesOfNoFetch(t, room), "m8")
		})

		// Only m8 is recorded; the API has to fetch the rest.
		got := listBodies(t, room, "?limit=50")
		t.Logf("API bodies, newest first: %q", got)
		want := []string{"m8", "m7", "m6", "m5", "m4", "m3", "m2", "m1"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("got %q, want %q (no commands, no bot replies)", got, want)
		}
	})

	t.Run("paging walks the whole history without gaps or repeats", func(t *testing.T) {
		var seen []string
		cursor := ""
		for page := 0; page < 10; page++ {
			query := "?limit=3"
			if cursor != "" {
				query += "&before=" + cursor
			}
			var body struct {
				Messages   []apiMessage `json:"messages"`
				NextBefore string       `json:"next_before"`
			}
			if err := json.Unmarshal([]byte(messagesOf(t, room, query)), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Messages) == 0 {
				break
			}
			for _, m := range body.Messages {
				seen = append(seen, m.Body)
			}
			cursor = body.NextBefore
		}
		if strings.Join(seen, ",") != "m8,m7,m6,m5,m4,m3,m2,m1" {
			t.Fatalf("paging gave %q", seen)
		}
	})

	t.Run("the bot's own replies and the secret never end up in messages", func(t *testing.T) {
		body := messagesOf(t, room, "?limit=200&deleted=include")
		if strings.Contains(body, "/feeds/") || strings.Contains(body, "!rss") || strings.Contains(body, "rssbot") {
			t.Fatalf("bot traffic leaked into the stored messages: %s", body)
		}
	})

	t.Run("a purged range is fetched again", func(t *testing.T) {
		// Simulate the retention job having removed everything but the newest message.
		runSQL(t, "DELETE FROM messages WHERE body <> 'm8'")
		got := listBodies(t, room, "?limit=50")
		if len(got) != 8 {
			t.Fatalf("expected the history to come back, got %q", got)
		}
	})
}

// messagesOfNoFetch lists what is stored without letting the API fetch from the homeserver: the feed does that.
func messagesOfNoFetch(t *testing.T, room id.RoomID) string {
	t.Helper()
	return storedBodies(t, room)
}
