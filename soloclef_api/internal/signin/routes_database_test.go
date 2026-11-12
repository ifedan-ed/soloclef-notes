package signin

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres for this package's database
// tests. **Every account in it is deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_SIGNIN_DATABASE"

const (
	secret        = "a-test-secret"
	alicePassword = "correct horse battery staple"
	bobPassword   = "a different passphrase"
)

// harness is the routes over a real database, with a clock the test moves.
type harness struct {
	t     *testing.T
	api   huma.API
	clock time.Time
	exec  func(sql string, args ...any)
	pool  *pgxpool.Pool
	// routes is what h.api serves, and mail everything it was asked to send.
	routes Routes
	mail   *recordingMailer
}

func newHarness(t *testing.T) (*harness, func(sql string, args ...any) int) {
	t.Helper()
	pool := testpostgres.Open(t, databaseVariable)
	bobHash, err := NewHasher(1).Hash(context.Background(), bobPassword)
	if err != nil {
		t.Fatal(err)
	}
	// Alice's hash is the one the Python tier made (passwords_test.go).
	if _, err := pool.Exec(context.Background(),
		`UPDATE "user" SET hashed_password = CASE id WHEN $1::uuid THEN $2 ELSE $3 END`,
		testpostgres.Alice, pythonHash, bobHash); err != nil {
		t.Fatal(err)
	}
	// The clock starts at the real time: the gate checks a minted token's expiry against the wall
	// clock, so a fixed date made every test here fail once its tokens' hour had passed. Whole
	// seconds, so a time written to Postgres (microseconds) reads back equal.
	h := &harness{t: t, clock: time.Now().UTC().Truncate(time.Second), pool: pool}
	h.api = humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	h.mail = &recordingMailer{}
	h.routes = Routes{
		Pool:   pool,
		Secret: secret,
		Cookies: CookiePolicy{SessionName: "s_session", RefreshName: "s_refresh",
			SameSite: "lax", SecurePolicy: "auto"},
		Hasher: NewHasher(2),
		Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return h.clock },
		// The deployment these tests describe lets people sign up and
		// sends mail; a test of the other deployment registers its own
		// routes from this value (h.variant).
		SignupAllowed: true,
		Mail:          h.mail,
		FrontendURL:   "https://notes.example/",
		BrandName:     "Example Notes",
		// Inline, so a test reads the mail as soon as the answer is back.
		Background: func(task func()) { task() },
	}
	h.routes.Register(h.api)
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	h.exec = exec
	return h, count
}

func (h *harness) do(request *http.Request) *httptest.ResponseRecorder {
	h.t.Helper()
	recorder := httptest.NewRecorder()
	h.api.Adapter().ServeHTTP(recorder, request)
	return recorder
}

func (h *harness) webSignIn(address, password string) *httptest.ResponseRecorder {
	form := url.Values{"username": {address}, "password": {password}, "grant_type": {"password"}}
	request := httptest.NewRequest(http.MethodPost, PasswordPath, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-Forwarded-Proto", "https")
	return h.do(request)
}

func (h *harness) phoneSignIn(address, password, device string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"email": address, "password": password, "device_label": device})
	request := httptest.NewRequest(http.MethodPost, PhonePath, strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	return h.do(request)
}

func (h *harness) bodyRefresh(path, token string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"refresh_token": token})
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	return h.do(request)
}

func pairOf(t *testing.T, recorder *httptest.ResponseRecorder) TokenPair {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("answered %d %s", recorder.Code, recorder.Body)
	}
	var pair TokenPair
	if err := json.Unmarshal(recorder.Body.Bytes(), &pair); err != nil {
		t.Fatal(err)
	}
	return pair
}

func userOf(t *testing.T, access string) string {
	t.Helper()
	session, err := principal.SessionToken{Secret: secret}.Read(access)
	if err != nil {
		t.Fatalf("the gate refused the minted token: %v", err)
	}
	return session.UserID
}

func cookiesOf(recorder *httptest.ResponseRecorder) map[string]*http.Cookie {
	out := map[string]*http.Cookie{}
	for _, c := range recorder.Result().Cookies() {
		out[c.Name] = c
	}
	return out
}

func TestTheWebSignInSetsTwoHttpOnlyCookiesForTheRightAccount(t *testing.T) {
	h, count := newHarness(t)
	answer := h.webSignIn("  ALICE@example.test ", alicePassword)
	if answer.Code != http.StatusOK {
		t.Fatalf("answered %d %s", answer.Code, answer.Body)
	}
	var body cookieSession
	_ = json.Unmarshal(answer.Body.Bytes(), &body)
	if !body.Authenticated || body.AccessExpiresAt != h.clock.Add(time.Hour).Unix() {
		t.Errorf("body %s", answer.Body)
	}
	cookies := cookiesOf(answer)
	session, refresh := cookies["s_session"], cookies["s_refresh"]
	if session == nil || refresh == nil {
		t.Fatalf("cookies %v", answer.Header().Values("Set-Cookie"))
	}
	for _, c := range []*http.Cookie{session, refresh} {
		if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
			t.Errorf("cookie %s is %+v", c.Name, c)
		}
	}
	if userOf(t, session.Value) != testpostgres.Alice {
		t.Error("the session names another account")
	}
	if n := count(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND kind = 'web' AND token_hash = $2`,
		testpostgres.Alice, hashToken(refresh.Value)); n != 1 {
		t.Errorf("%d web tokens of hers hold the cookie's hash", n)
	}
	if n := count(`SELECT count(*) FROM "user" WHERE id = $1 AND last_login IS NOT NULL`, testpostgres.Alice); n != 1 {
		t.Error("the sign-in was not recorded")
	}
}

// Every refusal is one answer: a stranger cannot tell an address with an
// account from one without, and one account's password opens no other.
func TestEveryRefusedSignInIsTheSameAnswer(t *testing.T) {
	h, count := newHarness(t)
	h.exec(`INSERT INTO "user" (id, email, hashed_password, is_active) VALUES
 ('33333333-3333-4333-8333-333333333333', 'gone@example.test', $1, false),
 ('44444444-4444-4444-8444-444444444444', 'sso@example.test', NULL, true)`, pythonHash)
	for _, c := range []struct{ name, address, password string }{
		{"a wrong password", "alice@example.test", "wrong"},
		{"another account's password", "alice@example.test", bobPassword},
		{"no such address", "nobody@example.test", alicePassword},
		{"a deactivated account", "gone@example.test", alicePassword},
		{"an account with no password", "sso@example.test", alicePassword},
		{"no password at all", "alice@example.test", ""},
	} {
		for _, answer := range []*httptest.ResponseRecorder{h.webSignIn(c.address, c.password), h.phoneSignIn(c.address, c.password, "phone")} {
			if answer.Code != http.StatusBadRequest || !strings.Contains(answer.Body.String(), `"message":"LOGIN_BAD_CREDENTIALS"`) {
				t.Errorf("%s: %d %s", c.name, answer.Code, answer.Body)
			}
			if len(answer.Result().Cookies()) != 0 {
				t.Errorf("%s: a refusal set cookies", c.name)
			}
		}
	}
	if n := count(`SELECT count(*) FROM refresh_tokens`); n != 0 {
		t.Errorf("%d sign-ins were recorded for refusals", n)
	}
}

func TestThePhoneSignInIsAYearLongSignInNamedForThePhone(t *testing.T) {
	h, count := newHarness(t)
	pair := pairOf(t, h.phoneSignIn("bob@example.test", bobPassword, "Pixel 9 · Soloclef app"))
	if pair.TokenType != "bearer" || pair.RefreshToken == nil || pair.SessionID == nil ||
		pair.AccessExpiresAt != h.clock.Add(time.Hour).Unix() || userOf(t, pair.AccessToken) != testpostgres.Bob {
		t.Fatalf("pair %+v", pair)
	}
	if n := count(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND kind = 'app'
 AND device_label = 'Pixel 9 · Soloclef app' AND family_id = $2 AND absolute_expiry = $3`,
		testpostgres.Bob, *pair.SessionID, h.clock.Add(phoneLifetime)); n != 1 {
		t.Errorf("%d phone sign-ins recorded as asked", n)
	}
}

// The refresh, through a stolen token: a refresh rotates, a racing second
// use within the grace window gets an access token only, and a later use of
// the spent token ends the whole sign-in -- the thief's copy and the real
// device's successor alike -- along with the phone's pushes. The other
// account's sign-in, made beside it, is untouched by all of it.
func TestARefreshRotatesAndASpentTokenUsedLaterEndsOnlyThatSignIn(t *testing.T) {
	h, count := newHarness(t)
	bob := pairOf(t, h.phoneSignIn("bob@example.test", bobPassword, "Bob's phone"))
	alice := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's phone"))
	h.exec(`INSERT INTO push_devices (user_id, token, platform, session_family_id) VALUES
 ($1, 'ExponentPushToken[bob]', 'ios', $2), ($3, 'ExponentPushToken[alice]', 'ios', $4)`,
		testpostgres.Bob, *bob.SessionID, testpostgres.Alice, *alice.SessionID)

	h.clock = h.clock.Add(10 * time.Minute)
	next := pairOf(t, h.bodyRefresh(RefreshPath, *bob.RefreshToken))
	if next.RefreshToken == nil || *next.RefreshToken == *bob.RefreshToken || userOf(t, next.AccessToken) != testpostgres.Bob {
		t.Fatalf("the refresh answered %+v", next)
	}

	h.clock = h.clock.Add(20 * time.Second)
	raced := pairOf(t, h.bodyRefresh(RefreshPath, *bob.RefreshToken))
	if raced.RefreshToken != nil || userOf(t, raced.AccessToken) != testpostgres.Bob {
		t.Fatalf("the use inside the grace window answered %+v", raced)
	}

	h.clock = h.clock.Add(time.Minute)
	if answer := h.bodyRefresh(RefreshPath, *bob.RefreshToken); answer.Code != http.StatusUnauthorized {
		t.Fatalf("the spent token used later answered %d %s", answer.Code, answer.Body)
	}
	if answer := h.bodyRefresh(RefreshPath, *next.RefreshToken); answer.Code != http.StatusUnauthorized {
		t.Fatalf("the successor still works after the theft: %d", answer.Code)
	}
	if n := count(`SELECT count(*) FROM push_devices WHERE user_id = $1`, testpostgres.Bob); n != 0 {
		t.Error("his phone still gets pushes after its sign-in was ended")
	}
	if n := count(`SELECT count(*) FROM push_devices WHERE user_id = $1`, testpostgres.Alice); n != 1 {
		t.Error("ending his sign-in took her phone's pushes")
	}
	if theirs := pairOf(t, h.bodyRefresh(RefreshPath, *alice.RefreshToken)); userOf(t, theirs.AccessToken) != testpostgres.Alice {
		t.Error("her refresh answered for another account")
	}
}

func TestABrowserRefreshesThroughItsCookieAndIsAnsweredWithCookies(t *testing.T) {
	h, _ := newHarness(t)
	signedIn := cookiesOf(h.webSignIn("alice@example.test", alicePassword))
	h.clock = h.clock.Add(2 * time.Hour)
	request := httptest.NewRequest(http.MethodPost, RefreshPath, strings.NewReader(`{"refresh_token":"ignored"}`))
	request.AddCookie(signedIn["s_refresh"])
	answer := h.do(request)
	if answer.Code != http.StatusOK || !strings.Contains(answer.Body.String(), `"authenticated":true`) {
		t.Fatalf("answered %d %s", answer.Code, answer.Body)
	}
	cookies := cookiesOf(answer)
	if cookies["s_refresh"] == nil || cookies["s_refresh"].Value == signedIn["s_refresh"].Value ||
		userOf(t, cookies["s_session"].Value) != testpostgres.Alice {
		t.Fatalf("cookies %v", answer.Header().Values("Set-Cookie"))
	}
}

func TestARefreshOutlivesNeitherItsSignInNorItsAccount(t *testing.T) {
	h, _ := newHarness(t)
	web := cookiesOf(h.webSignIn("alice@example.test", alicePassword))["s_refresh"].Value
	phone := pairOf(t, h.phoneSignIn("bob@example.test", bobPassword, "phone"))

	// A browser left unused past its idle time.
	h.clock = h.clock.Add(webIdle + time.Minute)
	if answer := h.bodyRefresh(RefreshPath, web); answer.Code != http.StatusUnauthorized {
		t.Errorf("an idle browser refreshed: %d", answer.Code)
	}
	// A phone has no idle limit...
	still := pairOf(t, h.bodyRefresh(RefreshPath, *phone.RefreshToken))
	// ...but a deactivated account cannot refresh at all.
	h.exec(`UPDATE "user" SET is_active = false WHERE id = $1`, testpostgres.Bob)
	if answer := h.bodyRefresh(RefreshPath, *still.RefreshToken); answer.Code != http.StatusUnauthorized {
		t.Errorf("a deactivated account refreshed: %d", answer.Code)
	}
	if answer := h.bodyRefresh(RefreshPath, "never-issued"); answer.Code != http.StatusUnauthorized {
		t.Errorf("a token never issued answered %d", answer.Code)
	}
}

func TestSigningOutEndsOnlyTheDeviceItIsSentFrom(t *testing.T) {
	h, count := newHarness(t)
	first := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "first"))
	second := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "second"))
	bob := pairOf(t, h.phoneSignIn("bob@example.test", bobPassword, "his"))

	answer := h.bodyRefresh(RevokePath, *first.RefreshToken)
	if answer.Code != http.StatusOK {
		t.Fatalf("sign-out answered %d %s", answer.Code, answer.Body)
	}
	for _, c := range answer.Result().Cookies() {
		if c.MaxAge >= 0 {
			t.Errorf("cookie %s was not cleared: %+v", c.Name, c)
		}
	}
	if code := h.bodyRefresh(RefreshPath, *first.RefreshToken).Code; code != http.StatusUnauthorized {
		t.Errorf("the signed-out device refreshed: %d", code)
	}
	pairOf(t, h.bodyRefresh(RefreshPath, *second.RefreshToken))
	pairOf(t, h.bodyRefresh(RefreshPath, *bob.RefreshToken))
	// A token that names nothing signs out just the same.
	if code := h.bodyRefresh(RevokePath, "never-issued").Code; code != http.StatusOK {
		t.Errorf("signing out with an unknown token answered %d", code)
	}
	if n := count(`SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL AND user_id = $1`, testpostgres.Bob); n != 1 {
		t.Errorf("his sign-in has %d live tokens", n)
	}
}

func TestTheSessionReadSaysWhenTheCallersAccessTokenExpires(t *testing.T) {
	h, _ := newHarness(t)
	pair := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "phone"))
	ask := func(caller principal.Principal, bearer string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, SessionPath, nil)
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		return h.do(request.WithContext(principal.WithPrincipal(request.Context(), caller)))
	}
	session := ask(principal.Principal{UserID: testpostgres.Alice, Method: principal.MethodSession}, pair.AccessToken)
	if want := `"access_expires_at":` + jsonNumber(pair.AccessExpiresAt); session.Code != http.StatusOK || !strings.Contains(session.Body.String(), want) {
		t.Errorf("a session answered %d %s", session.Code, session.Body)
	}
	key := ask(principal.Principal{UserID: testpostgres.Alice, Method: principal.MethodPersonalAccessToken}, "ss_pat_x")
	if key.Code != http.StatusOK || !strings.Contains(key.Body.String(), `"access_expires_at":null`) {
		t.Errorf("an API key answered %d %s", key.Code, key.Body)
	}
}

func jsonNumber(n int64) string {
	encoded, _ := json.Marshal(n)
	return string(encoded)
}
