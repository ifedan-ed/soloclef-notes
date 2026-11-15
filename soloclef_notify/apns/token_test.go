package apns_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_notify/apns"
	"git.danvics.com/danvics/soloclef/soloclef_notify/apns/apnstest"
)

// verify checks a provider token as Apple does: its header and claims, and
// its ES256 signature against the key's public half. It answers the claims.
func verify(t *testing.T, token string, public *ecdsa.PublicKey) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("%q is not a JWT", token)
	}
	decode := func(part string) []byte {
		raw, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil {
			t.Fatalf("%q is not base64url without padding: %v", part, err)
		}
		return raw
	}
	var header map[string]string
	if err := json.Unmarshal(decode(parts[0]), &header); err != nil || header["alg"] != "ES256" || header["kid"] != apnstest.KeyID || len(header) != 2 {
		t.Errorf("header = %v, %v", header, err)
	}
	var claims map[string]any
	if err := json.Unmarshal(decode(parts[1]), &claims); err != nil || claims["iss"] != apnstest.TeamID || len(claims) != 2 {
		t.Errorf("claims = %v, %v", claims, err)
	}
	signature := decode(parts[2])
	if len(signature) != 64 {
		t.Fatalf("the signature is %d bytes; ES256 in a JWT is r||s, 64", len(signature))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r, s := new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(public, digest[:], r, s) {
		t.Error("the signature does not verify with the key's public half")
	}
	return claims
}

// The provider token is the JWT Apple asks for: ES256 with the key id,
// issued by the team at the time it was signed, and verifiable with the
// key's public half.
func TestTheProviderTokenIsAnES256JWTAppleCanVerify(t *testing.T) {
	keyPEM, key := apnstest.Key(t)
	signer, err := apns.NewSigner(keyPEM, apnstest.KeyID, apnstest.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	token, err := signer.Token(now)
	if err != nil {
		t.Fatal(err)
	}
	if claims := verify(t, token, &key.PublicKey); claims["iat"] != float64(now.Unix()) {
		t.Errorf("iat = %v, want %d", claims["iat"], now.Unix())
	}
	// Another key's public half does not verify it.
	_, other := apnstest.Key(t)
	parts := strings.Split(token, ".")
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if ecdsa.Verify(&other.PublicKey, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		t.Error("another key verified the token")
	}
}

// One token is kept for fifty minutes, so Apple is not asked to take a new
// one per push (it throttles that), and is renewed once when Apple says it
// expired, however many streams were refused with it.
func TestTheTokenIsKeptFiftyMinutesAndRenewedOnceWhenStale(t *testing.T) {
	signer, err := apns.NewSigner(apnstest.KeyPEM(t), apnstest.KeyID, apnstest.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	first, _ := signer.Token(start)
	if again, _ := signer.Token(start.Add(49 * time.Minute)); again != first {
		t.Error("a new token was signed before fifty minutes")
	}
	second, _ := signer.Token(start.Add(50 * time.Minute))
	if second == first {
		t.Error("the token was kept past fifty minutes")
	}
	// A clock that went back is not trusted to keep the token.
	if back, _ := signer.Token(start.Add(40 * time.Minute)); back == second {
		t.Error("a token signed in the future was kept")
	}
	current, _ := signer.Token(start.Add(41 * time.Minute))
	// Renewing a token that is no longer the kept one changes nothing: the
	// stream that saw the stale one is late, and another already renewed.
	signer.Renew(first)
	if kept, _ := signer.Token(start.Add(41 * time.Minute)); kept != current {
		t.Error("renewing an old token dropped the current one")
	}
	signer.Renew(current)
	renewed, _ := signer.Token(start.Add(41 * time.Minute))
	if renewed == current {
		t.Error("renewing the kept token did not sign a new one")
	}
	if kept, _ := signer.Token(start.Add(42 * time.Minute)); kept != renewed {
		t.Error("the renewed token was not kept")
	}
}

// Only Apple's kind of key, with ids of Apple's shape, is taken, so a wrong
// file stops the worker at start and not as a 403 on every push.
func TestAKeyThatIsNotApplesIsRefused(t *testing.T) {
	good := apnstest.KeyPEM(t)
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	p384DER, _ := x509.MarshalPKCS8PrivateKey(p384)
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	rsaDER, _ := x509.MarshalPKCS8PrivateKey(rsaKey)
	for name, c := range map[string]struct {
		key           []byte
		keyID, teamID string
	}{
		"not PEM":          {[]byte("not a key"), apnstest.KeyID, apnstest.TeamID},
		"an EC PEM block":  {pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: p384DER}), apnstest.KeyID, apnstest.TeamID},
		"a P-384 key":      {pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: p384DER}), apnstest.KeyID, apnstest.TeamID},
		"an RSA key":       {pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaDER}), apnstest.KeyID, apnstest.TeamID},
		"a short key id":   {good, "ABC", apnstest.TeamID},
		"a lower-case id":  {good, "testkey001", apnstest.TeamID},
		"no team id":       {good, apnstest.KeyID, ""},
		"a long team id":   {good, apnstest.KeyID, "TESTTEAM0001"},
		"garbage in a key": {pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1, 2, 3}}), apnstest.KeyID, apnstest.TeamID},
	} {
		if _, err := apns.NewSigner(c.key, c.keyID, c.teamID); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := apns.NewSigner(good, apnstest.KeyID, apnstest.TeamID); err != nil {
		t.Errorf("Apple's kind of key was refused: %v", err)
	}
}
