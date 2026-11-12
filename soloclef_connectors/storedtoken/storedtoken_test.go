package storedtoken

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
)

// Tokens written by the Python tier's own library (cryptography's Fernet,
// keyed as TokenEncryption keys it) for a secret made up for this test:
//
//	key = urlsafe_b64encode(sha256(b"test-vector-secret-not-a-real-key").digest())
//	Fernet(key).encrypt(plaintext)
//
// If Open ever stops opening them, every stored Nextcloud connection stops
// working and no removed connection is revoked at its provider.
const (
	vectorSecret = "test-vector-secret-not-a-real-key"
	vectorLong   = "gAAAAABq9Iq7NAQ3pWlKWW79LYRYl2pUa2e-F60LMLbmkg_d7PBJjjTciR3kxkfQw9zp6_TjtxReKRTayvKrw6CQrg83OsSDHGne0cjbivazpSPUqjsQo-s="
	vectorShort  = "gAAAAABq9Iq75iXUEqvN8K7YnxXS1H1VsuwbYL6w0wp-ij5w9gM8g3NwRAjV8yDXANLNlJkXui48m-KGwNgaW4kZ0jXkadfqqQ=="
)

func TestATokenThePythonTierEncryptedIsOpened(t *testing.T) {
	for token, want := range map[string]string{
		vectorLong:  "app-password-Abc12-xyZ34 é",
		vectorShort: "x",
		// The same token with its padding stripped, as some stores keep it.
		strings.TrimRight(vectorShort, "="): "x",
	} {
		got, err := Open(vectorSecret, token)
		if err != nil || got != want {
			t.Errorf("Open = %q, %v; want %q", got, err, want)
		}
	}
}

func TestATokenThisKeyDidNotWriteIsRefused(t *testing.T) {
	flipped := []byte(vectorLong)
	// One character of the ciphertext changed: the signature no longer
	// matches, and nothing is decrypted.
	if flipped[40] == 'A' {
		flipped[40] = 'B'
	} else {
		flipped[40] = 'A'
	}
	for name, attempt := range map[string]struct{ secret, token string }{
		"another deployment's key": {"some-other-secret", vectorLong},
		"a token tampered with":    {vectorSecret, string(flipped)},
		"a token cut short":        {vectorSecret, vectorLong[:60]},
		"not base64":               {vectorSecret, "not a token at all!"},
		"nothing":                  {vectorSecret, ""},
		"another version byte":     {vectorSecret, "h" + vectorLong[1:]},
	} {
		if got, err := Open(attempt.secret, attempt.token); !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s: opened as %q, %v", name, got, err)
		}
	}
}

// What this tier seals, it and the Python tier open: the token is Fernet's
// layout byte for byte, with the timestamp and IV it was given.
func TestSealWritesWhatOpenReads(t *testing.T) {
	const secret = "a deployment's secret key"
	at := time.Unix(1_700_000_000, 0)
	for _, plain := range []string{"", "x", "exactly sixteen!", "an app password: fake-Abc12-dEf34-Ghi56", "é and 🎵"} {
		sealed, err := Seal(secret, plain, at, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		opened, err := Open(secret, sealed)
		if err != nil || opened != plain {
			t.Errorf("%q came back as %q (%v)", plain, opened, err)
		}
		if _, err := Open("another key", sealed); !errors.Is(err, ErrUnreadable) {
			t.Errorf("another key opened %q", plain)
		}
		raw, err := base64.URLEncoding.DecodeString(sealed)
		if err != nil {
			t.Fatalf("the token is not padded base64url: %v", err)
		}
		if raw[0] != 0x80 || binary.BigEndian.Uint64(raw[1:9]) != 1_700_000_000 || (len(raw)-9-16-32)%16 != 0 {
			t.Errorf("the token is not Fernet's layout: % x", raw[:9])
		}
	}
	// Two seals of one credential differ: the IV is random, so a stored
	// token does not say whether two connections hold the same password.
	first, _ := Seal(secret, "same", at, rand.Reader)
	second, _ := Seal(secret, "same", at, rand.Reader)
	if first == second {
		t.Error("two seals of one credential are the same token")
	}
	if _, err := Seal(secret, "x", at, strings.NewReader("short")); err == nil {
		t.Error("a short read of randomness was not an error")
	}
}

// Given the timestamp and the IV of a token the Python tier's library
// wrote, Seal writes that token, character for character.
func TestSealIsThePythonLibrarysEncryption(t *testing.T) {
	raw, err := base64.URLEncoding.DecodeString(vectorLong)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(int64(binary.BigEndian.Uint64(raw[1:9])), 0)
	sealed, err := Seal(vectorSecret, "app-password-Abc12-xyZ34 é", at, bytes.NewReader(raw[9:25]))
	if err != nil || sealed != vectorLong {
		t.Errorf("Seal = %q, %v; want the library's %q", sealed, err, vectorLong)
	}
}
