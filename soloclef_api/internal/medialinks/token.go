package medialinks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// A link's token is the claim and its signature:
//
//	base64url(claim JSON) "." base64url(HMAC-SHA256(secret, domain || claim JSON))
//
// The domain separates these signatures from every other use of the same
// secret (it also signs sessions), so a token of one kind can never be
// presented as another.

// linkLifetime is how long a link plays. Long enough to read a report in
// one sitting and come back after lunch; short enough that a link copied
// out of the page is soon worth nothing.
const linkLifetime = 12 * time.Hour

const signingDomain = "soloclef-artifact-media-link:v2:"

// claim is who may play which file, until when.
type claim struct {
	UserID     string `json:"u"`
	ArtifactID int64  `json:"a"`
	FileID     int64  `json:"f"`
	// Expires is seconds since the epoch.
	Expires int64 `json:"e"`
}

var encoding = base64.RawURLEncoding

func sign(payload []byte, secret string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingDomain))
	mac.Write(payload)
	return mac.Sum(nil)
}

// issue is the token for a claim.
func issue(c claim, secret string) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return encoding.EncodeToString(payload) + "." + encoding.EncodeToString(sign(payload, secret)), nil
}

// read is the claim a token carries. false for anything forged, malformed
// or expired; the caller answers all three alike, so a link tells a
// stranger nothing it does not grant.
func read(token, secret string, now time.Time) (claim, bool) {
	encoded, signature, found := strings.Cut(token, ".")
	if !found {
		return claim{}, false
	}
	payload, err := encoding.DecodeString(encoded)
	if err != nil {
		return claim{}, false
	}
	given, err := encoding.DecodeString(signature)
	// The signature is checked before the payload is parsed: nothing a
	// stranger wrote is interpreted.
	if err != nil || !hmac.Equal(given, sign(payload, secret)) {
		return claim{}, false
	}
	var c claim
	if json.Unmarshal(payload, &c) != nil || c.UserID == "" || c.ArtifactID <= 0 || c.FileID <= 0 {
		return claim{}, false
	}
	if c.Expires < now.Unix() {
		return claim{}, false
	}
	return c, true
}
