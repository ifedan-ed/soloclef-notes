package posthogcapture

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// newEventID is the version 4 id each event carries, which PostHog uses to drop
// a batch it has already taken.
func newEventID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		// Unreachable on this platform; an id that repeats would only cost the
		// event, so it is not worth failing a request over.
		return ""
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	text := hex.EncodeToString(bytes[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", text[0:8], text[8:12], text[12:16], text[16:20], text[20:32])
}
