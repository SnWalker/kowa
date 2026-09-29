// Package random creates cryptographically strong opaque identifiers.
package random

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

const tokenBytes = 32

// Generator creates URL-safe values with 256 bits of entropy.
type Generator struct{}

func (Generator) New() (string, error) {
	buffer := make([]byte, tokenBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("read secure random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
