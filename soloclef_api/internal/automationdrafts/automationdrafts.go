// Package automationdrafts serves the chat's automation drafts to the card
// that shows them: read one, and discard one. The draft itself is written by
// the chat's create_automation tool (soloclef_assistant/automationdrafts),
// and turned into an automation by the ordinary create route
// (internal/automations, POST /api/v1/automations with the draft's id),
// which marks it created in the same transaction. So this package never
// saves an automation, and the create route stays the one place that does.
//
// The card asks for the draft when it is drawn, as its owner, rather than
// carrying it in the chat message: a message is saved and synced to every
// open page of the thread, and the draft's state (created, discarded,
// replaced) changes after the message was written.
package automationdrafts

import "net/http"

// The paths, as the web and phone apps call them.
const (
	DraftPath   = "/api/v1/automation-drafts/{draft_id}"
	DiscardPath = "/api/v1/automation-drafts/{draft_id}/discard"
)

// Served lists every route of this package, for cmd/api's route table.
func Served() []string {
	return []string{http.MethodGet + " " + DraftPath, http.MethodPost + " " + DiscardPath}
}

// What a person is told.
const (
	draftAbsent     = "Draft not found"
	draftNotWaiting = "This draft was already created, discarded or replaced by a newer one."
)
