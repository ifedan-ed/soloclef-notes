// Package jmapaccounts keeps the mailboxes an account connected on a JMAP
// server (Stalwart): their rows, their sealed credentials, signing in to
// one with a credential that works (refreshing an OAuth token when it is
// about to lapse), and the requests Stalwart's OAuth server is sent.
//
// Two tiers use it. The API connects, lists, tests and removes a mailbox
// (soloclef_api/internal/jmapconnect) and sends a confirmed draft
// (internal/mailconfirm); the chat's mail tools read one in the API and in
// the worker (mailtools). One package, so there is one idea of when a token
// is refreshed: Stalwart replaces the refresh token on every use and
// deletes the whole grant when a replaced one is presented, so two tiers
// that refreshed differently would lock each other's mailbox out.
//
// # Credentials
//
// A mailbox's credentials (the app password, or the refresh and access
// tokens) are one JSON value sealed with AES-256-GCM under a key derived
// from the deployment's secret for this purpose alone and bound to the
// owner (ownmodels.NewPurposeSeal): a ciphertext copied onto another
// account's row does not open. Nothing in this package returns them to a
// caller that shows things; Account, which every list is made of, has no
// field for them.
//
// # Refresh
//
// A token is refreshed when it has less than refreshMargin left, under the
// row's lock (store.go): the second of two refreshes that met waits, finds
// the first's fresh token and uses it. A refresh the server refuses marks
// the mailbox to be reconnected; nothing reads it until its owner does.
package jmapaccounts

import (
	"errors"
	"time"
)

// The two ways a mailbox signs in.
const (
	AuthOAuth       = "oauth"
	AuthAppPassword = "app_password"
)

// refreshMargin is how long before it lapses an access token is replaced:
// longer than one conversation with the server, so a token never expires
// in the middle of one.
const refreshMargin = 2 * time.Minute

// Account is what may be shown of a mailbox: never a credential or its
// ciphertext.
type Account struct {
	ID             string
	Label          string
	ServerURL      string
	Username       string
	Auth           string
	CanSend        bool
	NeedsReconnect bool
	CreatedAt      time.Time
}

// stored is a mailbox's whole row, credentials sealed.
type stored struct {
	Account
	OAuthClientID      string
	TokenEndpoint      string
	RevocationEndpoint string
	Sealed             string
}

// ErrNoAccount is a mailbox that is not the owner's, or is not there. The
// two are one answer, so an id says nothing about other accounts.
var ErrNoAccount = errors.New("jmapaccounts: no such mailbox")
