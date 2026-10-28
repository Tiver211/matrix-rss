package gormstore

import (
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
)

// driver describes one supported database. Each driver lives in its own file guarded by a build tag
// and registers itself in init. Tags are additive:
//
//	go build ./...                       // no tags: every driver
//	go build -tags sqlite ./...          // SQLite only
//	go build -tags postgres ./...        // PostgreSQL only
//	go build -tags "sqlite postgres" ... // both, same as no tags
type driver struct {
	// dialector builds the GORM dialector for the given DSN.
	dialector func(dsn string) (gorm.Dialector, error)
	// sqlDialect is the dialect name understood by the Matrix crypto store, which shares the connection.
	sqlDialect string
}

var drivers = map[string]driver{}

func register(name string, d driver) {
	drivers[name] = d
}

func lookup(name string) (driver, error) {
	if d, ok := drivers[name]; ok {
		return d, nil
	}
	return driver{}, fmt.Errorf("database driver %q is not available in this build (compiled in: %s)", name, available())
}

func available() string {
	names := make([]string, 0, len(drivers))
	for name := range drivers {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
