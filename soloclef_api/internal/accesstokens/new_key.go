package accesstokens

import (
	"crypto/rand"
	"encoding/base64"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// keyBytes is how much randomness a key carries: 256 bits, so a key cannot
// be guessed, and its hash (the only thing stored) cannot be reversed by
// trying keys.
const keyBytes = 32

// prefixLength is how much of a key is kept in the clear: the "ss_pat_" the
// gate tells keys from session tokens by, and nine characters of the random
// part, which is what a person tells their keys apart by in the list. It is
// the column's width (personal_access_tokens.token_prefix, varchar(16)).
const prefixLength = 16

// NewKey is a freshly minted key, and what is stored of it.
type NewKey struct {
	// Key is the whole key. It is handed to its owner once, in the create's
	// answer, and kept nowhere.
	Key string
	// Hash is what the gate looks a presented key up by
	// (principal.HashToken), so a stolen copy of the table opens nothing.
	Hash string
	// Prefix is the part the list shows.
	Prefix string
}

// MintKey makes a new key from the operating system's random source. A
// failure of that source is an error and never a weaker key.
func MintKey() (NewKey, error) {
	random := make([]byte, keyBytes)
	if _, err := rand.Read(random); err != nil {
		return NewKey{}, err
	}
	// URL-safe and unpadded, so a key can be pasted into a header, a URL or
	// a shell without quoting.
	key := principal.TokenPrefix + base64.RawURLEncoding.EncodeToString(random)
	return NewKey{Key: key, Hash: principal.HashToken(key), Prefix: key[:prefixLength]}, nil
}
