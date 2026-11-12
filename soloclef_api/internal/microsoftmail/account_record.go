package microsoftmail

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailsignin"
)

// The record of a Microsoft mailbox, its keys and how its tokens are
// sealed and opened are soloclef_assistant/mailsignin's: the chat's mail
// tools sign in to the same records from the worker as well, and one
// package holds what a record is. The names below are that package's,
// under the names this package was written with.
type record = mailsignin.Record

const (
	authKey      = mailsignin.AuthKey
	authValue    = mailsignin.AuthMicrosoft
	subjectKey   = mailsignin.SubjectKey
	encryptedKey = mailsignin.EncryptedKey
	recordTime   = mailsignin.RecordTime
)

var (
	tokenFields         = mailsignin.TokenFields
	storedTokens        = mailsignin.StoredTokens
	errTokensUnreadable = mailsignin.ErrTokensUnreadable
)

// newAccountID is a random UUID (version 4), the form a mailbox's id has
// always had.
func newAccountID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

// sameMailbox reports whether connecting box again should update held:
// the same Microsoft account, or, for a record that names none, the same
// address.
func sameMailbox(held record, box mailbox) bool {
	if !held.IsMicrosoft() {
		return false
	}
	if strings.Trim(box.Subject, ":") != "" && held.Text(subjectKey) == box.Subject {
		return true
	}
	return strings.EqualFold(held.Text("username"), box.Address)
}

// errUnstorable is an address the config column takes and cannot give
// back: U+0000, which no jsonb value may hold.
var errUnstorable = errors.New("microsoftmail: the mailbox's address cannot be stored")

// placed is the account's list with a freshly granted mailbox in it: the
// record of the same mailbox with its tokens replaced (id, label and added
// date kept), or a new record at the end.
func placed(accounts []json.RawMessage, box mailbox, granted grant, secretKey string, now time.Time) ([]json.RawMessage, error) {
	if strings.ContainsRune(box.Address, 0) || strings.ContainsRune(box.Subject, 0) {
		return nil, errUnstorable
	}
	fields, err := tokenFields(granted, secretKey, now)
	if err != nil {
		return nil, err
	}
	fields["username"] = box.Address
	fields[subjectKey] = box.Subject
	fields[encryptedKey] = true

	out := make([]json.RawMessage, 0, len(accounts)+1)
	replaced := false
	for _, raw := range accounts {
		var held record
		// An entry that is not a record is kept as it is.
		if replaced || json.Unmarshal(raw, &held) != nil || !sameMailbox(held, box) {
			out = append(out, raw)
			continue
		}
		if err := held.Set(fields); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(held)
		if err != nil {
			return nil, err
		}
		out = append(out, encoded)
		replaced = true
	}
	if replaced {
		return out, nil
	}
	id, err := newAccountID()
	if err != nil {
		return nil, err
	}
	fresh := record{}
	fields["id"] = id
	fields["label"] = box.Address
	fields["host"] = IMAPHost
	fields["port"] = IMAPPort
	fields["use_tls"] = true
	fields[authKey] = authValue
	fields["created_at"] = now.UTC().Format(recordTime)
	if err := fresh.Set(fields); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(fresh)
	if err != nil {
		return nil, err
	}
	return append(out, encoded), nil
}
