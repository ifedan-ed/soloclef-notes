// Package publiclinks serves the caller's public share links: the list the
// sharing panel shows, and the revoke that takes one link down.
//
//	GET    /api/v1/snapshots                 every link the caller has made
//	DELETE /api/v1/snapshots/{snapshot_id}   take one down
//
// A public link is a frozen copy of a chat, or of one studio output, behind
// a token (`public_chat_snapshots`). Whoever holds the token may read the
// copy; that side is internal/publicchat. This package is the owner's side:
// which links exist, and removing one.
//
// # Whose link
//
// A link belongs to the account that made it, and both statements name that
// owner (`WHERE user_id = $1`). There is nothing else to check: no
// membership, no permission, no rule about whose chat a link points at. The
// list is "your links", and another account's link is the same `404 Public
// link not found` as one that was never made.
//
// # What this does better than the routes it replaces
//
//   - The list is one statement. It used to be four: the links, then the
//     threads, the outputs and the accounts they name, joined in memory.
//   - A link's frozen copy is not read to list it. `snapshot_data` holds a
//     whole conversation and the list wants one key of it, so the statement
//     asks Postgres for that key; the column never crosses the wire.
//   - A copy whose title is not text is listed under its thread's title, or
//     as "Untitled". It used to fail the whole list with a 500.
//   - The revoke is one guarded DELETE. It used to read the row and two
//     more rows of an access gate, and then delete.
//   - A 500 keeps its CORS headers, so a browser can read the envelope.
package publiclinks

import "time"

// The paths.
const (
	ListPath   = "/api/v1/snapshots"
	RevokePath = "/api/v1/snapshots/{snapshot_id}"
)

// What a caller is told.
const (
	linkNotFound = "Public link not found"
	revoked      = "Public link revoked"
	// untitled is what a link is listed as when neither its copy nor its
	// thread has a title.
	untitled = "Untitled"
)

// Link is one of the caller's links, as the list shows it.
type Link struct {
	ID         int64
	ShareToken string
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	// OwnerID is the account the link belongs to, which is always the
	// caller: the statement that reads it filters on the owner.
	OwnerID string
	// ThreadID is set for a shared chat and NotebookOutputID for a shared
	// studio output; a link is one or the other.
	ThreadID         *int64
	NotebookOutputID *int64
	// NotebookID is the notebook a shared output belongs to, nil for a chat.
	NotebookID *int64
	// StoredTitle is the title frozen into the copy, empty when it has
	// none. ThreadTitle is the title of the chat it was taken from as it is
	// now: nil for an output's link, and for a chat that has been deleted.
	StoredTitle string
	ThreadTitle *string
	// Messages is how many messages the copy holds.
	Messages int64
}

// Title is what the link is listed as: the title frozen into the copy, or
// failing that the chat's own, or failing that "Untitled". An empty title is
// no title.
func (l Link) Title() string {
	if l.StoredTitle != "" {
		return l.StoredTitle
	}
	if l.ThreadTitle != nil && *l.ThreadTitle != "" {
		return *l.ThreadTitle
	}
	return untitled
}

// Expired reports whether a link has stopped working: one with no expiry
// never does, and one whose moment has arrived has. The comparison is `<=`,
// so the exact instant counts as expired, as it does where the public page
// is served (internal/publicchat).
func Expired(expiresAt *time.Time, now time.Time) bool {
	return expiresAt != nil && !expiresAt.After(now)
}
