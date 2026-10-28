//go:build e2e

package e2e

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"maunium.net/go/mautrix/id"
)

func scratch() string { return os.Getenv("E2E_DIR") }

// runSQL changes the bot's database directly (SQLite CLI is not needed: use the modernc driver).
func runSQL(t *testing.T, statement string) {
	t.Helper()
	db := openBotDB(t)
	defer db.Close()
	if _, err := db.Exec(statement); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

// storedBodies returns the bodies stored for the room, read straight from the database.
func storedBodies(t *testing.T, room id.RoomID) string {
	t.Helper()
	db := openBotDB(t)
	defer db.Close()
	rows, err := db.Query("SELECT body FROM messages WHERE room_id = ? ORDER BY sent_at DESC", string(room))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var bodies []string
	for rows.Next() {
		var body string
		_ = rows.Scan(&body)
		bodies = append(bodies, body)
	}
	return strings.Join(bodies, "|")
}

func cli(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(filepath.Join(scratch(), "matrixrss"), args...)
	cmd.Env = append(os.Environ(),
		"DATABASE_DRIVER=sqlite",
		"DATABASE_DSN="+filepath.Join(scratch(), "bot", "matrixrss.db"),
		"PUBLIC_BASE_URL=http://127.0.0.1:8080",
	)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func openBotDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(scratch(), "bot", "matrixrss.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	return db
}
