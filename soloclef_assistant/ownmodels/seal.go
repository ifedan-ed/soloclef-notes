package ownmodels

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// KeySeal encrypts a provider key (and a connection's headers) before it is
// stored and opens it for the things that use it: a call to the endpoint,
// and a test through the model proxy.
//
// It lives here, and not in the API tier that seals, because two tiers open
// what it sealed: the API tier for the chat and the settings screen's tests,
// and the worker for the podcast, video and automation jobs that run on an
// account's chosen model. One implementation means one format.
//
// The column holds AES-256-GCM ciphertext under a key derived from the
// deployment's secret, and the owner's id is bound in as associated data,
// so a ciphertext copied onto another account's row does not open there.
// The Python tier stored the key as typed; anyone who could read the table,
// a dump of it or a backup had every account's provider keys.
//
// The stored text is "v1:" and the base64 of nonce and ciphertext. The
// version is there so the derivation or the cipher can change later with
// both spellings readable while rows are re-sealed.
type KeySeal struct {
	aead cipher.AEAD
}

const sealVersion = "v1:"

// ErrSealUnreadable is a stored key this deployment's secret did not seal,
// or a damaged one. The two are not told apart: the cure for either is to
// type the key in again.
var ErrSealUnreadable = errors.New("ownmodels: the stored key cannot be opened with this deployment's secret")

// NewKeySeal derives the sealing key from the deployment's secret. The
// label keeps it apart from every other use of that secret: the bytes that
// sign a session cannot open a provider key. The label is the one the API
// tier's rows were sealed under, so moving the code here opened them all.
func NewKeySeal(secret string) (KeySeal, error) {
	if secret == "" {
		return KeySeal{}, errors.New("ownmodels: the secret that seals provider keys is not set")
	}
	key := sha256.Sum256([]byte("soloclef model connection key v1\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return KeySeal{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return KeySeal{}, err
	}
	return KeySeal{aead: aead}, nil
}

// Ready reports whether the seal was made by NewKeySeal; the zero value
// can neither seal nor open.
func (s KeySeal) Ready() bool { return s.aead != nil }

// Seal is the text to store for an account's key.
func (s KeySeal) Seal(userID, key string) (string, error) {
	if s.aead == nil {
		return "", errors.New("ownmodels: the key seal was not made with NewKeySeal")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return sealVersion + base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, []byte(key), []byte(userID))), nil
}

// Open is the key an account's stored text holds.
func (s KeySeal) Open(userID, sealed string) (string, error) {
	if s.aead == nil {
		return "", errors.New("ownmodels: the key seal was not made with NewKeySeal")
	}
	encoded, versioned := strings.CutPrefix(sealed, sealVersion)
	if !versioned {
		return "", ErrSealUnreadable
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return "", ErrSealUnreadable
	}
	nonce, ciphertext := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	key, err := s.aead.Open(nil, nonce, ciphertext, []byte(userID))
	if err != nil {
		return "", ErrSealUnreadable
	}
	return string(key), nil
}
