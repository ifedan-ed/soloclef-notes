package maildrafts

import (
	"crypto/subtle"
	"slices"
	"time"
)

// Draft is a stored draft.
type Draft struct {
	ID        string
	Provider  string
	AccountID string
	Message   Message
	// StoredHash is the hash written with the message; the hash a send
	// checks is computed again from Message, never read from here.
	StoredHash        string
	Status            string
	SupersededBy      string
	Failure           string
	ProviderMessageID string
	CreatedAt         time.Time
	ExpiresAt         time.Time
	SentAt            *time.Time
}

// shownStatus is the status a draft is shown with at now.
func (d Draft) shownStatus(now time.Time) string {
	if (d.Status == StatusPending || d.Status == StatusFailed) && !now.Before(d.ExpiresAt) {
		return StatusExpired
	}
	return d.Status
}

// sendable reports whether a draft may be confirmed and sent at now: it is
// waiting (or its last send failed) and has not run out.
func (d Draft) sendable(now time.Time) bool {
	status := d.shownStatus(now)
	return status == StatusPending || status == StatusFailed
}

// confirmation is a stored go-ahead.
type confirmation struct {
	DraftID     string
	OwnerID     string
	ContentHash string
	ExpiresAt   time.Time
	Used        bool
	// Unusual is the codes of the unusual points the go-ahead was issued
	// with: what the person must acknowledge to send with it.
	Unusual []string
}

// admission is everything a send is judged on, read inside the send's
// transaction with the owner and the go-ahead locked.
type admission struct {
	OwnerID       string
	DraftID       string
	PresentedHash string
	// Acknowledged is the unusual points the person ticked on the card.
	Acknowledged []string
	// Confirmation is the go-ahead the presented token names; nil when the
	// token names none.
	Confirmation *confirmation
	Draft        Draft
	SentLastHour int
	Now          time.Time
}

// admit is the guard's decision, with nothing else in it: the rules a send
// must pass, in the order they are told. It is a function of what was read
// so the tests can put every case to it, and the store calls it between
// reading and writing.
func admit(a admission) error {
	c := a.Confirmation
	// The go-ahead first: without a valid one nothing else is looked at.
	if c == nil || c.OwnerID != a.OwnerID || c.DraftID != a.DraftID {
		return ErrConfirmationInvalid
	}
	if c.Used {
		return ErrConfirmationSpent
	}
	if !a.Now.Before(c.ExpiresAt) {
		return ErrConfirmationExpired
	}
	if !a.Draft.sendable(a.Now) {
		return ErrDraftNotPending
	}
	// The content as it stands now, hashed now, against what was shown and
	// what the browser says it showed.
	current := a.Draft.Message.ContentHash()
	if !equalHash(current, c.ContentHash) || !equalHash(current, a.PresentedHash) {
		return ErrDraftChanged
	}
	// What is unusual now, of what can be known here: the content (bound
	// to the go-ahead, so the same as when it was issued) and the past
	// hour (exact here, under the owner's lock). A point the go-ahead was
	// not issued with arose after the card was drawn, and the person has
	// not seen it: they are shown it before anything is sent.
	for _, code := range codes(append(fromContent(a.Draft.Message), fromPace(a.SentLastHour)...)) {
		if !slices.Contains(c.Unusual, code) {
			return ErrUnusualChanged
		}
	}
	// Every point the go-ahead was issued with, those worked out again
	// above and the recipients' history that only Confirm asks the
	// mailbox about, must have been ticked.
	for _, code := range c.Unusual {
		if !slices.Contains(a.Acknowledged, code) {
			return ErrUnusualNotConfirmed
		}
	}
	// A stored draft was valid when it was made; checked again so that a
	// row changed by any other path is never sent as something it is not.
	if err := a.Draft.Message.validate(); err != nil {
		return ErrDraftChanged
	}
	return nil
}

func equalHash(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
