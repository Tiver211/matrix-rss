// Package domain holds the business entities of the bot. It depends on nothing else.
package domain

import "time"

// Feed is a Matrix room published as an RSS feed. Readers reach it with a FeedToken.
type Feed struct {
	RoomID    string
	Title     string
	Enabled   bool
	CreatedAt time.Time
}
