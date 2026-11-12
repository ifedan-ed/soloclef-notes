package agentsignin

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// acceptedAlgorithms is access_token.ACCEPTED_ALGORITHMS: signatures made with
// a public/private key pair only. "none" and the HMAC family are never
// accepted, whatever the discovery document says.
var acceptedAlgorithms = []string{"RS256", "RS384", "RS512", "PS256", "ES256", "ES384"}

// keyRefreshCooldown is access_token.KEY_REFRESH_COOLDOWN_SECONDS: a token
// naming a key id that is not in the cached set makes the set be read again, so
// a key rotation needs no restart, but at most once a minute, so a stream of
// forged tokens with made-up key ids cannot become a stream of requests to the
// provider.
const keyRefreshCooldown = 60 * time.Second

// Verifier is one provider's keys and access_token.py's rules, shared by every
// request.
type Verifier struct {
	Settings  Settings
	Directory *ProviderDirectory
	// Now is overridable for tests only.
	Now func() time.Time

	mutex             sync.Mutex
	lastForcedRefresh *time.Time
}

// OwnsIssuer reports whether a token's unverified issuer names this provider,
// as access_token.claims_this_issuer does.
func (v *Verifier) OwnsIssuer(issuer string) bool { return v.Settings.IsThisIssuer(issuer) }

// Verify checks the token against the provider and returns what it says about
// the person behind the agent, or ErrRefused when it is not a valid sign-in, or
// ErrProviderUnavailable when it could not be judged either way.
func (v *Verifier) Verify(ctx context.Context, token string) (Identity, error) {
	claims, err := v.claims(ctx, token)
	if err != nil {
		return Identity{}, err
	}
	return identityFrom(claims), nil
}

// Claims is the claim set of a token that passed every check Verify makes:
// the signature against the provider's published keys, the algorithm, the
// issuer, this client as the audience and authorized party, and the times.
//
// It is here for the web's single sign-on (internal/oidcsignin), whose ID
// token is judged by exactly these rules and which then reads claims an
// agent's token does not carry (the nonce of the sign-in it answers, the
// person's name). One verifier, so the two sign-ins cannot come to disagree
// about what a valid token from the provider is.
func (v *Verifier) Claims(ctx context.Context, token string) (map[string]json.RawMessage, error) {
	return v.claims(ctx, token)
}

func (v *Verifier) claims(ctx context.Context, token string) (rawClaims, error) {
	metadata, err := v.Directory.Metadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProviderUnavailable, err)
	}

	header, err := readHeader(token)
	if err != nil {
		return nil, fmt.Errorf("%w: not a JWT: %w", ErrRefused, err)
	}
	if !slices.Contains(acceptedAlgorithms, header.Algorithm) ||
		!slices.Contains(metadata.SigningAlgorithms, header.Algorithm) {
		return nil, fmt.Errorf("%w: signed with %s", ErrRefused, header.Algorithm)
	}

	key, err := v.signingKey(ctx, header.KeyID)
	if err != nil {
		return nil, fmt.Errorf("%w: provider keys unreadable: %w", ErrProviderUnavailable, err)
	}
	if key == nil {
		return nil, fmt.Errorf("%w: signed with an unknown key", ErrRefused)
	}

	claims := rawClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{header.Algorithm}),
		// The claims are checked by checkClaims, which is PyJWT's own
		// validation rather than this library's.
		jwt.WithoutClaimsValidation(),
	)
	if _, err := parser.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) {
		if key.PublicKey == nil {
			// A key the provider publishes that this tier cannot verify with.
			// The Python tier hands the same token to the same key and the
			// verification fails there too.
			return nil, fmt.Errorf("the key named is not one this tier can verify with")
		}
		return key.PublicKey, nil
	}); err != nil {
		return nil, fmt.Errorf("%w: rejected: %w", ErrRefused, err)
	}

	if err := checkClaims(claims, v.Settings.ClientID, metadata.Issuer, v.now()); err != nil {
		return nil, err
	}
	if err := checkAuthorizedParty(claims, v.Settings.ClientID); err != nil {
		return nil, err
	}
	return claims, nil
}

// checkAuthorizedParty is the check after jwt.decode: the provider always names
// the client a token was issued to in azp, and with several audiences it must be
// the agent client (OpenID Connect Core 3.1.3.7).
func checkAuthorizedParty(claims rawClaims, clientID string) error {
	audiences, err := audienceList(claims["aud"])
	if err != nil {
		return err
	}
	raw, present := claims["azp"]
	party, isString := jsonString(raw)
	hasParty := present && !isNull(raw)
	if hasParty || len(audiences) > 1 {
		if !isString || party != clientID {
			return fmt.Errorf("%w: issued to another client", ErrRefused)
		}
	}
	return nil
}

// signingKey is AgentTokenVerifier._signing_key: the key the token names, with
// one forced re-read of the key set when it is not there, no more often than the
// cooldown allows.
func (v *Verifier) signingKey(ctx context.Context, keyID *string) (*signingKey, error) {
	document, err := v.Directory.SigningKeys(ctx, false)
	if err != nil {
		return nil, err
	}
	key, err := keyFor(document, keyID)
	if err != nil {
		return nil, err
	}
	if key != nil {
		return key, nil
	}

	if !v.mayForceRefresh() {
		return nil, nil
	}
	document, err = v.Directory.SigningKeys(ctx, true)
	if err != nil {
		return nil, err
	}
	return keyFor(document, keyID)
}

func (v *Verifier) mayForceRefresh() bool {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	now := v.now()
	if v.lastForcedRefresh != nil && now.Sub(*v.lastForcedRefresh) < keyRefreshCooldown {
		return false
	}
	v.lastForcedRefresh = &now
	return true
}

// keyFor is access_token._key_for: the key with that id, or the first key in the
// set when the token names none.
func keyFor(document []byte, keyID *string) (*signingKey, error) {
	keys, err := parseKeySet(document)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if keyID == nil || key.KeyID == *keyID {
			found := key
			return &found, nil
		}
	}
	return nil, nil
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// rawClaims keeps each claim as the bytes the token carried, so a claim's JSON
// type is still readable when it is checked. The accessors the JWT library
// requires are never called: this tier validates the claims itself.
type rawClaims map[string]json.RawMessage

func (rawClaims) GetExpirationTime() (*jwt.NumericDate, error) { return nil, nil }
func (rawClaims) GetIssuedAt() (*jwt.NumericDate, error)       { return nil, nil }
func (rawClaims) GetNotBefore() (*jwt.NumericDate, error)      { return nil, nil }
func (rawClaims) GetIssuer() (string, error)                   { return "", nil }
func (rawClaims) GetSubject() (string, error)                  { return "", nil }
func (rawClaims) GetAudience() (jwt.ClaimStrings, error)       { return nil, nil }

// The pointer is what the parser decodes into: its map special case is only for
// its own MapClaims.
var _ jwt.Claims = &rawClaims{}
