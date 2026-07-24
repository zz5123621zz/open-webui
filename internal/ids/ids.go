package ids

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// New returns a cryptographically random, URL-safe identifier containing
// 128 bits of entropy.
func New() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// NewSecret returns a cryptographically random, URL-safe secret containing
// 256 bits of entropy.
func NewSecret() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
