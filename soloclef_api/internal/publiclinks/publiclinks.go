// Package publiclinks serves the caller's public links: the list Settings,
// Public links shows, and the revoke that takes one link down.
//
//	GET    /api/v1/snapshots                 every link the caller has made
//	DELETE /api/v1/snapshots/{snapshot_id}   take one down
//
// A public link shares one Studio output (`output_public_links`); nothing
// else can be shared (internal/publicshare, which also serves what a
// stranger holding the token reads). This package is the owner's side:
// which links exist, and removing one. The paths keep the word "snapshots"
// from when a link could also be a frozen chat, because every installed web
// and phone build calls them by it.
//
// # Whose link
//
// A link belongs to the account that made it, and both statements name that
// owner (`WHERE user_id = $1`). There is nothing else to check. The list is
// "your links", and another account's link is the same `404 Public link not
// found` as one that was never made.
//
// # How it is built
//
//   - The list is one statement, and it does not read a link's frozen copy:
//     it asks Postgres for the one key it shows, so the copy never crosses
//     the wire.
//   - A copy whose title is not text is listed under its output's title, or
//     as "Untitled", rather than failing the whole list.
//   - The revoke is one guarded DELETE.
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
	// output has a title.
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
	// NotebookID is the notebook the shared output belongs to, for the
	// owner's own list to open it from; a stranger never sees it.
	NotebookID int64
	// StoredTitle is the title frozen into the copy, empty when it has
	// none. OutputTitle is the output's title as it is now.
	StoredTitle string
	OutputTitle string
}

// Title is what the link is listed as: the title frozen into the copy,
// which is what its visitors see, or failing that the output's own, or
// failing that "Untitled". An empty title is no title.
func (l Link) Title() string {
	if l.StoredTitle != "" {
		return l.StoredTitle
	}
	if l.OutputTitle != "" {
		return l.OutputTitle
	}
	return untitled
}

// Expired reports whether a link has stopped working: one with no expiry
// never does, and one whose moment has arrived has. The comparison is `<=`,
// so the exact instant counts as expired, as it does where the public page
// is served (internal/publicshare).
func Expired(expiresAt *time.Time, now time.Time) bool {
	return expiresAt != nil && !expiresAt.After(now)
}
