package agentsignin

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// testClientID is the agent application's client id; the issuer cannot be a
// constant because a discovery document is only accepted when its issuer is the
// address it was fetched from, which is this test's own server.
const testClientID = "agents-client"

const discoveryPath = "/application/o/agents/.well-known/openid-configuration"

// fakeProvider is an identity provider that publishes a key set this test made.
// Nothing here is a real key, a real issuer or a real account.
type fakeProvider struct {
	server    *httptest.Server
	issuer    string
	keys      atomic.Value // []byte
	discovery atomic.Int64
	jwks      atomic.Int64
	fail      atomic.Bool
}

func newFakeProvider(t *testing.T, keys []byte) *fakeProvider {
	t.Helper()
	provider := &fakeProvider{}
	provider.keys.Store(keys)
	routes := http.NewServeMux()
	routes.HandleFunc(discoveryPath,
		func(response http.ResponseWriter, _ *http.Request) {
			provider.discovery.Add(1)
			if provider.fail.Load() {
				response.WriteHeader(http.StatusInternalServerError)
				return
			}
			document := map[string]any{
				"issuer":                                provider.issuer,
				"authorization_endpoint":                provider.server.URL + "/authorize",
				"token_endpoint":                        provider.server.URL + "/token",
				"jwks_uri":                              provider.server.URL + "/jwks",
				"id_token_signing_alg_values_supported": []string{"RS256", "ES256"},
			}
			_ = json.NewEncoder(response).Encode(document)
		})
	routes.HandleFunc("/jwks", func(response http.ResponseWriter, _ *http.Request) {
		provider.jwks.Add(1)
		if provider.fail.Load() {
			response.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = response.Write(provider.keys.Load().([]byte))
	})
	provider.server = httptest.NewServer(routes)
	provider.issuer = provider.server.URL + "/application/o/agents/"
	t.Cleanup(provider.server.Close)
	return provider
}

// verifier points at the fake provider but keeps the issuer the tokens claim,
// because the discovery document's own issuer is what a token is checked
// against, not the address it was fetched from.
func (p *fakeProvider) verifier(now func() time.Time) *Verifier {
	settings := Settings{Issuer: p.issuer, ClientID: testClientID, ProviderName: "authentik"}
	return &Verifier{
		Settings:  settings,
		Directory: &ProviderDirectory{DiscoveryURL: p.server.URL + discoveryPath, Now: now},
		Now:       now,
	}
}

type tokenClaims map[string]any

func mint(t *testing.T, key *rsa.PrivateKey, keyID string, method jwt.SigningMethod, claims tokenClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(method, jwt.MapClaims(claims))
	if keyID != "" {
		token.Header["kid"] = keyID
	}
	var signed string
	var err error
	if method == jwt.SigningMethodHS256 {
		signed, err = token.SignedString([]byte("not the provider's key"))
	} else {
		signed, err = token.SignedString(key)
	}
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signed
}

func validClaims(issuer string, now time.Time) tokenClaims {
	return tokenClaims{
		"iss":            issuer,
		"aud":            testClientID,
		"azp":            testClientID,
		"sub":            "provider-subject-1",
		"iat":            now.Add(-time.Minute).Unix(),
		"exp":            now.Add(time.Hour).Unix(),
		"email":          "Agent.Owner@example.invalid",
		"email_verified": true,
		NotesAccessClaim: true,
	}
}

func TestAnAgentTokenIsAcceptedOnlyWhenEverythingHolds(t *testing.T) {
	signing := newRSAKey(t)
	other := newRSAKey(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	cases := map[string]struct {
		token func(t *testing.T, provider *fakeProvider) string
		want  error
	}{
		"a token signed by the provider's current key": {
			func(t *testing.T, provider *fakeProvider) string {
				return mint(t, signing, "signing", jwt.SigningMethodRS256, validClaims(provider.issuer, now))
			},
			nil,
		},
		"an expired token": {
			func(t *testing.T, provider *fakeProvider) string {
				claims := validClaims(provider.issuer, now)
				claims["exp"] = now.Add(-2 * time.Minute).Unix()
				return mint(t, signing, "signing", jwt.SigningMethodRS256, claims)
			}, ErrRefused,
		},
		"a token that expired within the leeway": {
			func(t *testing.T, provider *fakeProvider) string {
				claims := validClaims(provider.issuer, now)
				claims["exp"] = now.Add(-30 * time.Second).Unix()
				return mint(t, signing, "signing", jwt.SigningMethodRS256, claims)
			}, nil,
		},
		"a token signed by another key": {
			func(t *testing.T, provider *fakeProvider) string {
				return mint(t, other, "signing", jwt.SigningMethodRS256, validClaims(provider.issuer, now))
			},
			ErrRefused,
		},
		"a token signed with HS256": {
			func(t *testing.T, provider *fakeProvider) string {
				return mint(t, nil, "signing", jwt.SigningMethodHS256, validClaims(provider.issuer, now))
			},
			ErrRefused,
		},
		"a token for another issuer": {
			func(t *testing.T, provider *fakeProvider) string {
				claims := validClaims(provider.issuer, now)
				claims["iss"] = provider.server.URL + "/application/o/web/"
				return mint(t, signing, "signing", jwt.SigningMethodRS256, claims)
			}, ErrRefused,
		},
		"a token whose issuer lacks the provider's trailing slash": {
			func(t *testing.T, provider *fakeProvider) string {
				claims := validClaims(provider.issuer, now)
				claims["iss"] = strings.TrimSuffix(provider.issuer, "/")
				return mint(t, signing, "signing", jwt.SigningMethodRS256, claims)
			}, ErrRefused,
		},
		"a token for another audience": {
			func(t *testing.T, provider *fakeProvider) string {
				claims := validClaims(provider.issuer, now)
				claims["aud"], claims["azp"] = "the-web-client", "the-web-client"
				return mint(t, signing, "signing", jwt.SigningMethodRS256, claims)
			}, ErrRefused,
		},
		"a token with several audiences and another authorised party": {
			func(t *testing.T, provider *fakeProvider) string {
				claims := validClaims(provider.issuer, now)
				claims["aud"] = []string{testClientID, "the-web-client"}
				claims["azp"] = "the-web-client"
				return mint(t, signing, "signing", jwt.SigningMethodRS256, claims)
			}, ErrRefused,
		},
		"a token with several audiences, this client among them": {
			func(t *testing.T, provider *fakeProvider) string {
				claims := validClaims(provider.issuer, now)
				claims["aud"] = []string{testClientID, "another-client"}
				return mint(t, signing, "signing", jwt.SigningMethodRS256, claims)
			}, nil,
		},
		"a token naming no subject": {
			func(t *testing.T, provider *fakeProvider) string {
				claims := validClaims(provider.issuer, now)
				delete(claims, "sub")
				return mint(t, signing, "signing", jwt.SigningMethodRS256, claims)
			}, ErrRefused,
		},
		"a token with no issued-at": {
			func(t *testing.T, provider *fakeProvider) string {
				claims := validClaims(provider.issuer, now)
				delete(claims, "iat")
				return mint(t, signing, "signing", jwt.SigningMethodRS256, claims)
			}, ErrRefused,
		},
		"a token issued in the future": {
			func(t *testing.T, provider *fakeProvider) string {
				claims := validClaims(provider.issuer, now)
				claims["iat"] = now.Add(10 * time.Minute).Unix()
				return mint(t, signing, "signing", jwt.SigningMethodRS256, claims)
			}, ErrRefused,
		},
		"a token not valid yet": {
			func(t *testing.T, provider *fakeProvider) string {
				claims := validClaims(provider.issuer, now)
				claims["nbf"] = now.Add(10 * time.Minute).Unix()
				return mint(t, signing, "signing", jwt.SigningMethodRS256, claims)
			}, ErrRefused,
		},
		"a token naming a key the provider does not publish": {
			func(t *testing.T, provider *fakeProvider) string {
				return mint(t, signing, "rotated-away", jwt.SigningMethodRS256, validClaims(provider.issuer, now))
			},
			ErrRefused,
		},
		"something that is not a JWT at all": {
			func(t *testing.T, provider *fakeProvider) string { return "not.a.jwt" },
			ErrRefused,
		},
	}

	for name, test := range cases {
		// A verifier of its own per case, so one case's refusal cannot leave a
		// key-refresh cooldown that changes another's.
		provider := newFakeProvider(t, keySet(t, publicJWK(t, "signing", &signing.PublicKey)))
		verifier := provider.verifier(clock)
		_, err := verifier.Verify(context.Background(), test.token(t, provider))
		if test.want == nil && err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if test.want != nil && !errors.Is(err, test.want) {
			t.Errorf("%s: got %v, want %v", name, err, test.want)
		}
	}
}

func TestTheIdentityIsReadAsThePythonTierReadsIt(t *testing.T) {
	signing := newRSAKey(t)
	now := time.Now()
	provider := newFakeProvider(t, keySet(t, publicJWK(t, "signing", &signing.PublicKey)))
	verifier := provider.verifier(nil)

	claims := validClaims(provider.issuer, now)
	identity, err := verifier.Verify(context.Background(), mint(t, signing, "signing", jwt.SigningMethodRS256, claims))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if identity.Subject != "provider-subject-1" {
		t.Errorf("subject: got %q", identity.Subject)
	}
	if identity.Email != "agent.owner@example.invalid" {
		t.Errorf("email: got %q, want it lowercased", identity.Email)
	}
	if !identity.EmailVerified {
		t.Error("email_verified was not read")
	}
	if identity.NotesAccess == nil || !*identity.NotesAccess {
		t.Errorf("access claim: got %v", identity.NotesAccess)
	}

	// The access claim is a tri-state on purpose: absent is not false, and both
	// are refused, but only one of them means the provider was set up wrong.
	claims = validClaims(provider.issuer, now)
	delete(claims, NotesAccessClaim)
	identity, err = verifier.Verify(context.Background(), mint(t, signing, "signing", jwt.SigningMethodRS256, claims))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if identity.NotesAccess != nil {
		t.Errorf("a token without the access claim read as %v", *identity.NotesAccess)
	}

	claims = validClaims(provider.issuer, now)
	claims[NotesAccessClaim] = "yes"
	identity, err = verifier.Verify(context.Background(), mint(t, signing, "signing", jwt.SigningMethodRS256, claims))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if identity.NotesAccess != nil {
		t.Error("an access claim that is not a boolean was read as one")
	}
}

// A key rotation at the provider must not need a restart here, and must not let
// a stream of forged key ids become a stream of requests to the provider.
func TestAnUnknownKeyIdRefreshesTheKeySetOnceAMinute(t *testing.T) {
	first, second := newRSAKey(t), newRSAKey(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	provider := newFakeProvider(t, keySet(t, publicJWK(t, "first", &first.PublicKey)))
	verifier := provider.verifier(clock)

	if _, err := verifier.Verify(context.Background(), mint(t, first, "first", jwt.SigningMethodRS256, validClaims(provider.issuer, now))); err != nil {
		t.Fatalf("the first key: %v", err)
	}
	if provider.jwks.Load() != 1 {
		t.Fatalf("the key set was read %d times, want once", provider.jwks.Load())
	}

	// The provider rotates. The next token names a key id that is not in the
	// cached set, so the set is read again and the token is accepted.
	provider.keys.Store(keySet(t,
		publicJWK(t, "first", &first.PublicKey),
		publicJWK(t, "second", &second.PublicKey)))
	if _, err := verifier.Verify(context.Background(), mint(t, second, "second", jwt.SigningMethodRS256, validClaims(provider.issuer, now))); err != nil {
		t.Fatalf("after the rotation: %v", err)
	}
	if provider.jwks.Load() != 2 {
		t.Fatalf("the key set was read %d times, want twice", provider.jwks.Load())
	}

	// A forged key id within the cooldown is refused without asking again.
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := verifier.Verify(context.Background(), mint(t, first, "forged", jwt.SigningMethodRS256, validClaims(provider.issuer, now))); !errors.Is(err, ErrRefused) {
			t.Fatalf("a forged key id: got %v", err)
		}
	}
	// Still two: the refresh that picked up the rotation is the one refresh the
	// cooldown allows in this minute, so the forged ids cost the provider
	// nothing at all.
	if provider.jwks.Load() != 2 {
		t.Fatalf("the key set was read %d times, want two — the cooldown was already spent",
			provider.jwks.Load())
	}

	// A minute later the cooldown is over and exactly one more refresh is
	// allowed, however many forged tokens arrive.
	now = now.Add(61 * time.Second)
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := verifier.Verify(context.Background(), mint(t, first, "forged", jwt.SigningMethodRS256, validClaims(provider.issuer, now))); !errors.Is(err, ErrRefused) {
			t.Fatalf("a forged key id after the cooldown: got %v", err)
		}
	}
	if provider.jwks.Load() != 3 {
		t.Fatalf("the key set was read %d times, want three", provider.jwks.Load())
	}
}

// A provider that cannot be read is not a verdict on the token: it must not be
// answered with the refusal that would send every agent back through a sign-in.
func TestAProviderThatCannotBeReadIsNotARefusal(t *testing.T) {
	signing := newRSAKey(t)
	provider := newFakeProvider(t, keySet(t, publicJWK(t, "signing", &signing.PublicKey)))
	provider.fail.Store(true)
	verifier := provider.verifier(nil)

	_, err := verifier.Verify(context.Background(), mint(t, signing, "signing", jwt.SigningMethodRS256, validClaims(provider.issuer, time.Now())))
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("got %v, want the provider reported unavailable", err)
	}
	if errors.Is(err, ErrRefused) {
		t.Error("a provider this tier could not read was read as a bad token")
	}
}

// The documents are read once an hour, not once a request.
func TestTheProvidersDocumentsAreCached(t *testing.T) {
	signing := newRSAKey(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	provider := newFakeProvider(t, keySet(t, publicJWK(t, "signing", &signing.PublicKey)))
	verifier := provider.verifier(func() time.Time { return now })

	for attempt := 0; attempt < 3; attempt++ {
		if _, err := verifier.Verify(context.Background(), mint(t, signing, "signing", jwt.SigningMethodRS256, validClaims(provider.issuer, now))); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	if provider.discovery.Load() != 1 || provider.jwks.Load() != 1 {
		t.Fatalf("read the provider %d/%d times, want once each",
			provider.discovery.Load(), provider.jwks.Load())
	}

	now = now.Add(61 * time.Minute)
	if _, err := verifier.Verify(context.Background(), mint(t, signing, "signing", jwt.SigningMethodRS256, validClaims(provider.issuer, now))); err != nil {
		t.Fatalf("after the cache expired: %v", err)
	}
	if provider.discovery.Load() != 2 || provider.jwks.Load() != 2 {
		t.Fatalf("read the provider %d/%d times, want twice each",
			provider.discovery.Load(), provider.jwks.Load())
	}
}

// Only a token claiming this provider's issuer takes this path at all.
func TestOwnsIssuerIgnoresTrailingSlashes(t *testing.T) {
	const configured = "https://sso.example.invalid/application/o/agents/"
	verifier := &Verifier{Settings: Settings{Issuer: configured, ClientID: testClientID}}
	for issuer, want := range map[string]bool{
		configured: true,
		"https://sso.example.invalid/application/o/agents": true,
		"https://sso.example.invalid/application/o/web/":   false,
		"": false,
	} {
		if got := verifier.OwnsIssuer(issuer); got != want {
			t.Errorf("%q: got %v, want %v", issuer, got, want)
		}
	}
}
