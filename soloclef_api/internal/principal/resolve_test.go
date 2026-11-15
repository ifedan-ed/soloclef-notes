package principal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

type fakeDirectory struct {
	activeUsers map[string]bool
	// endedSignIns are sign-ins that were ended: signed out, or every one
	// of the account's on a new password.
	endedSignIns map[string]bool
	tokens       map[string]PersonalAccessTokenOwner
	failure      error
	touched      []int64
	// asked is every sign-in the gate asked about, as "user/sign-in@unix".
	asked []string
}

func (d *fakeDirectory) SessionIsLive(_ context.Context, userID, sessionID string, now time.Time) (bool, error) {
	d.asked = append(d.asked, userID+"/"+sessionID+"@"+strconv.FormatInt(now.Unix(), 10))
	if d.failure != nil {
		return false, d.failure
	}
	return d.activeUsers[userID] && !d.endedSignIns[sessionID], nil
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
	request := httptest.NewRequest(http.MethodGet, "/api/v1/logs/summary", nil)
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
			HashToken(livePersonalAccessToken): {TokenID: 7, UserID: testSubject, APIAccessEnabled: true},
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
		TokenID: 7, UserID: testSubject, LastUsedAt: time.Now(), APIAccessEnabled: true,
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

// Nothing here accepts an identity provider's token (an agent connects with
// an API key), so it is a 401 — and not a fall-through to the session cookie
// sent beside it, which would answer as an account the token never named.
func TestAnIdentityProvidersTokenIsUnauthorized(t *testing.T) {
	_, err := resolverFor(directoryWithLiveKey()).Resolve(context.Background(),
		requestWith("Bearer "+tokenForeignIssuer, tokenValid))
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("got %v, want ErrUnauthorized", err)
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

// The account's one access control. The switch is off for a new account, and
// while it is off no key acts for the account, however valid it is.
func TestAKeyOfAnAccountWithAPIAccessOffIsRefused(t *testing.T) {
	directory := directoryWithLiveKey()
	directory.tokens[HashToken(livePersonalAccessToken)] = PersonalAccessTokenOwner{TokenID: 7, UserID: testSubject}

	// The valid session cookie beside the key must not rescue the request:
	// the caller presented a key, and the key does not work.
	_, err := resolverFor(directory).Resolve(context.Background(),
		requestWith("Bearer "+livePersonalAccessToken, tokenValid))
	var refusal Refusal
	if !errors.As(err, &refusal) || refusal != ErrAPIAccessOff {
		t.Fatalf("got %v, want the API-access refusal", err)
	}
	if refusal.Status != http.StatusForbidden || refusal.Message != "API access is not enabled for this account." {
		t.Errorf("the refusal is %d %q", refusal.Status, refusal.Message)
	}
	if len(directory.touched) != 0 {
		t.Errorf("a refused key was recorded as used: %v", directory.touched)
	}
}

// The switch is about programs. The person's own session is never asked it.
func TestASessionIsNotAskedTheAPIAccessSwitch(t *testing.T) {
	directory := &fakeDirectory{activeUsers: map[string]bool{testSubject: true}}
	caller, err := resolverFor(directory).Resolve(context.Background(), requestWith("", tokenValid))
	if err != nil || caller.Method != MethodSession {
		t.Fatalf("got %v, %v; want the session", caller, err)
	}
}

// The sign-in the token names is asked about on every request, with the
// resolver's clock, by the cookie and the bearer alike; an ended one is no
// principal. A bearer of an ended sign-in falls through to the cookie, as
// every refused bearer does, and the cookie is held to the same check.
func TestAnEndedSignInIsNoPrincipal(t *testing.T) {
	directory := &fakeDirectory{activeUsers: map[string]bool{testSubject: true}, endedSignIns: map[string]bool{}}
	resolver := resolverFor(directory)
	at := time.Unix(1762003600, 0)
	resolver.Now = func() time.Time { return at }

	if caller, err := resolver.Resolve(context.Background(), requestWith("Bearer "+tokenValid, "")); err != nil || caller.UserID != testSubject {
		t.Fatalf("a live sign-in resolved to %+v, %v", caller, err)
	}
	if want := testSubject + "/" + testSignIn + "@1762003600"; len(directory.asked) != 1 || directory.asked[0] != want {
		t.Errorf("the directory was asked %v, want [%s]", directory.asked, want)
	}

	directory.endedSignIns[testSignIn] = true
	for name, request := range map[string]*http.Request{
		"as a bearer":   requestWith("Bearer "+tokenValid, ""),
		"as the cookie": requestWith("", tokenValid),
		"as both":       requestWith("Bearer "+tokenValid, tokenValid),
	} {
		if caller, err := resolver.Resolve(context.Background(), request); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%s: an ended sign-in resolved to %+v, %v", name, caller, err)
		}
	}

	// Another sign-in of the same account, in the cookie, still works.
	other := signedToken(testSecret, map[string]any{"sid": "9c8b7a6f-5e4d-4c3b-8a29-1f0e0d0c0b0a"})
	if caller, err := resolver.Resolve(context.Background(), requestWith("Bearer "+tokenValid, other)); err != nil || caller.UserID != testSubject {
		t.Errorf("the account's other sign-in resolved to %+v, %v", caller, err)
	}
}

// A token that names no sign-in is refused before the database is asked:
// there is nothing to ask about.
func TestATokenNamingNoSignInIsUnauthorized(t *testing.T) {
	directory := &fakeDirectory{activeUsers: map[string]bool{testSubject: true}}
	for name, request := range map[string]*http.Request{
		"as a bearer":   requestWith("Bearer "+tokenWithoutSignIn, ""),
		"as the cookie": requestWith("", tokenWithoutSignIn),
	} {
		if caller, err := resolverFor(directory).Resolve(context.Background(), request); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%s: resolved to %+v, %v", name, caller, err)
		}
	}
	if len(directory.asked) != 0 {
		t.Errorf("the directory was asked %v about a token naming no sign-in", directory.asked)
	}
}
