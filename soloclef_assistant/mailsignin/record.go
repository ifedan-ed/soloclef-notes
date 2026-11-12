package mailsignin

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// A Microsoft mailbox as the mail connection's `accounts` list stores it.
//
// It sits in the same list as password mailboxes, with the same
// `_encrypted` marker, and is told from them by `auth`. `host` and `port`
// are for display: the token only ever goes to IMAPHost (signin.go). The
// keys are the ones the Python tier wrote, so a mailbox connected through
// either tier is one record.
const (
	AuthKey       = "auth"
	AuthMicrosoft = "microsoft"
	AccessKey     = "access_token"
	RefreshKey    = "refresh_token"
	ExpiresKey    = "access_token_expires_at"
	SubjectKey    = "microsoft_subject"
	ReconnectKey  = "needs_reconnect"
	EncryptedKey  = "_encrypted"
)

// RecordTime is the form a record's times have always had: ISO 8601 with
// microseconds and an offset.
const RecordTime = "2006-01-02T15:04:05.000000-07:00"

// Record is one stored mailbox with every key it holds, known or not: a
// reconnect or a refresh changes the token keys and keeps the rest.
type Record map[string]json.RawMessage

// Text is a key's value when it is a string, else "".
func (r Record) Text(key string) string {
	var value string
	_ = json.Unmarshal(r[key], &value)
	return value
}

// Truth is a key's value when it is a boolean, else false.
func (r Record) Truth(key string) bool {
	var value bool
	_ = json.Unmarshal(r[key], &value)
	return value
}

// IsMicrosoft reports whether the mailbox was connected through Microsoft.
func (r Record) IsMicrosoft() bool { return r.Text(AuthKey) == AuthMicrosoft }

// Set writes values into the record.
func (r Record) Set(values map[string]any) error {
	for key, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		r[key] = encoded
	}
	return nil
}

// TokenFields is a grant as a record holds it: both tokens sealed, when
// the access token lapses, and the reconnect flag cleared. The refresh
// token is left out when Microsoft kept the previous one, so merging these
// over a record keeps what it had.
func TokenFields(granted Grant, secretKey string, now time.Time) (map[string]any, error) {
	access, err := storedtoken.Seal(secretKey, granted.AccessToken, now, rand.Reader)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{
		AccessKey:    access,
		ExpiresKey:   granted.Expiry.UTC().Format(RecordTime),
		ReconnectKey: false,
	}
	if granted.RefreshToken != "" {
		refresh, err := storedtoken.Seal(secretKey, granted.RefreshToken, now, rand.Reader)
		if err != nil {
			return nil, err
		}
		fields[RefreshKey] = refresh
	}
	return fields, nil
}

// ErrTokensUnreadable: a record's tokens are missing, or were sealed with
// a key this deployment no longer has. The remedy is to reconnect.
var ErrTokensUnreadable = errors.New("mailsignin: the mailbox's stored tokens cannot be read")

// StoredTokens opens a record's tokens. access is "" when the record holds
// no usable access token (none, or no readable expiry); the refresh token
// is always required.
func StoredTokens(held Record, secretKey string) (access string, expires time.Time, refresh string, err error) {
	sealed := held.Text(RefreshKey)
	if sealed == "" || !held.Truth(EncryptedKey) {
		return "", time.Time{}, "", ErrTokensUnreadable
	}
	if refresh, err = storedtoken.Open(secretKey, sealed); err != nil || refresh == "" {
		return "", time.Time{}, "", ErrTokensUnreadable
	}
	sealed = held.Text(AccessKey)
	expires, parseErr := time.Parse(time.RFC3339Nano, held.Text(ExpiresKey))
	if sealed == "" || parseErr != nil {
		return "", time.Time{}, refresh, nil
	}
	if access, err = storedtoken.Open(secretKey, sealed); err != nil {
		return "", time.Time{}, "", ErrTokensUnreadable
	}
	return access, expires, refresh, nil
}
