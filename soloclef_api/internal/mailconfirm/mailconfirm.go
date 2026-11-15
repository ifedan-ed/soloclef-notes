// Package mailconfirm serves the card a mail draft is shown on: the draft
// as stored with a go-ahead to send it, the send, and the discard.
//
//	POST /api/v1/mail/drafts/{draft_id}/confirmation  the stored draft, and a go-ahead while it can be sent
//	POST /api/v1/mail/drafts/{draft_id}/send          send it, presenting the go-ahead and the hash shown
//	POST /api/v1/mail/drafts/{draft_id}/discard       drop it
//	GET  /api/v1/mail/drafts                          the drafts still waiting, for the Drafts waiting page
//
// The list exists because a draft can outlive the chat that made it: an
// automation's run deletes its conversation when it ends, so the card in
// it is gone, and the draft would wait out its day with nowhere to be
// sent from. The list names each draft and nothing more; the page draws
// the same card for each, which asks for its own go-ahead (Confirm), so
// the single-use confirmation and every check of the send stay where they
// are.
//
// These routes are the only way a message the assistant drafted is sent,
// and they are for the person in a session only: an API key, an agent's
// token or the chat itself (whose tools have no send) cannot reach them.
// The rules are soloclef_assistant/maildrafts' (Guard); this package
// answers them in HTTP, each refusal with the code the card tells it by.
//
// The confirmation is a POST, not a GET, because it issues something: a
// single-use go-ahead bound to the content in the same answer. A GET would
// be cached, prefetched or replayed by things that are not the person.
package mailconfirm

// The paths.
const (
	ConfirmationPath = "/api/v1/mail/drafts/{draft_id}/confirmation"
	SendPath         = "/api/v1/mail/drafts/{draft_id}/send"
	DiscardPath      = "/api/v1/mail/drafts/{draft_id}/discard"
	WaitingPath      = "/api/v1/mail/drafts"
)

// ServedRoutes is every route of this package as "METHOD path".
func ServedRoutes() []string {
	return []string{"POST " + ConfirmationPath, "POST " + SendPath, "POST " + DiscardPath, "GET " + WaitingPath}
}

// BodyRoutes is the one route that reads a small JSON body.
func BodyRoutes() []string { return []string{"POST " + SendPath} }
