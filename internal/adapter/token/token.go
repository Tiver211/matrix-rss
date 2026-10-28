// Package token generates random URL-safe secrets.
package token

import (
	"crypto/rand"
	"encoding/base64"
)

const sizeBytes = 32

// Generator implements usecase.TokenGenerator with crypto/rand.
type Generator struct{}

func (Generator) New() (string, error) {
	raw := make([]byte, sizeBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
