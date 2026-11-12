package oidcsignin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

const appReturnAddress = "soloclef://auth"

// memoryAppCodes is AppCodes in memory, on the harness's clock, for the
// flow tests; ValkeyAppCodes has its own test against a real Valkey below.
type memoryAppCodes struct {
	now   func() time.Time
	mutex sync.Mutex
	codes map[string]struct {
		grant  AppGrant
		issued time.Time
	}
}

func newMemoryAppCodes(now func() time.Time) *memoryAppCodes {
	return &memoryAppCodes{now: now, codes: map[string]struct {
		grant  AppGrant
		issued time.Time
	}{}}
}

func (m *memoryAppCodes) Issue(_ context.Context, grant AppGrant) (string, error) {
	code, err := newAppCode()
	if err != nil {
		return "", err
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.codes[appCodeKey(code)] = struct {
		grant  AppGrant
		issued time.Time
	}{grant, m.now()}
	return code, nil
}

func (m *memoryAppCodes) Redeem(_ context.Context, code string) (AppGrant, bool, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	held, found := m.codes[appCodeKey(code)]
	delete(m.codes, appCodeKey(code))
	if !found || !m.now().Before(held.issued.Add(appCodeLifetime)) {
		return AppGrant{}, false, nil
	}
	return held.grant, true, nil
}

// pkce is one app's verifier and its challenge.
type pkce struct{ verifier, challenge string }

func newPKCE(t *testing.T) pkce {
	t.Helper()
	verifier, err := random(48)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(verifier))
	return pkce{verifier: verifier, challenge: base64.RawURLEncoding.EncodeToString(digest[:])}
}

// appHarness is the harness with the app's return address allowed and a
// code store.
func appHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.exec(`UPDATE "user" SET is_verified = true`)
	routes := h.routes
	routes.Settings.AppRedirects = []string{appReturnAddress}
	routes.Codes = newMemoryAppCodes(func() time.Time { return h.clock })
	h.serve(routes)
	return h
}

func appStartURL(challenge string, extra url.Values) string {
	query := url.Values{"app_redirect": {appReturnAddress}, "challenge": {challenge}, "challenge_method": {"S256"}, "device": {"  iPhone 15   Pro  "}}
	for key, values := range extra {
		query[key] = values
	}
	return AppStartPath + "?" + query.Encode()
}

// appSignIn is the app's sheet: start, the provider, the callback. It
// answers where the sheet was sent back to.
func (h *harness) appSignIn(who person, challenge string) *url.URL {
	h.t.Helper()
	started := h.get(appStartURL(challenge, nil))
	cookie := cookiesOf(started)[attemptCookieName]
	if started.Code != http.StatusFound || cookie == nil {
		h.t.Fatalf("starting an app sign-in answered %d %s", started.Code, started.Body)
	}
	finished := h.get(h.provider.signIn(started.Header().Get("Location"), who), cookie)
	return h.backInApp(finished)
}

// backInApp asserts the sheet went back to the app, set no session in the
// sheet's browser and dropped the attempt, and answers the address.
func (h *harness) backInApp(finished *httptest.ResponseRecorder) *url.URL {
	h.t.Helper()
	location := finished.Header().Get("Location")
	if finished.Code != http.StatusSeeOther || !strings.HasPrefix(location, appReturnAddress+"?") {
		h.t.Fatalf("the sheet was sent to %q (%d), not back to the app", location, finished.Code)
	}
	cookies := cookiesOf(finished)
	if cookies["s_session"] != nil || cookies["s_refresh"] != nil {
		h.t.Error("an app sign-in set session cookies in the sheet's browser")
	}
	if dropped := cookies[attemptCookieName]; dropped == nil || dropped.MaxAge >= 0 {
		h.t.Error("the attempt cookie outlived the app sign-in")
	}
	address, err := url.Parse(location)
	if err != nil {
		h.t.Fatal(err)
	}
	return address
}

func (h *harness) exchange(code, verifier, device string) *httptest.ResponseRecorder {
	h.t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code, "verifier": verifier, "device_label": device})
	request := httptest.NewRequest(http.MethodPost, AppExchangePath, strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	h.api.Adapter().ServeHTTP(recorder, request)
	return recorder
}

// refusedCode asserts an exchange was refused with this status and code, in
// the message (which the app reads) and the code field alike.
func refusedCode(t *testing.T, why string, got *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(got.Body.Bytes(), &body)
	if got.Code != status || body.Error.Message != code || body.Error.Code != code {
		t.Errorf("%s: answered %d %s, want %d with %s", why, got.Code, got.Body, status, code)
	}
}

type appSessionBody struct {
	AccessToken     string  `json:"access_token"`
	RefreshToken    *string `json:"refresh_token"`
	TokenType       string  `json:"token_type"`
	AccessExpiresAt int64   `json:"access_expires_at"`
	SessionID       *string `json:"session_id"`
	EndSessionURL   *string `json:"end_session_url"`
}

// Two people sign in from the app: each code is spent once, only with its
// own app's verifier, and makes a phone session for its own account alone.
func TestEachAppSignInEndsInTheAppWithACodeOnlyItsOwnVerifierSpends(t *testing.T) {
	h := appHarness(t)
	hers, his := newPKCE(t), newPKCE(t)

	herCode := h.appSignIn(alice, hers.challenge).Query().Get("code")
	hisCode := h.appSignIn(bob, his.challenge).Query().Get("code")
	if herCode == "" || hisCode == "" || herCode == hisCode {
		t.Fatalf("codes %q and %q", herCode, hisCode)
	}
	// His code with her verifier: refused, and spent, so it is not his
	// either any more (the real app simply signs in again).
	refusedCode(t, "his code with her verifier", h.exchange(hisCode, hers.verifier, ""), http.StatusBadRequest, codeAppVerifier)
	refusedCode(t, "his code once it was spent", h.exchange(hisCode, his.verifier, ""), http.StatusBadRequest, codeAppExpired)
	refusedCode(t, "a code never issued", h.exchange("not-a-code", hers.verifier, ""), http.StatusBadRequest, codeAppExpired)

	got := h.exchange(herCode, hers.verifier, "")
	if got.Code != http.StatusOK {
		t.Fatalf("her exchange answered %d %s", got.Code, got.Body)
	}
	var session appSessionBody
	if err := json.Unmarshal(got.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	read, err := principal.SessionToken{Secret: testSecret}.Read(session.AccessToken)
	if err != nil || read.UserID != testpostgres.Alice {
		t.Fatalf("her code's session is %+v (%v)", read, err)
	}
	if session.RefreshToken == nil || session.SessionID == nil || session.TokenType != "bearer" || session.AccessExpiresAt == 0 {
		t.Fatalf("not a phone's token pair: %s", got.Body)
	}
	// The access token names the sign-in the pair belongs to, so signing
	// the phone out ends it at once.
	if read.SessionID != *session.SessionID {
		t.Errorf("the access token names sign-in %q, the pair %q", read.SessionID, *session.SessionID)
	}
	// The stub's sign-out page is plain http, so it is not handed on.
	if session.EndSessionURL != nil {
		t.Errorf("a sign-out page that is not https was handed to the app: %q", *session.EndSessionURL)
	}
	// A phone sign-in, under the name the sheet started with, on her
	// account; nothing for his.
	if n := h.count(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND family_id = $2 AND kind = 'app' AND device_label = 'iPhone 15 Pro'`,
		testpostgres.Alice, *session.SessionID); n != 1 {
		t.Errorf("%d phone sign-ins for her under the sheet's device name", n)
	}
	if n := h.count(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1`, testpostgres.Bob); n != 0 {
		t.Errorf("his refused exchanges made %d sign-ins", n)
	}
	refusedCode(t, "her code a second time", h.exchange(herCode, hers.verifier, ""), http.StatusBadRequest, codeAppExpired)

	// He signs in again and his own exchange works, naming the device the
	// app gives at the exchange.
	hisCode = h.appSignIn(bob, his.challenge).Query().Get("code")
	got = h.exchange(hisCode, his.verifier, "Pixel 9")
	if err := json.Unmarshal(got.Body.Bytes(), &session); err != nil || got.Code != http.StatusOK {
		t.Fatalf("his exchange answered %d %s", got.Code, got.Body)
	}
	if read, _ := (principal.SessionToken{Secret: testSecret}).Read(session.AccessToken); read.UserID != testpostgres.Bob {
		t.Fatalf("his code's session is %+v", read)
	}
	if n := h.count(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND device_label = 'Pixel 9' AND kind = 'app'`, testpostgres.Bob); n != 1 {
		t.Error("his sign-in is not under the name the app gave")
	}

	// A minute on, a code is no longer spendable.
	late := h.appSignIn(alice, hers.challenge).Query().Get("code")
	h.clock = h.clock.Add(appCodeLifetime)
	refusedCode(t, "a code a minute old", h.exchange(late, hers.verifier, ""), http.StatusBadRequest, codeAppExpired)
}

// What an app sign-in is refused for, and where each refusal goes.
func TestAnAppSignInIsRefusedBackToTheAppOrHereWhenThereIsNowhereSafe(t *testing.T) {
	h := appHarness(t)
	hers := newPKCE(t)

	// A return address not on the list is answered here: there is nowhere
	// safe to send even an error.
	for _, address := range []string{"", "evil://auth", "soloclef://auth/../x", "https://notes.example/auth"} {
		got := h.get(AppStartPath + "?" + url.Values{"app_redirect": {address}, "challenge": {hers.challenge}}.Encode())
		refusedCode(t, "the return address "+address, got, http.StatusBadRequest, codeAppRedirect)
		if len(got.Result().Cookies()) != 0 || got.Header().Get("Location") != "" {
			t.Errorf("%q: a refused start set a cookie or redirected", address)
		}
	}
	// A challenge that is not S256 goes back to the app with the reason.
	for _, query := range []url.Values{{"challenge": {"short"}}, {"challenge_method": {"plain"}}} {
		back := h.backInApp(h.get(appStartURL(hers.challenge, query)))
		if back.Query().Get("error") != codeAppBadPKCE {
			t.Errorf("%v: back in the app with %q", query, back.RawQuery)
		}
	}
	// "Use a different account" asks the provider for its account chooser,
	// and never for prompt=login.
	started := h.get(appStartURL(hers.challenge, url.Values{"choose_account": {"1"}}))
	authorization, _ := url.Parse(started.Header().Get("Location"))
	if authorization.Query().Get("prompt") != "select_account" {
		t.Errorf("choose_account asked for prompt %q", authorization.Query().Get("prompt"))
	}
	plain := h.get(appStartURL(hers.challenge, nil))
	if address, _ := url.Parse(plain.Header().Get("Location")); address.Query().Has("prompt") {
		t.Error("a sign-in that did not ask to choose an account sent a prompt")
	}

	// The provider's refusal, and a callback whose state is not the
	// attempt's, go back to the app that asked.
	cookie := cookiesOf(plain)[attemptCookieName]
	if back := h.backInApp(h.get(CallbackPath+"?error=access_denied&state=x", cookie)); back.Query().Get("error") != "access_denied" {
		t.Errorf("the provider's refusal came back as %q", back.RawQuery)
	}
	if back := h.backInApp(h.get(CallbackPath+"?code=x&state=not-its-state", cookie)); back.Query().Get("error") != codeInvalidState {
		t.Errorf("a callback with another state came back as %q", back.RawQuery)
	}

	// The provider being down, for a replica that has not read its
	// discovery document yet, is said in the app.
	fresh := h.routes
	fresh.Verifier = NewVerifier(h.provider.issuer, testClientID, "authentik")
	clock := func() time.Time { return h.clock }
	fresh.Verifier.Now, fresh.Verifier.Directory.Now = clock, clock
	h.serve(fresh)
	h.provider.mutex.Lock()
	h.provider.down = true
	h.provider.mutex.Unlock()
	if back := h.backInApp(h.get(appStartURL(hers.challenge, nil))); back.Query().Get("error") != codeUnavailable {
		t.Errorf("a provider that is down came back as %q", back.RawQuery)
	}
	h.provider.mutex.Lock()
	h.provider.down = false
	h.provider.mutex.Unlock()

	// An account deactivated between the callback and the exchange is not
	// signed in.
	code := h.appSignIn(alice, hers.challenge).Query().Get("code")
	h.exec(`UPDATE "user" SET is_active = false WHERE id = $1`, testpostgres.Alice)
	refusedCode(t, "a deactivated account", h.exchange(code, hers.verifier, ""), http.StatusUnauthorized, codeInactive)
}

// With single sign-on off the exchange is not there either.
func TestTheAppExchangeIsNotFoundWhereSingleSignOnIsOff(t *testing.T) {
	h := newHarness(t)
	h.serve(Routes{Errors: h.routes.Errors, Logger: h.routes.Logger})
	got := h.exchange("x", strings.Repeat("v", 43), "")
	if got.Code != http.StatusNotFound {
		t.Errorf("answered %d %s", got.Code, got.Body)
	}
}

// The codes in a real Valkey: spent once, and gone after their minute.
// SOLOCLEF_API_TEST_OIDCSIGNIN_VALKEY names a throwaway Valkey.
func TestValkeyAppCodesAreSpentOnce(t *testing.T) {
	address := os.Getenv("SOLOCLEF_API_TEST_OIDCSIGNIN_VALKEY")
	if address == "" {
		t.Skip("SOLOCLEF_API_TEST_OIDCSIGNIN_VALKEY is not set; this test needs a throwaway Valkey")
	}
	codes, err := NewValkeyAppCodes(address)
	if err != nil {
		t.Fatal(err)
	}
	defer codes.Close()
	ctx := context.Background()
	grant := AppGrant{UserID: testpostgres.Alice, Challenge: strings.Repeat("c", 43), Device: "iPhone"}
	code, err := codes.Issue(ctx, grant)
	if err != nil {
		t.Fatal(err)
	}
	client, _ := codes.connection()
	if ttl, err := client.Do(ctx, client.B().Ttl().Key(appCodeKey(code)).Build()).AsInt64(); err != nil || ttl <= 0 || ttl > 60 {
		t.Errorf("the code's lifetime is %d s (%v)", ttl, err)
	}
	if exists, _ := client.Do(ctx, client.B().Exists().Key("soloclef:app_sign_in:"+code).Build()).AsInt64(); exists != 0 {
		t.Error("the code is stored under itself, not its hash")
	}
	got, found, err := codes.Redeem(ctx, code)
	if err != nil || !found || got != grant {
		t.Fatalf("the first redeem answered %+v %v %v", got, found, err)
	}
	if _, found, err := codes.Redeem(ctx, code); err != nil || found {
		t.Errorf("a second redeem found the code (%v)", err)
	}
}
