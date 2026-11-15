// Package sealedsecret encrypts a credential before it is stored and opens
// it for the one thing that uses it.
//
// It is the strongest of the two schemes this codebase has, and the one new
// columns use: AES-256-GCM under a key derived from the deployment's secret,
// with the owner's id bound in as associated data, so a ciphertext copied
// onto another account's row does not open there. The other scheme,
// storedtoken, is the Python tier's Fernet (AES-128-CBC with an HMAC, no
// owner binding); it stays for the rows that tier wrote and reads.
//
// Each use derives its own key from the one secret under its own Purpose
// label. A provider key sealed for a model connection therefore cannot be
// opened as an MCP server's header, even for the same owner: a ciphertext
// is only ever meaningful in the column it was written for.
//
// The stored text is "v1:" and the base64 of nonce and ciphertext. The
// version is there so the derivation or the cipher can change later with
// both spellings readable while rows are re-sealed.
//
// It lives in this module, which requires nothing outside the standard
// library, because four places seal or open with it and must agree on the
// format: the API tier (writes), the chat's tools and the worker (opens),
// and the schema's data migration that sealed the rows written in the clear.
package sealedsecret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// Purpose names one use of the deployment's secret. The text is part of the
// key derivation, so a Purpose's text is never changed once rows are sealed
// under it: a new text is a new key, and every stored value would stop
// opening.
type Purpose string

// The purposes. Listed in one place so that a reviewer sees every key the
// one secret is stretched into.
const (
	// ModelConnectionKey is an account's own model provider key and headers
	// (soloclef_assistant/ownmodels). Its text predates this package and is
	// what the existing rows were sealed under.
	ModelConnectionKey Purpose = "soloclef model connection key v1"
	// MCPServerSecret is every secret-bearing part of an MCP server's
	// settings: header values, an address that carries a key, an OAuth
	// client secret and the OAuth tokens (mcpserver).
	MCPServerSecret Purpose = "soloclef mcp server secret v1"
	// WebSearchKey is an account's Exa or Tavily API key.
	WebSearchKey Purpose = "soloclef web search key v1"
	// MCPOAuthAttempt is an MCP OAuth sign-in in progress, carried in a
	// cookie between its start and the provider's callback.
	MCPOAuthAttempt Purpose = "soloclef mcp oauth attempt v1"
)

const version = "v1:"

// ErrUnreadable is a stored value this deployment's secret did not seal
// for this purpose and owner, or a damaged one. The cases are not told
// apart: the cure for each is to enter the credential again, and a caller
// told which it was would learn something about a key it does not hold.
var ErrUnreadable = errors.New("sealedsecret: the stored value cannot be opened with this deployment's secret")

// Seal is one purpose's key. The zero value can neither seal nor open.
type Seal struct {
	aead cipher.AEAD
}

// New derives purpose's key from the deployment's secret. The NUL keeps a
// label and a secret from running into each other.
func New(secret string, purpose Purpose) (Seal, error) {
	switch {
	case secret == "":
		return Seal{}, errors.New("sealedsecret: the secret that seals stored credentials is not set")
	case purpose == "":
		return Seal{}, errors.New("sealedsecret: a purpose is required")
	}
	key := sha256.Sum256([]byte(string(purpose) + "\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return Seal{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Seal{}, err
	}
	return Seal{aead: aead}, nil
}

// Ready reports whether the seal was made by New.
func (s Seal) Ready() bool { return s.aead != nil }

// Seal is the text to store for owner's plaintext. A fresh random nonce
// each time, so sealing the same value twice stores two different texts.
func (s Seal) Seal(owner, plaintext string) (string, error) {
	if s.aead == nil {
		return "", errors.New("sealedsecret: the seal was not made with New")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return version + base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, []byte(plaintext), []byte(owner))), nil
}

// Open is the plaintext owner's stored text holds.
func (s Seal) Open(owner, sealed string) (string, error) {
	if s.aead == nil {
		return "", errors.New("sealedsecret: the seal was not made with New")
	}
	encoded, versioned := strings.CutPrefix(sealed, version)
	if !versioned {
		return "", ErrUnreadable
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return "", ErrUnreadable
	}
	nonce, ciphertext := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	plain, err := s.aead.Open(nil, nonce, ciphertext, []byte(owner))
	if err != nil {
		return "", ErrUnreadable
	}
	return string(plain), nil
}

// IsSealed reports whether text has the form Seal writes. It does not say
// the text opens; it tells a sealed value from one written in the clear.
func IsSealed(text string) bool { return strings.HasPrefix(text, version) }
