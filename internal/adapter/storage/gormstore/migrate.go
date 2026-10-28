package gormstore

import (
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

const legacyTokenName = "legacy"

// migrate creates the schema and converts data of older versions.
func migrate(db *gorm.DB) error {
	err := db.AutoMigrate(&feedModel{}, &messageModel{}, &revisionModel{}, &apiKeyModel{}, &feedTokenModel{})
	if err != nil {
		return err
	}
	return migrateLegacyFeedTokens(db)
}

// migrateLegacyFeedTokens moves the plain token column of feeds, from the time a feed had a single
// token, into hashed feed tokens, so existing feed URLs keep working, and drops the column.
func migrateLegacyFeedTokens(db *gorm.DB) error {
	if !db.Migrator().HasColumn("feeds", "token") {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var legacy []struct {
			RoomID string
			Token  string
		}
		if err := tx.Table("feeds").Select("room_id, token").Scan(&legacy).Error; err != nil {
			return err
		}
		for _, feed := range legacy {
			if feed.Token == "" {
				continue
			}
			token := feedTokenModel{
				RoomID: feed.RoomID, Name: legacyTokenName, Hash: domain.HashSecret(feed.Token),
				Hint: feed.Token[:min(len(feed.Token), 8)], CreatedAt: time.Now().UTC(), CreatedBy: "migration",
			}
			if err := tx.Create(&token).Error; err != nil {
				return fmt.Errorf("migrate token of %s: %w", feed.RoomID, err)
			}
		}
		if err := tx.Exec("DROP INDEX IF EXISTS idx_feeds_token").Error; err != nil {
			return err
		}
		return tx.Exec("ALTER TABLE feeds DROP COLUMN token").Error
	})
}
