//go:build sqlite || !postgres

package gormstore

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

const sqlitePragmas = "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"

func init() {
	register("sqlite", driver{
		dialector:  sqliteDialector,
		sqlDialect: "sqlite3",
	})
}

// sqliteDialector treats the DSN as a file path and creates its directory when missing.
func sqliteDialector(path string) (gorm.Dialector, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	return sqlite.Open("file:" + path + sqlitePragmas), nil
}
