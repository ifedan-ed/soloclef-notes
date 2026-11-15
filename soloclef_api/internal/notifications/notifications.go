// Package notifications serves an account's inbox:
//
//	GET    /api/v1/notifications                         one page of a view, newest first
//	DELETE /api/v1/notifications                         clear a view
//	PATCH  /api/v1/notifications/read-all                mark everything read
//	PATCH  /api/v1/notifications/{notification_id}/read  mark one read
//	DELETE /api/v1/notifications/{notification_id}       delete one
//	GET    /api/v1/notifications/unread-count            unread, in all or in one tab or type
//	GET    /api/v1/notifications/unread-counts-batch     unread in the Status tab
//	GET    /api/v1/notifications/source-types            what the Status tab can filter by
//
// A view is the caller's rows narrowed by the filters a tab sends (view.go).
// The inbox is a person's: only an interactive session reads or changes it,
// and an API key or an agent is told so.
//
// # What it does better than the Python
//
//   - **A page and its total are one statement**, where there were two.
//   - **A filter that is not understood is refused by the list too.** The
//     Python list ignored an unknown `source_type` or `filter` and showed the
//     whole inbox under a heading that said otherwise; only its clear refused.
//   - **A search for "100%" finds "100%".** The pattern characters of ILIKE
//     are escaped; they used to be passed through as wildcards.
//   - **Marking one read is one guarded statement** that locks the row, and
//     the list has a tie-break (id) so two rows of one instant keep their
//     order from page to page.
//   - **The unread counts for a tab are one pass**, and the source types one
//     statement in a stated order.
//
// The one tab this version of the product has is status, which also holds
// Studio outputs, automation runs and mail drafts. Comment mentions and
// credit warnings went with workspaces and billing, and the administrator's
// announcements were removed (2026-11-13); their names are refused rather
// than answered with zero.
package notifications

import (
	"net/http"
	"time"
)

// The paths. The literals beside `{notification_id}` are safe on a ServeMux:
// `read-all` shares PATCH with `…/{notification_id}/read`, which is one
// segment longer, and the three literal GETs have no `GET …/{notification_id}`
// to be mistaken for.
const (
	ListPath        = "/api/v1/notifications"
	ReadAllPath     = "/api/v1/notifications/read-all"
	OnePath         = "/api/v1/notifications/{notification_id}"
	ReadPath        = "/api/v1/notifications/{notification_id}/read"
	UnreadCountPath = "/api/v1/notifications/unread-count"
	UnreadBatchPath = "/api/v1/notifications/unread-counts-batch"
	SourceTypesPath = "/api/v1/notifications/source-types"
)

// Served is every route as a "METHOD /path" pair.
func Served() []string {
	return []string{
		http.MethodGet + " " + ListPath,
		http.MethodDelete + " " + ListPath,
		http.MethodPatch + " " + ReadAllPath,
		http.MethodPatch + " " + ReadPath,
		http.MethodDelete + " " + OnePath,
		http.MethodGet + " " + UnreadCountPath,
		http.MethodGet + " " + UnreadBatchPath,
		http.MethodGet + " " + SourceTypesPath,
	}
}

const (
	absent          = "Notification not found"
	stillRunning    = "Still running. It can be deleted once it has finished."
	sessionRequired = "The inbox is read from a signed-in session, not with an API key."

	defaultPageSize = 50
	maxPageSize     = 100
	maxSearchChars  = 200
	// recentWindow is the span the web app syncs live; an unread count says
	// how many of its rows fall inside it, so the client can hold the older
	// part still while the recent part moves.
	recentWindow = 14 * 24 * time.Hour
)

// statusTypes are the Status tab's types: a connection's and a document's
// progress, what soloclef_notify/notify writes when something a person
// started ends (a Studio output, an automation run, a mail draft waiting),
// and its "Soloclef 1.2.0 is out" for an iPhone on an older app.
var statusTypes = []string{"connector_indexing", "connector_deletion", "document_processing", "studio_output", "automation_run", "mail_draft", "app_update"}

// statusCategory is the one tab, named by the clients' `category` filter.
// It lists the status types; a row of a type no tab lists (one an older
// version wrote) is only in the whole inbox. Any other name is refused, so
// a client that asks for a removed tab is told rather than shown the
// Status tab under its heading.
const statusCategory = "status"

// retiredAnnouncementsCategory is the Inbox tab the App Store builds 1.0.0
// and 1.1.0 still ask for; it is always empty (view.go, routes.go).
// installed-builds: notifications-announcements-tab
const retiredAnnouncementsCategory = "announcements"
