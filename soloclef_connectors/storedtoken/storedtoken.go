// Package storedtoken opens the credentials a connection keeps encrypted
// in its row: a Nextcloud app password, a Google grant's tokens.
//
// The Python tier encrypts them when a person connects an account
// (app.utils.oauth_security.TokenEncryption) and marks the row
// `_token_encrypted`. This tier writes them the same way (Seal) when a person
// connects an account through it. It opens one only to use it against the
// service it belongs to: the
// Nextcloud import, to download the files a person picked
// (internal/nextcloudimport), and the removal of a connection, to revoke
// the credential at its provider so that a deleted connection is dead there
// too (internal/searchconnectors). The key is the deployment's SECRET_KEY.
package storedtoken

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ErrUnreadable: the token is not one this key wrote, or is damaged. The
// two are not told apart, on purpose: the answer to either is a new
// sign-in, and a caller told which it was would learn something about a
// key it does not hold.
var ErrUnreadable = errors.New("storedtoken: the stored token cannot be decrypted with this key")

// Open decrypts a token the Python tier's TokenEncryption wrote.
//
// That class is the `cryptography` library's Fernet with a key derived as
// SHA-256 of the secret. A Fernet token is, base64url-encoded:
//
//	0x80 | 8-byte timestamp | 16-byte IV | AES-128-CBC ciphertext | 32-byte HMAC-SHA256
//
// The 32-byte key is two halves: the first signs, the second encrypts. The
// HMAC covers everything before it and is checked first, in constant time,
// so nothing is decrypted that this key did not write. No age limit is
// applied: the Python tier applies none, and a stored password does not
// expire. The standard library has every piece, which is why this is forty
// lines and not a dependency.
func Open(secretKey, token string) (string, error) {
	key := sha256.Sum256([]byte(secretKey))
	signing, encryption := key[:16], key[16:]

	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		// The library also takes a token whose padding was stripped.
		if raw, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(token, "=")); err != nil {
			return "", ErrUnreadable
		}
	}
	const header, ivLength, macLength = 1 + 8, aes.BlockSize, sha256.Size
	if len(raw) < header+ivLength+aes.BlockSize+macLength || raw[0] != 0x80 {
		return "", ErrUnreadable
	}
	signed, mac := raw[:len(raw)-macLength], raw[len(raw)-macLength:]
	check := hmac.New(sha256.New, signing)
	check.Write(signed)
	if !hmac.Equal(check.Sum(nil), mac) {
		return "", ErrUnreadable
	}
	iv, ciphertext := signed[header:header+ivLength], signed[header+ivLength:]
	if len(ciphertext)%aes.BlockSize != 0 {
		return "", ErrUnreadable
	}
	block, err := aes.NewCipher(encryption)
	if err != nil {
		return "", ErrUnreadable
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	// PKCS#7: the last byte says how many bytes of padding there are, and
	// each of them holds that number.
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(plain) {
		return "", ErrUnreadable
	}
	for _, filler := range plain[len(plain)-padding:] {
		if int(filler) != padding {
			return "", ErrUnreadable
		}
	}
	return string(plain[:len(plain)-padding]), nil
}

// Seal encrypts a credential for a connection's row, in the form Open reads
// and the Python tier's TokenEncryption wrote: a Fernet token under the key
// derived from secretKey. A row written here is therefore read by every
// reader of a row written there, in either tier, and the other way round.
//
// now goes into the token's timestamp field, which nothing reads (no age
// limit is applied to a stored credential); random is where the IV comes
// from, crypto/rand.Reader in a deployment.
func Seal(secretKey, plaintext string, now time.Time, random io.Reader) (string, error) {
	key := sha256.Sum256([]byte(secretKey))
	signing, encryption := key[:16], key[16:]

	const header = 1 + 8
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	raw := make([]byte, header+aes.BlockSize+len(plaintext)+padding, header+aes.BlockSize+len(plaintext)+padding+sha256.Size)
	raw[0] = 0x80
	binary.BigEndian.PutUint64(raw[1:header], uint64(now.Unix()))
	iv := raw[header : header+aes.BlockSize]
	if _, err := io.ReadFull(random, iv); err != nil {
		return "", fmt.Errorf("storedtoken: no randomness for the token: %w", err)
	}
	body := raw[header+aes.BlockSize:]
	copy(body, plaintext)
	// PKCS#7: every byte of the padding holds its length.
	for index := len(plaintext); index < len(body); index++ {
		body[index] = byte(padding)
	}
	block, err := aes.NewCipher(encryption)
	if err != nil {
		return "", err
	}
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(body, body)
	sign := hmac.New(sha256.New, signing)
	sign.Write(raw)
	return base64.URLEncoding.EncodeToString(sign.Sum(raw)), nil
}
