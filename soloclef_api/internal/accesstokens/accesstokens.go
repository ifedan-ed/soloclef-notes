// Package accesstokens serves a person's own API keys: making one, the list
// of them, and the revoke.
//
// A key is shown whole once, in the answer that makes it, and only its hash
// (principal.HashToken, what the gate looks a presented key up by) and its
// first sixteen characters are stored. Every row is its owner's: each query
// is keyed by the caller's user_id, so one person's key id names nothing of
// another's. All three routes take a browser session and refuse a key, so a
// key can neither list its siblings nor mint successors; using a key is the
// gate's business, and the gate also requires the account's API-access
// switch (internal/principal).
package accesstokens

import "time"

// Token is a personal_access_tokens row as PATRead renders it. The secret is
// not among its columns: only the first sixteen characters are stored in the
// clear, and that is what the list shows.
type Token struct {
	ID         int64
	Label      string
	Prefix     string
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	CreatedAt  time.Time
}
