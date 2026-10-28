//go:build e2e

package e2e

import "testing"

func TestE2E(t *testing.T) {
	alice = newUser(t, "alice")
	bob = newUser(t, "bob")
	carol = newUser(t, "carol")

	t.Run("plain", scenarioPlain)
	t.Run("backfill", scenarioBackfill)
	t.Run("roomadmin", scenarioRoomAdmin)
	t.Run("cli", scenarioCLI)
	t.Run("e2ee", scenarioEncrypted)
}
