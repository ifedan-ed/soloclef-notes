package eventstream

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// newEventID is uuid.uuid4().hex: sixteen random bytes as thirty-two lowercase
// hex digits, with the version and variant bits a version-4 UUID carries, and
// no dashes.
func newEventID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("stamping an event id: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return hex.EncodeToString(raw), nil
}
