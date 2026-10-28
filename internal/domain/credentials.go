package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// APIKey grants read access to the HTTP API. Only a hash of the secret is stored, so the secret
// can be shown just once, when the key is created.
type APIKey struct {
	ID   uint64
	Name string
	// Hint is the beginning of the secret; it helps to recognise a key in listings.
	Hint string
	// Rooms limits the key to these rooms; empty means all rooms.
	Rooms     []string
	ExpiresAt *time.Time
	CreatedAt time.Time
	CreatedBy string
	RevokedAt *time.Time
}

// Active reports whether the key may be used at the given time.
func (k APIKey) Active(now time.Time) bool {
	return k.RevokedAt == nil && (k.ExpiresAt == nil || now.Before(*k.ExpiresAt))
}

// AllowsRoom reports whether the key may read the room.
func (k APIKey) AllowsRoom(roomID string) bool {
	if len(k.Rooms) == 0 {
		return true
	}
	for _, room := range k.Rooms {
		if room == roomID {
			return true
		}
	}
	return false
}

// FeedToken is the secret in the URL of a feed. A feed can have several, e.g. one per reader,
// and each is revoked on its own. Like API keys they are stored as hashes.
type FeedToken struct {
	ID        uint64
	RoomID    string
	Name      string
	Hint      string
	CreatedAt time.Time
	CreatedBy string
	RevokedAt *time.Time
}

// Active reports whether the token may be used.
func (t FeedToken) Active() bool {
	return t.RevokedAt == nil
}

// KeyRequest describes an API key an admin wants to create.
type KeyRequest struct {
	Name      string
	Rooms     []string
	ExpiresAt *time.Time
	CreatedBy string
}

// HashSecret returns the hex SHA-256 of a secret. Secrets are random 256-bit values, so a fast
// hash is enough and allows looking a credential up by its hash.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// ParseLifetime reads a duration such as "36h" or "30d" (days are accepted in addition to Go's units).
func ParseLifetime(value string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(value, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%w: invalid lifetime %q", ErrInvalid, value)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%w: invalid lifetime %q", ErrInvalid, value)
	}
	return d, nil
}
