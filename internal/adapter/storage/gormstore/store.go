// Package gormstore implements the repositories on top of GORM.
// One connection pool serves both the application tables and the Matrix crypto store.
package gormstore

import (
	"database/sql"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Store struct {
	db         *gorm.DB
	sqlDB      *sql.DB
	sqlDialect string
}

// Open connects with the named driver ("sqlite" or "postgres") and migrates the schema.
func Open(driverName, dsn string) (*Store, error) {
	drv, err := lookup(driverName)
	if err != nil {
		return nil, err
	}
	dialector, err := drv.dialector(dsn)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return &Store{db: db, sqlDB: sqlDB, sqlDialect: drv.sqlDialect}, nil
}

func (s *Store) Feeds() *FeedRepository       { return &FeedRepository{db: s.db} }
func (s *Store) Messages() *MessageRepository { return &MessageRepository{db: s.db} }
func (s *Store) APIKeys() *APIKeyRepository   { return &APIKeyRepository{db: s.db} }
func (s *Store) FeedTokens() *FeedTokenRepository {
	return &FeedTokenRepository{db: s.db}
}

// SQLDB exposes the shared connection pool for components that bring their own schema.
func (s *Store) SQLDB() *sql.DB { return s.sqlDB }

// SQLDialect names the SQL flavour of SQLDB ("sqlite3" or "postgres").
func (s *Store) SQLDialect() string { return s.sqlDialect }

func (s *Store) Close() error { return s.sqlDB.Close() }
