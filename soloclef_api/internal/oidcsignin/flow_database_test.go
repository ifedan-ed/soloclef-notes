package oidcsignin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/signin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres for this package's database
// tests. **Every account in it is deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_OIDCSIGNIN_DATABASE"

const (
	testSecret       = "a-test-secret"
	testClientID     = "notes-client"
	testClientSecret = "s3cr:et%/+ with every awkward character"
	frontend         = "https://notes.example"
	redirectURL      = "https://notes.example/auth/oidc/callback"
	issuerPath       = "/application/o/notes/"
)

// person is somebody with an identity at the provider.
type person struct {
	subject       string
	email         string
	emailVerified bool
	name          string
}

// grant is what a code issued by the stub stands for.
type grant struct {
	who       person
	challenge string
	nonce     string
}

// stubProvider is an OpenID Connect provider small enough to read: a
// discovery document, a key set with one real RSA key, an authorization
// step the test walks by hand (signIn), and a token endpoint that checks
// the client's secret, the redirect address and the PKCE verifier before it
// signs an ID token. Nothing in it is a real key, issuer or account.
type stubProvider struct {
	t      *testing.T
	server *httptest.Server
	issuer string
	key    *rsa.PrivateKey
	// now is the clock ID tokens are stamped with.
	now func() time.Time

	mutex  sync.Mutex
	codes  map[string]grant
	tokens int // how many times the token endpoint was asked
	down   bool
	// tamper changes an ID token's claims before it is signed, and signer
	// replaces the key it is signed with, for the tests of forged tokens.
	tamper func(jwt.MapClaims)
	signer *rsa.PrivateKey
}

func newStubProvider(t *testing.T, now func() time.Time) *stubProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &stubProvider{t: t, key: key, now: now, codes: map[string]grant{}}
	routes := http.NewServeMux()
	routes.HandleFunc("GET "+issuerPath+".well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		if p.isDown() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                p.issuer,
			"authorization_endpoint":                p.server.URL + "/authorize",
			"token_endpoint":                        p.server.URL + "/token",
			"jwks_uri":                              p.server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	routes.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		public := p.key.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "stub-key",
			"n": base64.RawURLEncoding.EncodeToString(public.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(public.E)).Bytes()),
		}}})
	})
	routes.HandleFunc("POST /token", p.token)
	p.server = httptest.NewServer(routes)
	p.issuer = p.server.URL + issuerPath
	t.Cleanup(p.server.Close)
	return p
}

func (p *stubProvider) isDown() bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.down
}

func (p *stubProvider) asked() int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.tokens
}

// signIn is the person signing in at the provider: it reads the
// authorization request the API redirected the browser to, holds it to what
// an authorization-code request with PKCE must carry, and answers the
// address the provider would send the browser back to.
func (p *stubProvider) signIn(authorization string, who person) string {
	p.t.Helper()
	address, err := url.Parse(authorization)
	if err != nil {
		p.t.Fatal(err)
	}
	query := address.Query()
	if !strings.HasPrefix(authorization, p.server.URL+"/authorize?") || query.Get("response_type") != "code" ||
		query.Get("client_id") != testClientID || query.Get("redirect_uri") != redirectURL ||
		query.Get("code_challenge_method") != "S256" || len(query.Get("code_challenge")) != 43 ||
		query.Get("state") == "" || query.Get("nonce") == "" || !strings.Contains(query.Get("scope"), "openid") {
		p.t.Fatalf("not an authorization-code request with PKCE: %s", authorization)
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	code := base64.RawURLEncoding.EncodeToString(raw)
	p.mutex.Lock()
	p.codes[code] = grant{who: who, challenge: query.Get("code_challenge"), nonce: query.Get("nonce")}
	p.mutex.Unlock()
	return CallbackPath + "?" + url.Values{"code": {code}, "state": {query.Get("state")}}.Encode()
}

// token is the token endpoint: one code, once, for the client that proves
// itself and holds the verifier behind the challenge.
func (p *stubProvider) token(w http.ResponseWriter, r *http.Request) {
	p.mutex.Lock()
	p.tokens++
	_ = r.ParseForm()
	issued, known := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code"))
	tamper, signer := p.tamper, p.signer
	p.mutex.Unlock()

	refuse := func(reason string) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": reason})
	}
	// RFC 6749 2.3.1: the id and the secret arrive form-encoded inside the
	// Basic credentials.
	id, secret, _ := r.BasicAuth()
	id, _ = url.QueryUnescape(id)
	secret, _ = url.QueryUnescape(secret)
	if id != testClientID || secret != testClientSecret {
		refuse("invalid_client")
		return
	}
	challenge := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !known || r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("redirect_uri") != redirectURL ||
		base64.RawURLEncoding.EncodeToString(challenge[:]) != issued.challenge {
		refuse("invalid_grant")
		return
	}
	now := p.now()
	claims := jwt.MapClaims{
		"iss": p.issuer, "aud": testClientID, "sub": issued.who.subject,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "nonce": issued.nonce,
		"email": issued.who.email, "email_verified": issued.who.emailVerified, "name": issued.who.name,
	}
	if tamper != nil {
		tamper(claims)
	}
	if signer == nil {
		signer = p.key
	}
	unsigned := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	unsigned.Header["kid"] = "stub-key"
	idToken, err := unsigned.SignedString(signer)
	if err != nil {
		p.t.Error(err)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "the-providers-own-access-token", "token_type": "Bearer", "id_token": idToken,
	})
}

// harness is the two routes over a real database and the stub provider.
type harness struct {
	t        *testing.T
	pool     *pgxpool.Pool
	provider *stubProvider
	clock    time.Time
	routes   Routes
	api      huma.API
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	// The clock starts at the real time: the gate checks a minted token's expiry against the wall
	// clock, so a fixed date made every test here fail once its tokens' hour had passed. Whole
	// seconds, so a time written to Postgres (microseconds) reads back equal.
	h := &harness{t: t, clock: time.Now().UTC().Truncate(time.Second)}
	h.pool = testpostgres.Open(t, databaseVariable)
	now := func() time.Time { return h.clock }
	h.provider = newStubProvider(t, now)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	verifier := NewVerifier(h.provider.issuer, testClientID, "authentik")
	verifier.Now, verifier.Directory.Now = now, now
	h.routes = Routes{
		Enabled: true,
		Settings: Settings{
			ClientID: testClientID, ClientSecret: testClientSecret, RedirectURL: redirectURL,
			FrontendURL: frontend + "/", ProviderName: "authentik",
		},
		Pool:     h.pool,
		Verifier: verifier,
		Sessions: signin.Routes{
			Pool: h.pool, Secret: testSecret,
			Cookies: signin.CookiePolicy{SessionName: "s_session", RefreshName: "s_refresh", SameSite: "lax", SecurePolicy: "always"},
			Hasher:  signin.NewHasher(1), Errors: envelope, Logger: logger, Now: now,
		},
		Secret: testSecret,
		Errors: envelope,
		Logger: logger,
		Now:    now,
	}
	h.serve(h.routes)
	return h
}

// serve mounts the routes; a test that changes the deployment mounts again.
func (h *harness) serve(routes Routes) {
	h.routes = routes
	h.api = humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(h.api)
	// The refresh, so a test can spend the cookies it was given.
	routes.Sessions.Register(h.api)
}

func (h *harness) get(target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	h.t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	h.api.Adapter().ServeHTTP(recorder, request)
	return recorder
}

func cookiesOf(recorder *httptest.ResponseRecorder) map[string]*http.Cookie {
	found := map[string]*http.Cookie{}
	for _, cookie := range recorder.Result().Cookies() {
		found[cookie.Name] = cookie
	}
	return found
}

// start presses the button: the redirect to the provider and the cookie.
func (h *harness) start(next string) (authorization string, attemptCookie *http.Cookie) {
	h.t.Helper()
	target := AuthorizePath
	if next != "" {
		target += "?" + url.Values{"next": {next}}.Encode()
	}
	started := h.get(target)
	attemptCookie = cookiesOf(started)[attemptCookieName]
	if started.Code != http.StatusFound || attemptCookie == nil {
		h.t.Fatalf("starting a sign-in answered %d %s", started.Code, started.Body)
	}
	return started.Header().Get("Location"), attemptCookie
}

// signIn is a whole sign-in by this person in one browser.
func (h *harness) signIn(who person, next string) *httptest.ResponseRecorder {
	h.t.Helper()
	authorization, cookie := h.start(next)
	return h.get(h.provider.signIn(authorization, who), cookie)
}

// sessionUser is the account a finished sign-in's cookies belong to: the
// access token must be this tier's, and the refresh token must be on file
// for the same account.
func (h *harness) sessionUser(finished *httptest.ResponseRecorder) string {
	h.t.Helper()
	cookies := cookiesOf(finished)
	access, refresh := cookies["s_session"], cookies["s_refresh"]
	if access == nil || refresh == nil || !access.HttpOnly || !refresh.HttpOnly || !access.Secure {
		h.t.Fatalf("no session cookies: %v", finished.Result().Cookies())
	}
	session, err := principal.SessionToken{Secret: testSecret}.Read(access.Value)
	if err != nil {
		h.t.Fatalf("the access cookie is not a session token: %v", err)
	}
	// The sign-in the token names is the one the refresh cookie belongs to,
	// so ending it ends the access token at once.
	if n := h.count(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND family_id = $2 AND token_hash = encode(sha256(convert_to($3, 'UTF8')), 'hex')`,
		session.UserID, session.SessionID, refresh.Value); n != 1 {
		h.t.Fatalf("the access cookie names sign-in %s, which the refresh cookie is not of", session.SessionID)
	}
	return session.UserID
}

// refusedWith asserts a sign-in ended on the sign-in page with this code,
// signed nobody in, and dropped the attempt.
func (h *harness) refusedWith(why string, got *httptest.ResponseRecorder, code string) {
	h.t.Helper()
	want := frontend + "/login?error=" + url.QueryEscape(code)
	cookies := cookiesOf(got)
	if got.Code != http.StatusSeeOther || got.Header().Get("Location") != want {
		h.t.Errorf("%s: answered %d to %q, want 303 to %q", why, got.Code, got.Header().Get("Location"), want)
	}
	if cookies["s_session"] != nil || cookies["s_refresh"] != nil {
		h.t.Errorf("%s: a refused sign-in set session cookies", why)
	}
	if dropped := cookies[attemptCookieName]; dropped == nil || dropped.MaxAge >= 0 {
		h.t.Errorf("%s: the attempt cookie was not dropped", why)
	}
}

func (h *harness) count(sql string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *harness) exec(sql string, args ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(context.Background(), sql, args...); err != nil {
		h.t.Fatal(err)
	}
}

var (
	alice = person{subject: "sub-alice", email: "Alice@Example.Test", emailVerified: true, name: "Alice Example"}
	bob   = person{subject: "sub-bob", email: "bob@example.test", emailVerified: true, name: "Bob Example"}
)

// The whole flow, for two people: each lands in their own account with the
// session cookies a password sign-in sets, and neither's identity ever
// opens the other's account.
func TestEachIdentitySignsInToItsOwnAccountAndNeverTheOthers(t *testing.T) {
	h := newHarness(t)
	h.exec(`UPDATE "user" SET is_verified = true`)

	hers := h.signIn(alice, "/notebooks/7?tab=chat")
	if hers.Code != http.StatusSeeOther || hers.Header().Get("Location") != frontend+"/notebooks/7?tab=chat" {
		t.Fatalf("her sign-in answered %d to %q: %s", hers.Code, hers.Header().Get("Location"), hers.Body)
	}
	if got := h.sessionUser(hers); got != testpostgres.Alice {
		t.Fatalf("Alice's identity signed in as %s", got)
	}
	if dropped := cookiesOf(hers)[attemptCookieName]; dropped == nil || dropped.MaxAge >= 0 {
		t.Error("the attempt cookie outlived the sign-in")
	}
	his := h.signIn(bob, "")
	if his.Header().Get("Location") != frontend+"/dashboard" {
		t.Errorf("with nowhere asked for he landed on %q", his.Header().Get("Location"))
	}
	if got := h.sessionUser(his); got != testpostgres.Bob {
		t.Fatalf("Bob's identity signed in as %s", got)
	}

	// The cookies are a working session: the refresh token is on file, and
	// spending it answers a session for the same account and no other.
	for _, signedIn := range []struct {
		finished *httptest.ResponseRecorder
		userID   string
	}{{hers, testpostgres.Alice}, {his, testpostgres.Bob}} {
		request := httptest.NewRequest(http.MethodPost, signin.RefreshPath, nil)
		request.AddCookie(cookiesOf(signedIn.finished)["s_refresh"])
		refreshed := httptest.NewRecorder()
		h.api.Adapter().ServeHTTP(refreshed, request)
		if refreshed.Code != http.StatusOK {
			t.Fatalf("the refresh cookie of a single sign-on answered %d %s", refreshed.Code, refreshed.Body)
		}
		if got := h.sessionUser(refreshed); got != signedIn.userID {
			t.Errorf("a refresh of %s's sign-in answered a session for %s", signedIn.userID, got)
		}
	}

	// One link each, to their own account, under the provider's name, and
	// the sign-in is stamped on their own row.
	for subject, userID := range map[string]string{"sub-alice": testpostgres.Alice, "sub-bob": testpostgres.Bob} {
		if n := h.count(`SELECT count(*) FROM oauth_account WHERE oauth_name = 'authentik' AND account_id = $1 AND user_id = $2`, subject, userID); n != 1 {
			t.Errorf("%s has %d links to %s", subject, n, userID)
		}
	}
	if n := h.count(`SELECT count(*) FROM oauth_account`); n != 2 {
		t.Errorf("%d links, want one each", n)
	}
	if n := h.count(`SELECT count(*) FROM "user" WHERE last_login = $1 AND display_name IN ('Alice Example', 'Bob Example')`, h.clock); n != 2 {
		t.Errorf("%d accounts carry the sign-in's stamp and the person's name", n)
	}
	if n := h.count(`SELECT count(*) FROM "user"`); n != 2 {
		t.Errorf("%d accounts: a sign-in to an existing account made another", n)
	}

	// Later the provider says Alice's address is Bob's. She is still found
	// by her subject and is still Alice: an address is not an identity.
	h.exec(`UPDATE "user" SET display_name = 'Ally' WHERE id = $1`, testpostgres.Alice)
	renamed := person{subject: "sub-alice", email: "bob@example.test", emailVerified: true, name: "Someone Else"}
	if got := h.sessionUser(h.signIn(renamed, "")); got != testpostgres.Alice {
		t.Fatalf("Alice's subject with Bob's address signed in as %s", got)
	}
	if n := h.count(`SELECT count(*) FROM oauth_account WHERE account_id = 'sub-alice' AND user_id = $1 AND account_email = 'bob@example.test'`, testpostgres.Alice); n != 1 {
		t.Error("her link does not carry the address the provider has today")
	}
	if n := h.count(`SELECT count(*) FROM oauth_account WHERE account_id = 'sub-bob' AND user_id = $1 AND account_email = 'bob@example.test'`, testpostgres.Bob); n != 1 {
		t.Error("Bob's link was changed by Alice's sign-in")
	}
	// The name she chose is not written over by the provider's.
	if n := h.count(`SELECT count(*) FROM "user" WHERE id = $1 AND display_name = 'Ally'`, testpostgres.Alice); n != 1 {
		t.Error("the provider's name replaced the one she set")
	}
}

// Who gets an account, and who is refused, at a first sign-in.
func TestAFirstSignInLinksOnlyToAVerifiedAccountOrMakesOneWhereAllowed(t *testing.T) {
	h := newHarness(t)
	// Alice's account is unverified, as the sign-up form leaves one: a
	// stranger may have made it in her name, so her identity is not linked
	// to it.
	h.refusedWith("an unverified account with her address", h.signIn(alice, ""), codeAccountNotVerified)
	// An identity whose address the provider did not verify matches nothing.
	h.exec(`UPDATE "user" SET is_verified = true`)
	unverified := alice
	unverified.emailVerified = false
	h.refusedWith("an address the provider did not verify", h.signIn(unverified, ""), codeEmailNotVerified)
	// A deactivated account is not signed in to, and is not linked.
	h.exec(`UPDATE "user" SET is_active = false WHERE id = $1`, testpostgres.Bob)
	h.refusedWith("a deactivated account", h.signIn(bob, ""), codeInactive)
	if n := h.count(`SELECT count(*) FROM oauth_account`); n != 0 {
		t.Errorf("%d links after three refused sign-ins", n)
	}

	// Nobody has Carol's address. With sign-up off she is refused and no
	// account appears.
	carol := person{subject: "sub-carol", email: "carol@example.test", emailVerified: true, name: "Carol Example"}
	h.refusedWith("no account, sign-up off", h.signIn(carol, ""), codeNoAccount)
	if n := h.count(`SELECT count(*) FROM "user"`); n != 2 {
		t.Fatalf("%d accounts after a refused first sign-in", n)
	}
	// With sign-up on she gets one: verified, an ordinary account, with no
	// password at all, linked to her identity.
	open := h.routes
	open.Settings.SignupAllowed = true
	h.serve(open)
	made := h.signIn(carol, "")
	carolID := h.sessionUser(made)
	if carolID == testpostgres.Alice || carolID == testpostgres.Bob {
		t.Fatalf("Carol was signed in to somebody else's account %s", carolID)
	}
	if n := h.count(`SELECT count(*) FROM "user" WHERE id = $1 AND email = 'carol@example.test' AND is_verified AND is_active
		AND NOT is_superuser AND hashed_password IS NULL AND display_name = 'Carol Example'`, carolID); n != 1 {
		t.Error("the new account is not a verified, ordinary, passwordless account in her name")
	}
	if n := h.count(`SELECT count(*) FROM oauth_account WHERE account_id = 'sub-carol' AND user_id = $1`, carolID); n != 1 {
		t.Error("the new account is not linked to her identity")
	}
	// Her second sign-in is the same account, not a second one.
	if again := h.sessionUser(h.signIn(carol, "")); again != carolID || h.count(`SELECT count(*) FROM "user"`) != 3 {
		t.Errorf("her second sign-in was %s among %d accounts", again, h.count(`SELECT count(*) FROM "user"`))
	}
	// Sign-up being on does not link an unverified account either.
	h.exec(`UPDATE "user" SET is_verified = false WHERE id = $1`, testpostgres.Alice)
	h.refusedWith("an unverified account, sign-up on", h.signIn(alice, ""), codeAccountNotVerified)
}

// A callback is finished only in the browser that started the sign-in.
func TestACallbackWithoutItsOwnAttemptIsRefusedBeforeTheProviderIsAsked(t *testing.T) {
	h := newHarness(t)
	h.exec(`UPDATE "user" SET is_verified = true`)

	authorization, cookie := h.start("")
	callback := h.provider.signIn(authorization, alice)
	_, anotherBrowsers := h.start("")
	forged := *cookie
	forged.Value += "x"
	withState := func(state string) string {
		address, _ := url.Parse(callback)
		query := address.Query()
		query.Set("state", state)
		return CallbackPath + "?" + query.Encode()
	}
	h.refusedWith("no cookie at all", h.get(callback), codeInvalidState)
	h.refusedWith("another browser's cookie", h.get(callback, anotherBrowsers), codeInvalidState)
	h.refusedWith("a cookie that was changed", h.get(callback, &forged), codeInvalidState)
	h.refusedWith("a state that is not the cookie's", h.get(withState("not-the-state"), cookie), codeInvalidState)
	h.refusedWith("no state", h.get(withState(""), cookie), codeInvalidState)
	if n := h.provider.asked(); n != 0 {
		t.Errorf("the token endpoint was asked %d times for callbacks that matched no attempt", n)
	}
	// Ten minutes on, the attempt is over.
	h.clock = h.clock.Add(attemptLifetime + time.Second)
	h.refusedWith("an attempt past its time", h.get(callback, cookie), codeInvalidState)
	h.clock = h.clock.Add(-attemptLifetime - time.Second)

	// The provider's own refusal is told as the provider's, whether or not
	// the attempt is found, and anything it did not word is not echoed.
	h.refusedWith("the person said no", h.get(CallbackPath+"?error=access_denied&state=x", cookie), "access_denied")
	h.refusedWith("the person said no, cookies blocked", h.get(CallbackPath+"?error=access_denied"), "access_denied")
	h.refusedWith("an error nobody words", h.get(CallbackPath+"?error=%3Cscript%3E&state=x", cookie), codeProviderRefused)
	h.refusedWith("no code", h.get(CallbackPath+"?state="+mustState(t, callback), cookie), codeProviderRefused)

	// And the genuine callback, in its own browser, still works after all
	// of that: none of the refusals spent the provider's code.
	if got := h.sessionUser(h.get(callback, cookie)); got != testpostgres.Alice {
		t.Errorf("the genuine callback signed in as %s", got)
	}
	// A code is spent once: the same callback again is refused by the
	// provider.
	h.refusedWith("the same callback a second time", h.get(callback, cookie), codeExchangeFailed)
}

func mustState(t *testing.T, callback string) string {
	t.Helper()
	address, err := url.Parse(callback)
	if err != nil {
		t.Fatal(err)
	}
	return address.Query().Get("state")
}

// An ID token is accepted only when it is the provider's, for this client,
// for this sign-in, and in date.
func TestAnIDTokenThatIsNotThisSignInsIsRefused(t *testing.T) {
	h := newHarness(t)
	h.exec(`UPDATE "user" SET is_verified = true`)
	stranger, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		why    string
		tamper func(jwt.MapClaims)
		signer *rsa.PrivateKey
	}{
		{"another sign-in's nonce", func(c jwt.MapClaims) { c["nonce"] = "another-sign-ins-nonce" }, nil},
		{"no nonce", func(c jwt.MapClaims) { delete(c, "nonce") }, nil},
		{"issued to another client", func(c jwt.MapClaims) { c["aud"] = "another-client" }, nil},
		{"several audiences and another authorized party", func(c jwt.MapClaims) {
			c["aud"] = []string{testClientID, "another-client"}
			c["azp"] = "another-client"
		}, nil},
		{"from another issuer", func(c jwt.MapClaims) { c["iss"] = "https://another.example/" }, nil},
		{"already expired", func(c jwt.MapClaims) { c["exp"] = h.clock.Add(-2 * time.Minute).Unix() }, nil},
		{"no subject", func(c jwt.MapClaims) { delete(c, "sub") }, nil},
		{"signed with a key the provider does not publish", nil, stranger},
	} {
		h.provider.mutex.Lock()
		h.provider.tamper, h.provider.signer = test.tamper, test.signer
		h.provider.mutex.Unlock()
		h.refusedWith(test.why, h.signIn(alice, ""), codeInvalidIDToken)
	}
	if n := h.count(`SELECT count(*) FROM oauth_account`) + h.count(`SELECT count(*) FROM refresh_tokens`); n != 0 {
		t.Errorf("%d links and sign-ins were recorded for refused tokens", n)
	}
	h.provider.mutex.Lock()
	h.provider.tamper, h.provider.signer = nil, nil
	h.provider.mutex.Unlock()
	if got := h.sessionUser(h.signIn(alice, "")); got != testpostgres.Alice {
		t.Errorf("the untampered token signed in as %s", got)
	}
}

// The code is exchanged with this client's secret and this attempt's
// verifier, and a provider that cannot be reached is said to be unavailable.
func TestTheExchangeProvesTheClientAndTheProviderBeingDownIsSaidSo(t *testing.T) {
	h := newHarness(t)
	h.exec(`UPDATE "user" SET is_verified = true`)

	// The wrong client secret: the provider refuses the exchange.
	wrong := h.routes
	wrong.Settings.ClientSecret = "not the secret"
	h.serve(wrong)
	h.refusedWith("the wrong client secret", h.signIn(alice, ""), codeExchangeFailed)

	// One browser's verifier does not spend another browser's code: the
	// attempt of browser B, with the state swapped in, still carries B's
	// verifier, which is not the one behind A's challenge.
	right := h.routes
	right.Settings.ClientSecret = testClientSecret
	h.serve(right)
	authorizationA, _ := h.start("")
	callbackA := h.provider.signIn(authorizationA, alice)
	authorizationB, cookieB := h.start("")
	stateB := mustState(t, h.provider.signIn(authorizationB, bob))
	address, _ := url.Parse(callbackA)
	query := address.Query()
	query.Set("state", stateB)
	h.refusedWith("A's code with B's attempt", h.get(CallbackPath+"?"+query.Encode(), cookieB), codeExchangeFailed)

	// The provider down, for a replica that has not read its discovery
	// document yet: there is nowhere to send the browser, and the page says
	// single sign-on is unavailable instead of failing.
	fresh := h.routes
	fresh.Verifier = NewVerifier(h.provider.issuer, testClientID, "authentik")
	h.serve(fresh)
	h.provider.mutex.Lock()
	h.provider.down = true
	h.provider.mutex.Unlock()
	h.refusedWith("the provider is down at the start", h.get(AuthorizePath), codeUnavailable)
	if n := h.count(`SELECT count(*) FROM refresh_tokens`); n != 0 {
		t.Errorf("%d sign-ins recorded", n)
	}
}

// A deployment without single sign-on answers every route 404 and reads
// nothing (the app's exchange too: app_flow_database_test.go).
func TestBothRoutesAreNotFoundWhereSingleSignOnIsOff(t *testing.T) {
	h := newHarness(t)
	h.serve(Routes{Errors: h.routes.Errors, Logger: h.routes.Logger})
	for _, path := range []string{AuthorizePath, CallbackPath + "?code=x&state=y", AppStartPath + "?app_redirect=soloclef://auth"} {
		got := h.get(path)
		if got.Code != http.StatusNotFound || !strings.Contains(got.Body.String(), "Single sign-on is not switched on") || len(got.Result().Cookies()) != 0 {
			t.Errorf("%s answered %d %s", path, got.Code, got.Body)
		}
	}
}
