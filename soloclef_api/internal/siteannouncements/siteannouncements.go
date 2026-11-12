// Package siteannouncements serves the administrator's announcements page:
//
//	GET  /api/v1/admin/announcements                 the Sent list, newest first
//	POST /api/v1/admin/announcements                 send one to every active account
//	POST /api/v1/admin/announcements/{id}/withdraw   take one out of every inbox
//
// An announcement is delivered as one inbox notification per account
// (internal/notifications owns those rows afterwards: reading, deleting and
// hiding an expired one).
//
// # Who may
//
// A site administrator, in a session: the account's own is_superuser flag,
// read on every request, as everywhere else in this tier. An API key or an
// agent's token is refused even when its account is an administrator;
// writing to every inbox on the site is not something a key left in a
// script should be able to do.
//
// # What it does better than the Python
//
//   - **Sending is one statement.** The Python added one ORM row per
//     recipient and flushed them; here the announcement and every copy are
//     one INSERT … SELECT, so ten thousand accounts are one round trip and
//     the recipient count cannot differ from the copies made.
//   - **Withdrawing keeps the read count.** The Python deleted the copies
//     and with them every "read", so a withdrawn announcement read by
//     hundreds showed "read by 0". The read copies removed are counted
//     onto the announcement in the same statement.
//   - **The Sent list counts reads in one pass**, and its order has a
//     tie-break.
//   - **No audiences.** Workspaces are gone, so an announcement goes to
//     every active account and the route takes no audience.
//
// Not done here: the phone's "New announcement" push. This tier has no
// push sender yet, so a phone sees the announcement when it next opens the
// inbox.
package siteannouncements

import "net/http"

// The paths.
const (
	ListPath     = "/api/v1/admin/announcements"
	WithdrawPath = "/api/v1/admin/announcements/{announcement_id}/withdraw"
)

// Served is every route as a "METHOD /path" pair.
func Served() []string {
	return []string{
		http.MethodGet + " " + ListPath,
		http.MethodPost + " " + ListPath,
		http.MethodPost + " " + WithdrawPath,
	}
}

const (
	administratorsOnly = "Site administrators only."
	announcementAbsent = "Announcement not found."

	// The Sent list is the newest of them; an older one is history.
	sentListSize = 50
	// A draft is a title and a few paragraphs; the cap is generous for
	// that and small enough to read whole.
	maxBodyBytes = 64 << 10
)
