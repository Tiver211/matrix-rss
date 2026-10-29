//go:build e2e

package e2e

import (
	"os"
	"testing"
)

func TestE2E(t *testing.T) {
	alice = newUser(t, "alice")
	bob = newUser(t, "bob")
	carol = newUser(t, "carol")

	only := os.Getenv("E2E_ONLY")
	run := func(name string, f func(*testing.T)) {
		if only == "" || only == name {
			t.Run(name, f)
		}
	}
	run("plain", scenarioPlain)
	run("backfill", scenarioBackfill)
	run("media", scenarioMedia)
	run("roomadmin", scenarioRoomAdmin)
	run("cli", scenarioCLI)
	run("e2ee", scenarioEncrypted)
}
