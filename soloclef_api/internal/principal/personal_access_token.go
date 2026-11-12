package principal

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// TokenPrefix is app.utils.pat.PAT_PREFIX. A credential starting with it is an
// API key and is never tried as a session token.
const TokenPrefix = "ss_pat_"

// LastUsedThrottle is app.utils.pat.LAST_USED_THROTTLE: last_used_at is only
// written once every ten minutes per token, so a polled route does not turn
// every read into a write.
const LastUsedThrottle = 10 * time.Minute

// HashToken is app.utils.pat.hash_pat: the stored hash is the hex sha256 of the
// token text, so the token itself is never in the database or in a log.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func isPersonalAccessToken(credential string) bool {
	return strings.HasPrefix(credential, TokenPrefix)
}

// PersonalAccessTokenOwner is a live API key and the account it acts as.
type PersonalAccessTokenOwner struct {
	TokenID    int64
	UserID     string
	LastUsedAt time.Time
	// APIAccessEnabled is the account's switch, read with the key so the
	// check costs no query of its own.
	APIAccessEnabled bool
}

// NeedsLastUsedTouch mirrors maybe_touch_last_used.
func (o PersonalAccessTokenOwner) NeedsLastUsedTouch(now time.Time) bool {
	return o.LastUsedAt.IsZero() || now.Sub(o.LastUsedAt) >= LastUsedThrottle
}
