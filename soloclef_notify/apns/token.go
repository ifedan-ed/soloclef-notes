package apns

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"
)

// tokenLife is how long one provider token is used before a new one is
// signed. Apple refuses a token older than an hour (ExpiredProviderToken)
// and one renewed more often than every twenty minutes
// (TooManyProviderTokenUpdates), so it is signed once and kept for fifty
// minutes: ten minutes inside Apple's hour for a clock that runs slow.
// Signing one per push would be throttled.
const tokenLife = 50 * time.Minute

// appleID is the shape of a key id and a team id: ten capitals or digits.
var appleID = regexp.MustCompile(`^[A-Z0-9]{10}$`)

// Signer makes the provider token (an ES256 JWT) every request carries, and
// keeps it for tokenLife. It is safe for the parallel streams of one batch:
// they share one token, and at most one of them signs a new one.
type Signer struct {
	key    *ecdsa.PrivateKey
	keyID  string
	teamID string

	mu     sync.Mutex
	token  string
	signed time.Time
}

// NewSigner reads the .p8 key Apple issued (PKCS #8, PEM, an ECDSA P-256
// key) with its key id and the team it belongs to. Anything else is refused
// here, at start-up, rather than as a 403 on the first push.
func NewSigner(keyPEM []byte, keyID, teamID string) (*Signer, error) {
	if !appleID.MatchString(keyID) {
		return nil, fmt.Errorf("apns: the key id %q is not ten capitals or digits (it is in the key's file name, AuthKey_<id>.p8)", keyID)
	}
	if !appleID.MatchString(teamID) {
		return nil, fmt.Errorf("apns: the team id %q is not ten capitals or digits", teamID)
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("apns: the key file is not a PEM \"PRIVATE KEY\" (Apple's .p8)")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apns: the key file: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("apns: the key file is not an ECDSA P-256 key, which is what Apple issues for APNs")
	}
	return &Signer{key: key, keyID: keyID, teamID: teamID}, nil
}

// Token is the provider token to send now: the one kept, or a new one when
// the kept one is tokenLife old (or was dropped by Renew).
func (s *Signer) Token(now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && now.Sub(s.signed) < tokenLife && !now.Before(s.signed) {
		return s.token, nil
	}
	token, err := s.sign(now)
	if err != nil {
		return "", err
	}
	s.token, s.signed = token, now
	return token, nil
}

// Renew drops the kept token if it is still stale, the one Apple answered
// ExpiredProviderToken to, so the next Token signs a new one. When several
// streams were refused with the same token, the first renews it and the
// others find a new one already there: one signature, not one per stream.
func (s *Signer) Renew(stale string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == stale {
		s.token = ""
	}
}

// sign is the JWT Apple asks for: header {alg ES256, kid}, claims {iss: the
// team, iat: now}, signed with the key. ES256 in a JWT is the raw r||s, each
// 32 bytes, not the ASN.1 form crypto/ecdsa writes by default.
func (s *Signer) sign(now time.Time) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "ES256", "kid": s.keyID})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{"iss": s.teamID, "iat": now.Unix()})
	if err != nil {
		return "", err
	}
	encode := base64.RawURLEncoding.EncodeToString
	unsigned := encode(header) + "." + encode(claims)
	digest := sha256.Sum256([]byte(unsigned))
	r, sv, err := ecdsa.Sign(rand.Reader, s.key, digest[:])
	if err != nil {
		return "", fmt.Errorf("apns: signing the provider token: %w", err)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	sv.FillBytes(signature[32:])
	return unsigned + "." + encode(signature), nil
}
