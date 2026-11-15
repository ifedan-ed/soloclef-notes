package ownmodels

import (
	"errors"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// KeySeal encrypts a provider key (and a connection's headers) before it is
// stored and opens it for the things that use it: a call to the endpoint,
// and a test through the model proxy.
//
// It is sealedsecret under the model connection's purpose: AES-256-GCM under
// a key derived from the deployment's secret, the owner's id bound in. Two
// tiers open what it sealed (the API tier for the chat and the settings
// screen's tests, the worker for the jobs that run on an account's chosen
// model), and one implementation means one format. The purpose's label is
// the one these rows were sealed under before the cipher moved to its own
// package, so the move opened them all.
type KeySeal struct {
	seal sealedsecret.Seal
}

// ErrSealUnreadable is a stored key this deployment's secret did not seal,
// or a damaged one. The two are not told apart: the cure for either is to
// type the key in again.
var ErrSealUnreadable = errors.New("ownmodels: the stored key cannot be opened with this deployment's secret")

// NewKeySeal derives the sealing key from the deployment's secret.
func NewKeySeal(secret string) (KeySeal, error) {
	return NewPurposeSeal(secret, "soloclef model connection key v1")
}

// NewPurposeSeal is the same seal, for another kind of credential, under a
// key of its own: purpose is mixed into the derivation, so a mailbox's
// token sealed for one purpose does not open as a provider key, and a leak
// of one derived key opens nothing of the other. One implementation and
// one stored format for every credential an account keeps sealed, instead
// of a second cipher written beside this one.
//
// purpose names the kind and its version ("soloclef jmap mail credentials
// v1"); changing it changes the key, so it is a constant of the caller.
func NewPurposeSeal(secret, purpose string) (KeySeal, error) {
	if secret == "" {
		return KeySeal{}, errors.New("ownmodels: the secret that seals stored credentials is not set")
	}
	if purpose == "" {
		return KeySeal{}, errors.New("ownmodels: a seal needs a purpose, which keeps its key apart from every other")
	}
	// sealedsecret derives sha256(purpose + NUL + secret), the derivation this
	// package used before it moved there, so rows sealed earlier still open.
	seal, err := sealedsecret.New(secret, sealedsecret.Purpose(purpose))
	if err != nil {
		return KeySeal{}, err
	}
	return KeySeal{seal: seal}, nil
}

// Ready reports whether the seal was made by NewKeySeal; the zero value
// can neither seal nor open.
func (s KeySeal) Ready() bool { return s.seal.Ready() }

// Seal is the text to store for an account's key.
func (s KeySeal) Seal(userID, key string) (string, error) {
	if !s.seal.Ready() {
		return "", errors.New("ownmodels: the key seal was not made with NewKeySeal")
	}
	return s.seal.Seal(userID, key)
}

// Open is the key an account's stored text holds.
func (s KeySeal) Open(userID, sealed string) (string, error) {
	if !s.seal.Ready() {
		return "", errors.New("ownmodels: the key seal was not made with NewKeySeal")
	}
	key, err := s.seal.Open(userID, sealed)
	if errors.Is(err, sealedsecret.ErrUnreadable) {
		return "", ErrSealUnreadable
	}
	return key, err
}
