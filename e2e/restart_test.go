//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/id"
)

func roomByTitle(t *testing.T, title string) id.RoomID {
	t.Helper()
	var body struct {
		Rooms []struct {
			RoomID string `json:"room_id"`
			Title  string `json:"title"`
		} `json:"rooms"`
	}
	status, raw := httpGet(t, "/api/v1/rooms", rootToken)
	if status != 200 || json.Unmarshal([]byte(raw), &body) != nil {
		t.Fatalf("rooms: %d %s", status, raw)
	}
	for _, r := range body.Rooms {
		if r.Title == title {
			return id.RoomID(r.RoomID)
		}
	}
	t.Fatalf("no room %q in %s", title, raw)
	return ""
}

// TestDowntime runs while the bot is stopped: people keep talking.
func TestDowntime(t *testing.T) {
	if os.Getenv("E2E_PHASE") != "down" {
		t.Skip()
	}
	// The bot is down, so the room list comes from the database via the CLI.
	out, _, err := cli(t, "rooms")
	if err != nil {
		t.Fatal(err)
	}
	find := func(title string) id.RoomID {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, title) {
				return id.RoomID(strings.Fields(line)[0])
			}
		}
		t.Fatalf("no %q in %s", title, out)
		return ""
	}
	alice = newUser(t, "alice")
	alice.say(find("Plain room"), "plain: sent while the bot was down")
	alice.say(find("Encrypted room"), "encrypted: sent while the bot was down")
}

// TestAfterRestart runs once the bot is back on the same database.
func TestAfterRestart(t *testing.T) {
	if os.Getenv("E2E_PHASE") != "up" {
		t.Skip()
	}
	alice = newUser(t, "alice")
	plain, encrypted := roomByTitle(t, "Plain room"), roomByTitle(t, "Encrypted room")

	eventually(t, "messages sent during the downtime to be delivered", 30*time.Second, func() bool {
		return strings.Contains(storedBodies(t, plain), "plain: sent while the bot was down") &&
			strings.Contains(storedBodies(t, encrypted), "encrypted: sent while the bot was down")
	})

	// The bot keeps working in the encrypted room with its old device and keys.
	alice.say(encrypted, "encrypted: after the restart")
	reply := alice.replyTo(encrypted, "!rss token list")
	t.Logf("reply after restart: %s", strings.ReplaceAll(reply, "\n", " | "))
	eventually(t, "a live message after the restart", 20*time.Second, func() bool {
		return strings.Contains(storedBodies(t, encrypted), "encrypted: after the restart")
	})
}
