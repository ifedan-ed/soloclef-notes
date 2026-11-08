package principal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeDirectory struct {
	activeUsers map[string]bool
	tokens      map[string]PersonalAccessTokenOwner
	failure     error
	touched     []int64
}

func (d *fakeDirectory) UserIsActive(_ context.Context, userID string) (bool, error) {
	if d.failure != nil {
		return false, d.failure
	}
	return d.activeUsers[userID], nil
}

func (d *fakeDirectory) PersonalAccessTokenOwner(
	_ context.Context, tokenHash string, _ time.Time,
) (PersonalAccessTokenOwner, bool, error) {
	if d.failure != nil {
		return PersonalAccessTokenOwner{}, false, d.failure
	}
	owner, found := d.tokens[tokenHash]
	return owner, found, nil
}

func (d *fakeDirectory) TouchPersonalAccessToken(tokenID int64) {
	d.touched = append(d.touched, tokenID)
}

const (
	livePersonalAccessToken = TokenPrefix + "a-live-key"
	deadPersonalAccessToken = TokenPrefix + "a-revoked-key"
)

func resolverFor(directory Directory) Resolver {
	return Resolver{
		SessionTokens:     SessionToken{Secret: testSecret},
		Directory:         directory,
		SessionCookieName: "surfsense_session",
	}
}

func requestWith(header, cookie string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/logs/workspaces/1/summary", nil)
	if header != "" {
		request.Header.Set("Authorization", header)
	}
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: "surfsense_session", Value: cookie})
	}
	return request
}

func directoryWithLiveKey() *fakeDirectory {
	return &fakeDirectory{
		activeUsers: map[string]bool{testSubject: true},
		tokens: map[string]PersonalAccessTokenOwner{
			HashToken(livePersonalAccessToken): {TokenID: 7, UserID: testSubject},
		},
	}
}

func TestAnAPIKeyResolvesToAGatedPrincipal(t *testing.T) {
	directory := directoryWithLiveKey()

	caller, err := resolverFor(directory).Resolve(context.Background(),
		requestWith("Bearer "+livePersonalAccessToken, ""))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if caller.Method != MethodPersonalAccessToken || !caller.IsGated() {
		t.Errorf("got %+v, want a gated API-key principal", caller)
	}
	if len(directory.touched) != 1 || directory.touched[0] != 7 {
		t.Errorf("last_used_at was not recorded: %v", directory.touched)
	}
}

func TestAnAPIKeySentWithoutTheBearerSchemeStillResolves(t *testing.T) {
	// get_auth_context takes the whole header when the scheme is not bearer,
	// so a client that sends the key on its own keeps working.
	caller, err := resolverFor(directoryWithLiveKey()).Resolve(context.Background(),
		requestWith(livePersonalAccessToken, ""))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if caller.Method != MethodPersonalAccessToken {
		t.Errorf("got %+v", caller)
	}
}

func TestARecentlyUsedKeyIsNotTouchedAgain(t *testing.T) {
	directory := directoryWithLiveKey()
	directory.tokens[HashToken(livePersonalAccessToken)] = PersonalAccessTokenOwner{
		TokenID: 7, UserID: testSubject, LastUsedAt: time.Now(),
	}

	if _, err := resolverFor(directory).Resolve(context.Background(),
		requestWith("Bearer "+livePersonalAccessToken, "")); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(directory.touched) != 0 {
		t.Errorf("a key used minutes ago was written again: %v", directory.touched)
	}
}

func TestAKeyThatDoesNotResolveFallsThroughToTheSessionCookie(t *testing.T) {
	// The Python resolution does not stop at a dead key; it keeps going.
	caller, err := resolverFor(directoryWithLiveKey()).Resolve(context.Background(),
		requestWith("Bearer "+deadPersonalAccessToken, tokenValid))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if caller.Method != MethodSession || caller.UserID != testSubject {
		t.Errorf("got %+v, want the cookie's session principal", caller)
	}
}

func TestABearerTokenForAnInactiveAccountFallsThroughToTheCookie(t *testing.T) {
	directory := &fakeDirectory{activeUsers: map[string]bool{}}

	_, err := resolverFor(directory).Resolve(context.Background(), requestWith("Bearer "+tokenValid, tokenValid))
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("an inactive account must end as 401, got %v", err)
	}

	directory.activeUsers[testSubject] = true
	caller, err := resolverFor(directory).Resolve(context.Background(), requestWith("", tokenValid))
	if err != nil || caller.Method != MethodSession {
		t.Errorf("got %+v, %v", caller, err)
	}
}

func TestAnIdentityProvidersTokenIsLeftToThePythonTier(t *testing.T) {
	_, err := resolverFor(directoryWithLiveKey()).Resolve(context.Background(),
		requestWith("Bearer "+tokenForeignIssuer, ""))
	if !errors.Is(err, ErrServedByPythonTier) {
		t.Errorf("got %v, want ErrServedByPythonTier", err)
	}
}

func TestNoCredentialIsUnauthorized(t *testing.T) {
	for name, request := range map[string]*http.Request{
		"nothing":            requestWith("", ""),
		"junk bearer":        requestWith("Bearer nonsense", ""),
		"junk cookie":        requestWith("", "nonsense"),
		"an expired session": requestWith("Bearer "+tokenExpired, tokenExpired),
	} {
		if _, err := resolverFor(directoryWithLiveKey()).Resolve(context.Background(), request); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%s: got %v, want ErrUnauthorized", name, err)
		}
	}
}

func TestADatabaseFailureIsNotMistakenForARefusal(t *testing.T) {
	failure := errors.New("connection refused")
	directory := &fakeDirectory{failure: failure}

	_, err := resolverFor(directory).Resolve(context.Background(), requestWith("Bearer "+tokenValid, ""))
	if !errors.Is(err, failure) {
		t.Errorf("got %v, want the database failure", err)
	}
}

// foreignIssuer is the issuer tokenForeignIssuer claims.
const foreignIssuer = "https://sso.example.com/application/o/app/"

// fakeAgents is an agent verifier that owns one issuer, so the branch can be
// tested without a provider.
type fakeAgents struct {
	issuer  string
	account string
	err     error
	asked   int
}

func (a *fakeAgents) OwnsIssuer(issuer string) bool { return issuer == a.issuer }

func (a *fakeAgents) Resolve(context.Context, string) (string, error) {
	a.asked++
	return a.account, a.err
}

// An agent's token no longer costs a second hop: when the verifier owns the
// issuer it is resolved here, and when it does not the request still goes to the
// tier that can check it.
func TestAnAgentsTokenIsVerifiedHereWhenThisTierOwnsTheIssuer(t *testing.T) {
	agents := &fakeAgents{
		issuer:  foreignIssuer,
		account: "11111111-1111-1111-1111-111111111111",
	}
	resolver := resolverFor(directoryWithLiveKey())
	resolver.Agents = agents

	caller, err := resolver.Resolve(context.Background(), requestWith("Bearer "+tokenForeignIssuer, ""))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if caller.UserID != agents.account || caller.Method != MethodAgent {
		t.Errorf("got %v, want the agent's account", caller)
	}
	if !caller.IsGated() {
		t.Error("an agent sees every workspace; it must be gated like an API key")
	}

	// A token from some other issuer is still the Python tier's to judge.
	agents.issuer = "https://elsewhere.example.invalid/"
	if _, err := resolver.Resolve(context.Background(), requestWith("Bearer "+tokenForeignIssuer, "")); !errors.Is(err, ErrServedByPythonTier) {
		t.Errorf("got %v, want ErrServedByPythonTier", err)
	}
}

// A refusal the verifier reached is answered here, in its own status and words,
// and never falls through to the session check or to the Python tier.
func TestAnAgentsRefusalIsNotAFallThrough(t *testing.T) {
	refusal := Refusal{Status: http.StatusForbidden, Message: "This sign-in may not use Soloclef Notes"}
	resolver := resolverFor(directoryWithLiveKey())
	resolver.Agents = &fakeAgents{issuer: foreignIssuer, err: refusal}

	_, err := resolver.Resolve(context.Background(),
		requestWith("Bearer "+tokenForeignIssuer, tokenValid))
	var got Refusal
	if !errors.As(err, &got) || got != refusal {
		t.Fatalf("got %v, want the verifier's own refusal", err)
	}
}
