// Package random creates cryptographically strong opaque identifiers.
package random

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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

// HexGenerator creates opaque identifiers whose alphabet is the frozen
// artifact identifier grammar (^[A-Za-z0-9][A-Za-z0-9_.:-]{0,199}$). Workspace,
// WorkItem and HumanTask identities travel into the artifact and knowledge
// contracts as scope, subject and upstream references, so an identity that the
// artifact grammar refuses would break an otherwise legal delivery.
type HexGenerator struct{}

// New returns a 128-bit identifier in lower-case hexadecimal form.
func (HexGenerator) New() (string, error) {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", fmt.Errorf("read secure random bytes: %w", err)
	}
	return hex.EncodeToString(buffer[:]), nil
}
