package ownmodels

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

// A key sealed the way this package sealed before the cipher moved to
// sealedsecret (the derivation written out here by hand) still opens: every
// stored model connection depends on it.
func TestAKeySealedBeforeTheMoveStillOpens(t *testing.T) {
	const secret, owner = "fake-deployment-secret", "11111111-1111-4111-8111-111111111111"
	derived := sha256.Sum256([]byte("soloclef model connection key v1\x00" + secret))
	block, err := aes.NewCipher(derived[:])
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	for index := range nonce {
		nonce[index] = byte(index)
	}
	stored := "v1:" + base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte("fake-provider-key"), []byte(owner)))

	seal, err := NewKeySeal(secret)
	if err != nil {
		t.Fatal(err)
	}
	if key, err := seal.Open(owner, stored); err != nil || key != "fake-provider-key" {
		t.Fatalf("open = %q, %v", key, err)
	}
}
