// Package digest computes content digests: SHA-256 over the RFC 8785
// canonical JSON of a value (ADR 0008).
package digest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/gowebpki/jcs"
)

// Prefix starts every digest string.
const Prefix = "sha256:"

// Canonical returns the RFC 8785 canonical JSON of v: object keys sorted,
// no insignificant whitespace, numbers and strings in their one canonical
// form. Integers beyond 2^53 lose precision, as in any I-JSON encoder.
func Canonical(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode digest input: %w", err)
	}
	c, err := jcs.Transform(b)
	if err != nil {
		return nil, fmt.Errorf("canonicalize digest input: %w", err)
	}
	return c, nil
}

// Of returns the digest of v's canonical JSON, as sha256:<hex>.
func Of(v any) (string, error) {
	c, err := Canonical(v)
	if err != nil {
		return "", err
	}
	return Bytes(c), nil
}

// Bytes returns the digest of raw bytes, as sha256:<hex>.
func Bytes(b []byte) string {
	sum := sha256.Sum256(b)
	return Prefix + hex.EncodeToString(sum[:])
}
