// Package mailsignin turns a mailbox as an account's mail connection
// stores it into the settings to sign in to it with.
//
// A mailbox is a record in the `accounts` list of an account's one
// EMAIL_IMAP_CONNECTOR connection. There are two kinds. One added with an
// app password holds the password sealed with the deployment's secret key
// (soloclef_connectors/storedtoken). One connected through Microsoft holds
// a sealed access token that lasts an hour and a sealed refresh token, and
// signing in to it may first trade the refresh token for a new pair and
// store that.
//
// It is a package of this module because every tier that reads a mailbox
// needs it and `internal` cannot cross a module: the API tests a mailbox
// with it (soloclef_api/internal/mailaccounts, internal/microsoftmail,
// which also make the records), and the chat's mail tools read one with it
// in the API and in the worker's automation runs (mailtools). A second
// copy would be a second idea of when a token is refreshed and where the
// new one is written, and Microsoft may rotate a refresh token on use: two
// tiers that disagreed would lock each other's mailbox out.
package mailsignin

import (
	"encoding/json"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// Account is what of a stored mailbox may be shown: never a password, a
// token or their ciphertext.
type Account struct {
	ID       string
	Label    string
	Host     string
	Username string
	// Microsoft says the mailbox was connected through Microsoft's sign-in
	// and not with a password.
	Microsoft bool
	// NeedsReconnect says Microsoft has refused the mailbox's grant; it
	// cannot be read until its owner connects it again.
	NeedsReconnect bool
}

// Describe is the showable part of a stored record. ok is false for an
// entry that is not a mailbox record at all.
func Describe(stored []byte) (Account, bool) {
	var held Record
	if json.Unmarshal(stored, &held) != nil || held.Text("id") == "" {
		return Account{}, false
	}
	return Account{
		ID: held.Text("id"), Label: held.Text("label"), Host: held.Text("host"), Username: held.Text("username"),
		Microsoft: held.IsMicrosoft(), NeedsReconnect: held.Truth(ReconnectKey),
	}, true
}

// PasswordUnreadable is what a person is told about a password mailbox
// whose stored password is missing or was sealed with another key.
const PasswordUnreadable = "This account's credentials are missing or can no longer be read; add it again"

// password is a password mailbox's stored password in the clear.
func password(held Record, secretKey string) (string, bool) {
	sealed := held.Text("app_password")
	if sealed == "" || !held.Truth(EncryptedKey) {
		return "", false
	}
	opened, err := storedtoken.Open(secretKey, sealed)
	return opened, err == nil && strings.TrimSpace(opened) != ""
}
