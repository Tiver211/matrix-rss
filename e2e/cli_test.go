//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func scenarioCLI(t *testing.T) {
	t.Run("the CLI works on the live database while the bot runs", func(t *testing.T) {
		out, _, err := cli(t, "rooms")
		if err != nil || !strings.Contains(out, "Team") || !strings.Contains(out, "Management") {
			t.Fatalf("rooms: %q, %v", out, err)
		}
		out, _, err = cli(t, "key", "list")
		if err != nil || !strings.Contains(out, "global-key") || !strings.Contains(out, "REVOKED") {
			t.Fatalf("key list: %q, %v", out, err)
		}

		secret, note, err := cli(t, "key", "create", "-rooms", "does-not-exist", "x")
		if err == nil {
			t.Fatalf("an unknown room must be refused, got %q / %q", secret, note)
		}
	})

	t.Run("a key issued by the CLI is accepted by the running bot", func(t *testing.T) {
		out, _, err := cli(t, "rooms")
		if err != nil {
			t.Fatal(err)
		}
		var room string
		for _, field := range strings.Fields(out) {
			if strings.HasPrefix(field, "!") {
				room = field
				break
			}
		}
		secret, _, err := cli(t, "key", "create", "-rooms", room, "-expires", "1d", "cli-key")
		secret = strings.TrimSpace(secret)
		if err != nil || !strings.HasPrefix(secret, "mxk_") {
			t.Fatalf("create: %q, %v", secret, err)
		}
		if status, _ := httpGet(t, "/api/v1/rooms", secret); status != 200 {
			t.Fatalf("the running bot must accept the CLI's key, got %d", status)
		}
		list, _, _ := cli(t, "key", "list")
		var id string
		for _, line := range strings.Split(list, "\n") {
			if strings.Contains(line, "cli-key") {
				id = strings.Fields(line)[0]
			}
		}
		if _, _, err := cli(t, "key", "revoke", id); err != nil {
			t.Fatal(err)
		}
		if status, _ := httpGet(t, "/api/v1/rooms", secret); status != 401 {
			t.Fatalf("a key revoked by the CLI must stop working at once, got %d", status)
		}
	})
}
