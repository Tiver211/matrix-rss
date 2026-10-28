//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/id"
)

var (
	keyRe = regexp.MustCompile(`mxk_[A-Za-z0-9_-]+`)
	idRe  = regexp.MustCompile(`#(\d+)`)
)

func idOf(t *testing.T, text, name string) string {
	t.Helper()
	re := regexp.MustCompile(`#(\d+)\s+` + regexp.QuoteMeta(name) + `\b`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no entry %q in:\n%s", name, text)
	}
	return m[1]
}

func scenarioRoomAdmin(t *testing.T) {
	team := alice.createRoom("Team", false, botID, bob.id, carol.id)
	other := alice.createRoom("Management", false, botID, bob.id)
	bob.join(team)
	carol.join(team)
	bob.join(other)
	alice.waitJoined(team, botID)
	alice.waitJoined(other, botID)
	alice.makeAdmin(team, bob.id, 100) // bob administers Team only
	defaultURL := feedURL(t, alice.ask(team, "!rss on", "/feeds/"))
	alice.ask(other, "!rss on", "/feeds/")
	teamPath := "/api/v1/rooms/" + roomPath(team) + "/messages"
	otherPath := "/api/v1/rooms/" + roomPath(other) + "/messages"

	var dm id.RoomID

	t.Run("a room admin cannot publish rooms", func(t *testing.T) {
		if reply := bob.ask(team, "!rss on", "Only the bot's admins"); reply == "" {
			t.Fatal("no reply")
		}
	})

	t.Run("strangers are refused", func(t *testing.T) {
		for _, command := range []string{"!rss rotate", "!rss token list", "!rss token create x"} {
			if reply := carol.replyTo(team, command); !strings.Contains(reply, "not allowed") {
				t.Errorf("%s -> %q", command, reply)
			}
		}
		if reply := carol.replyTo(team, "!rss key list"); !strings.Contains(reply, "No API keys") {
			t.Errorf("key list -> %q", reply)
		}
		// Everyone gets help.
		if reply := carol.replyTo(team, "!rss"); !strings.Contains(reply, "Commands") {
			t.Errorf("help -> %q", reply)
		}
	})

	t.Run("a room admin lists the tokens of their room", func(t *testing.T) {
		reply := bob.ask(team, "!rss token list", "default")
		if strings.Contains(reply, defaultURL[strings.LastIndex(defaultURL, "/")+1:]) {
			t.Fatalf("the listing must not contain secrets: %s", reply)
		}
		if reply := bob.replyTo(team, "!rss token list "+string(other)); !strings.Contains(reply, "not allowed") {
			t.Fatalf("another room: %q", reply)
		}
	})

	t.Run("secrets are refused in a shared room and the instruction names the room", func(t *testing.T) {
		reply := bob.ask(team, "!rss token create reader1", "private chat")
		if !strings.Contains(reply, "!rss token create "+string(team)+" reader1") {
			t.Fatalf("instruction: %s", reply)
		}
		reply = bob.ask(team, "!rss key create grafana", "private chat")
		if !strings.Contains(reply, "rooms="+string(team)) || keyRe.MatchString(reply) {
			t.Fatalf("instruction: %s", reply)
		}
	})

	t.Run("a room admin can open a private chat with the bot, a stranger cannot", func(t *testing.T) {
		dm = bob.directChat(botID)
		bob.waitJoined(dm, botID)

		carolDM := carol.directChat(botID)
		time.Sleep(3 * time.Second)
		if _, joined := carol.members(carolDM)[botID]; joined {
			t.Fatal("the bot joined a private chat started by a stranger")
		}
	})

	var readerURL string
	t.Run("feed tokens in the private chat", func(t *testing.T) {
		reply := bob.ask(dm, fmt.Sprintf("!rss token create %s reader1", team), "/feeds/")
		readerURL = feedURL(t, reply)
		if status, _ := fetch(t, readerURL); status != 200 {
			t.Fatalf("the new token must work, got %d", status)
		}
		if readerURL == defaultURL {
			t.Fatal("tokens must differ")
		}
		if reply := bob.ask(dm, fmt.Sprintf("!rss token create %s sneaky", other), "not allowed"); reply == "" {
			t.Fatal("no reply")
		}
		if reply := bob.replyTo(dm, "!rss token list"); !strings.Contains(reply, "which room") {
			t.Fatalf("a private chat has no room of its own: %q", reply)
		}
	})

	var keySecret, keyID string
	t.Run("API keys of a room admin are limited to their room", func(t *testing.T) {
		if reply := bob.replyTo(dm, "!rss key create wide"); !strings.Contains(reply, "limit the key") {
			t.Fatalf("an unrestricted key must be refused: %q", reply)
		}
		if reply := bob.replyTo(dm, fmt.Sprintf("!rss key create wide rooms=%s,%s", team, other)); !strings.Contains(reply, "not allowed") {
			t.Fatalf("a key reaching another room must be refused: %q", reply)
		}

		reply := bob.ask(dm, fmt.Sprintf("!rss key create k1 rooms=%s expires=7d", team), "mxk_")
		keySecret = keyRe.FindString(reply)
		if status, _ := httpGet(t, teamPath, keySecret); status != 200 {
			t.Fatalf("the key must read its room, got %d", status)
		}
		if status, _ := httpGet(t, otherPath, keySecret); status != 403 {
			t.Fatalf("another room must be 403, got %d", status)
		}
		if status, body := httpGet(t, "/api/v1/rooms", keySecret); status != 200 || strings.Contains(body, string(other)) || !strings.Contains(body, string(team)) {
			t.Fatalf("room list for the key: %d %s", status, body)
		}
		if status, _ := httpGet(t, "/api/v1/admin/keys", keySecret); status != 403 {
			t.Fatalf("keys cannot use the admin API, got %d", status)
		}

		list := bob.ask(dm, "!rss key list", "k1")
		keyID = idOf(t, list, "k1")
		if strings.Contains(list, keySecret) {
			t.Fatalf("the listing must not contain the secret: %s", list)
		}
	})

	t.Run("room admins see only their own keys", func(t *testing.T) {
		// alice (bot admin) makes an unrestricted key and one for the other room.
		aliceDM := alice.directChat(botID)
		alice.waitJoined(aliceDM, botID)
		alice.ask(aliceDM, "!rss key create global-key", "mxk_")
		alice.ask(aliceDM, fmt.Sprintf("!rss key create other-key rooms=%s", other), "mxk_")

		list := bob.ask(dm, "!rss key list", "k1")
		if strings.Contains(list, "global-key") || strings.Contains(list, "other-key") {
			t.Fatalf("a room admin saw foreign keys:\n%s", list)
		}
		all := alice.ask(aliceDM, "!rss key list", "global-key")
		if !strings.Contains(all, "k1") || !strings.Contains(all, "other-key") {
			t.Fatalf("a bot admin sees everything:\n%s", all)
		}
		globalID := idOf(t, all, "global-key")
		if reply := bob.replyTo(dm, "!rss key revoke "+globalID); !strings.Contains(reply, "no key or token") {
			t.Fatalf("a foreign key must look unknown: %q", reply)
		}
	})

	t.Run("revoking works at once", func(t *testing.T) {
		list := bob.ask(dm, "!rss token list "+string(team), "reader1")
		bob.ask(dm, "!rss token revoke "+idOf(t, list, "reader1"), "revoked")
		if status, _ := fetch(t, readerURL); status != 404 {
			t.Fatalf("a revoked token must give 404, got %d", status)
		}
		if status, _ := fetch(t, defaultURL); status != 200 {
			t.Fatalf("the other token must keep working, got %d", status)
		}

		bob.ask(dm, "!rss key revoke "+keyID, "revoked")
		if status, _ := httpGet(t, teamPath, keySecret); status != 401 {
			t.Fatalf("a revoked key must give 401, got %d", status)
		}
	})

	t.Run("rotate replaces every URL of the room", func(t *testing.T) {
		newURL := feedURL(t, bob.ask(team, "!rss rotate", "New URL"))
		if status, _ := fetch(t, defaultURL); status != 404 {
			t.Fatalf("the old URL must stop working, got %d", status)
		}
		if status, _ := fetch(t, newURL); status != 200 {
			t.Fatalf("the new URL must work, got %d", status)
		}
	})

	t.Run("a user who left the room loses the rights at once", func(t *testing.T) {
		if _, err := bob.client.LeaveRoom(context.Background(), team); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1 * time.Second)
		if reply := bob.replyTo(dm, fmt.Sprintf("!rss token create %s late", team)); !strings.Contains(reply, "not allowed") {
			t.Fatalf("expected a refusal, got %q", reply)
		}
	})
}
