// Package accesstokens serves a person's own API keys: the list of them, and
// the write that revokes one.
//
// The revoke is DELETE /api/v1/pats/{pat_id}
// (app/routes/personal_access_tokens_routes.py:delete_personal_access_token).
// It is the second write handed over and it was chosen for the one thing the
// first does not have: a PostHog event of the route's own. The Python route
// deletes the row, commits, and then captures pat_revoked through capture_for —
// so this is where the gated-request middleware stops being the only thing this
// tier tells PostHog, and where an event has to fire after a commit and not
// before (internal/routeanalytics, internal/database.Effects).
//
// Minting a key is deliberately **not** here. create_personal_access_token
// generates the credential, and a second implementation of credential minting is
// a second place to get it wrong — the same reason fastapi-users' own routes
// stay where they are (custom/docs/go-api-tier.md, "What cannot move").
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
