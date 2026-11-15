package jmapaccounts

import (
	"encoding/json"
	"errors"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// sealPurpose keeps the key mailbox credentials are sealed under apart from
// every other key derived from the deployment's secret. Changing it makes
// every stored mailbox unreadable, so it carries a version instead.
const sealPurpose = "soloclef jmap mail credentials v1"

// Secrets is a mailbox's credentials in the clear: the app password, or
// the OAuth tokens. It exists only between opening a row and using it.
type Secrets struct {
	Password      string    `json:"password,omitempty"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	AccessToken   string    `json:"access_token,omitempty"`
	AccessExpires time.Time `json:"access_expires,omitzero"`
}

// Seal seals and opens Secrets for one owner.
type Seal struct{ seal ownmodels.KeySeal }

// NewSeal derives the key from the deployment's secret.
func NewSeal(secret string) (Seal, error) {
	keySeal, err := ownmodels.NewPurposeSeal(secret, sealPurpose)
	return Seal{seal: keySeal}, err
}

// Ready reports whether the seal was made by NewSeal.
func (s Seal) Ready() bool { return s.seal.Ready() }

// Close is the sealed text of an owner's secrets, for the row.
func (s Seal) Close(ownerID string, secrets Secrets) (string, error) {
	plain, err := json.Marshal(secrets)
	if err != nil {
		return "", err
	}
	return s.seal.Seal(ownerID, string(plain))
}

// errUnreadable is a row this deployment's secret did not seal, or one that
// is damaged: the cure is to connect the mailbox again.
var errUnreadable = errors.New("jmapaccounts: the mailbox's credentials cannot be opened")

// open is an owner's secrets from the row's sealed text.
func (s Seal) open(ownerID, sealed string) (Secrets, error) {
	plain, err := s.seal.Open(ownerID, sealed)
	if err != nil {
		return Secrets{}, errUnreadable
	}
	var secrets Secrets
	if json.Unmarshal([]byte(plain), &secrets) != nil {
		return Secrets{}, errUnreadable
	}
	return secrets, nil
}
