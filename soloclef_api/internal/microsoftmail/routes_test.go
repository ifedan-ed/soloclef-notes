package microsoftmail

import (
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailsignin"

	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/mailaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres for this package's tests.
// **Every account in it is deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_MICROSOFTMAIL_DATABASE"

// fakeMicrosoft is Microsoft's token endpoint.
type fakeMicrosoft struct {
	*httptest.Server
	mu sync.Mutex
	// challenge is the S256 challenge the sign-in page was last given; the
	// code exchange must come with the verifier behind it.
	challenge string
	// What the next code exchange hands over.
	refreshToken, scope string
	claims              map[string]any
	// refusal, when set, is the error every request is refused with.
	refusal string
	// dead are refresh tokens Microsoft no longer accepts.
	dead map[string]bool
	// refreshes counts refresh requests.
	refreshes int
}

func newFakeMicrosoft(t *testing.T) *fakeMicrosoft {
	t.Helper()
	m := &fakeMicrosoft{refreshToken: "fake-refresh-1", scope: "openid email profile " + mailsignin.IMAPScope, dead: map[string]bool{},
		claims: map[string]any{"aud": clientID, "email": "ada@example.test", "tid": "tenant-1", "oid": "object-1"}}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		_ = r.ParseForm()
		refuse := func(status int, code string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": code, "error_codes": []int{70000}})
		}
		switch {
		case r.URL.Path != "/token" || r.Method != http.MethodPost:
			w.WriteHeader(http.StatusTeapot)
		case r.Form.Get("client_id") != clientID || r.Form.Get("client_secret") != "fake-client-secret":
			refuse(http.StatusUnauthorized, "invalid_client")
		case m.refusal != "":
			refuse(http.StatusBadRequest, m.refusal)
		case r.Form.Get("grant_type") == "authorization_code":
			digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "good-code" || base64.RawURLEncoding.EncodeToString(digest[:]) != m.challenge ||
				r.Form.Get("redirect_uri") != "https://api.notes.example"+CallbackPath {
				refuse(http.StatusBadRequest, "invalid_grant")
				return
			}
			reply := map[string]any{"access_token": "fake-access-first", "scope": m.scope, "expires_in": 3600, "id_token": idToken(m.claims)}
			if m.refreshToken != "" {
				reply["refresh_token"] = m.refreshToken
			}
			_ = json.NewEncoder(w).Encode(reply)
		case r.Form.Get("grant_type") == "refresh_token":
			m.refreshes++
			if m.dead[r.Form.Get("refresh_token")] {
				refuse(http.StatusBadRequest, "invalid_grant")
				return
			}
			// Microsoft rotates the refresh token.
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fake-access-renewed",
				"refresh_token": r.Form.Get("refresh_token") + "-rotated", "expires_in": "3600"})
		}
	}))
	t.Cleanup(m.Close)
	return m
}

type mailSite struct {
	t         *testing.T
	pool      *pgxpool.Pool
	microsoft *fakeMicrosoft
	now       time.Time
	settings  Settings
	handler   http.Handler
	// caller is who is asking; nil is nobody, as at Microsoft's callback.
	caller *principal.Principal
}

func newMailSite(t *testing.T, configured bool) *mailSite {
	t.Helper()
	s := &mailSite{t: t, pool: testpostgres.Open(t, databaseVariable), microsoft: newFakeMicrosoft(t),
		now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	s.settings = Settings{
		ClientSecret: "fake-client-secret", Tenant: "common", RedirectURI: "https://api.notes.example" + CallbackPath,
		FrontendURL: "https://notes.example/", SecretKey: secretKey,
		AuthorizeURL: "https://login.microsoft.test/common/oauth2/v2.0/authorize", TokenURL: s.microsoft.URL + "/token",
	}
	if configured {
		s.settings.ClientID = clientID
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes, err := NewRoutes(Store{Pool: s.pool}, s.settings, http.DefaultTransport, func() time.Time { return s.now },
		errorenvelope.Writer{ReportURL: "https://notes.example/contact"}, logger)
	if err != nil {
		t.Fatal(err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	withCaller := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.caller != nil {
			r = r.WithContext(principal.WithPrincipal(r.Context(), *s.caller))
		}
		api.Adapter().ServeHTTP(w, r)
	})
	bodies := requestbody.Middleware{
		Covers: requestbody.CoveringRoutes(BodyRoutes()...),
	}
	s.handler = requestcontext.Middleware(logger, bodies.Wrap(withCaller))
	return s
}

func (s *mailSite) signIn() SignIn {
	s.t.Helper()
	signIn, err := NewSignIn(Store{Pool: s.pool}, s.settings, http.DefaultTransport, func() time.Time { return s.now },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		s.t.Fatal(err)
	}
	return signIn
}

// as sends a request as a session of userID ("" for nobody), with cookie
// as the browser's cookie header.
func (s *mailSite) as(userID, method, path, body, cookie string) *httptest.ResponseRecorder {
	s.t.Helper()
	s.caller = nil
	if userID != "" {
		s.caller = &principal.Principal{UserID: userID, Method: principal.MethodSession}
	}
	return s.send(method, path, body, cookie)
}

func (s *mailSite) send(method, path, body, cookie string) *httptest.ResponseRecorder {
	s.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	// No answer or redirect holds a token, the client secret or a sealed
	// value. Every invented secret here begins "fake-".
	whole := recorder.Body.String() + recorder.Header().Get("Location")
	if strings.Contains(whole, "fake-") || strings.Contains(whole, "gAAAA") {
		s.t.Fatalf("%s %s answered a credential: %s", method, path, whole)
	}
	return recorder
}

// begin starts a connect as userID and returns the browser's cookie, the
// state Microsoft would echo and the sign-in page's query.
func (s *mailSite) begin(userID, body string) (cookie, state string, query url.Values) {
	s.t.Helper()
	started := s.as(userID, http.MethodPost, ConnectPath, body, "")
	var answer struct {
		AuthURL string `json:"auth_url"`
	}
	if started.Code != http.StatusOK || json.Unmarshal(started.Body.Bytes(), &answer) != nil {
		s.t.Fatalf("starting answered %d %s", started.Code, started.Body)
	}
	consent, err := url.Parse(answer.AuthURL)
	if err != nil || consent.Host != "login.microsoft.test" {
		s.t.Fatalf("the sign-in page is %q", answer.AuthURL)
	}
	query = consent.Query()
	for key, want := range map[string]string{
		"client_id": clientID, "redirect_uri": "https://api.notes.example" + CallbackPath, "scope": scopes,
		"response_type": "code", "response_mode": "query", "code_challenge_method": "S256", "prompt": "select_account",
	} {
		if query.Get(key) != want {
			s.t.Errorf("the sign-in page's %s is %q, want %q", key, query.Get(key), want)
		}
	}
	s.microsoft.mu.Lock()
	s.microsoft.challenge = query.Get("code_challenge")
	s.microsoft.mu.Unlock()
	set := started.Result().Cookies()
	if len(set) != 1 || set[0].Name != cookieName || !set[0].HttpOnly || !set[0].Secure ||
		set[0].Path != CallbackPath || set[0].SameSite != http.SameSiteLaxMode || set[0].MaxAge != 600 {
		s.t.Fatalf("the attempt's cookie is %+v", set)
	}
	// Sealed: the verifier in it cannot be read off the cookie.
	if strings.Contains(set[0].Value, ".") || strings.Contains(set[0].Value, userID) {
		s.t.Errorf("the cookie is readable: %q", set[0].Value)
	}
	return cookieName + "=" + set[0].Value, query.Get("state"), query
}

// finish is Microsoft's callback, and where it sent the browser.
func (s *mailSite) finish(cookie, query string) (location string, outcome string) {
	s.t.Helper()
	back := s.as("", http.MethodGet, CallbackPath+"?"+query, "", cookie)
	if back.Code != http.StatusSeeOther {
		s.t.Fatalf("the callback answered %d %s", back.Code, back.Body)
	}
	target, err := url.Parse(back.Header().Get("Location"))
	if err != nil || target.Host != "notes.example" || target.Scheme != "https" {
		s.t.Fatalf("the callback sent the browser to %q", back.Header().Get("Location"))
	}
	if back.Header().Get("Cache-Control") != "no-store" {
		s.t.Errorf("the callback's answer may be kept")
	}
	// The attempt's cookie is spent whatever happened.
	if cleared := back.Result().Cookies(); len(cleared) != 1 || cleared[0].Name != cookieName || cleared[0].MaxAge >= 0 {
		s.t.Errorf("the attempt's cookie was not cleared: %+v", cleared)
	}
	return target.Path, target.Query().Get(outcomeParam)
}

// records is the owner's stored mailboxes.
func (s *mailSite) records(userID string) []record {
	s.t.Helper()
	var raw []byte
	err := s.pool.QueryRow(context.Background(), `
		SELECT COALESCE(jsonb_agg(entry.record ORDER BY c.id, entry.position), '[]'::jsonb)
		FROM search_source_connectors c, jsonb_array_elements(c.config -> 'accounts') WITH ORDINALITY AS entry(record, position)
		WHERE c.user_id = $1 AND c.connector_type = 'EMAIL_IMAP_CONNECTOR'`, userID).Scan(&raw)
	if err != nil {
		s.t.Fatal(err)
	}
	var records []record
	if err := json.Unmarshal(raw, &records); err != nil {
		s.t.Fatal(err)
	}
	return records
}

func (s *mailSite) connections() int {
	s.t.Helper()
	var count int
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM search_source_connectors`).Scan(&count); err != nil {
		s.t.Fatal(err)
	}
	return count
}

func TestADeploymentWithoutAMicrosoftAppSaysSo(t *testing.T) {
	s := newMailSite(t, false)
	if answered := s.as(testpostgres.Alice, http.MethodGet, AvailabilityPath, "", ""); answered.Code != http.StatusOK ||
		strings.TrimSpace(answered.Body.String()) != `{"configured":false}` {
		t.Errorf("availability answered %d %s", answered.Code, answered.Body)
	}
	for _, request := range []*httptest.ResponseRecorder{
		s.as(testpostgres.Alice, http.MethodPost, ConnectPath, `{"return_path": "/dashboard"}`, ""),
		s.as("", http.MethodGet, CallbackPath+"?code=good-code&state=x", "", ""),
	} {
		if request.Code != http.StatusServiceUnavailable || !strings.Contains(request.Body.String(), notConfiguredCode) {
			t.Errorf("answered %d %s", request.Code, request.Body)
		}
	}
	if s.connections() != 0 {
		t.Error("something was stored")
	}
}

func TestConnectingAMicrosoftMailbox(t *testing.T) {
	s := newMailSite(t, true)
	const alice, bob = testpostgres.Alice, testpostgres.Bob

	if answered := s.as(alice, http.MethodGet, AvailabilityPath, "", ""); strings.TrimSpace(answered.Body.String()) != `{"configured":true}` {
		t.Fatalf("availability answered %d %s", answered.Code, answered.Body)
	}

	t.Run("an API key may ask whether it is set up and may not connect", func(t *testing.T) {
		s.caller = &principal.Principal{UserID: alice, Method: principal.MethodPersonalAccessToken}
		if answered := s.send(http.MethodGet, AvailabilityPath, "", ""); answered.Code != http.StatusOK {
			t.Errorf("availability answered %d", answered.Code)
		}
		if answered := s.send(http.MethodPost, ConnectPath, "", ""); answered.Code != http.StatusForbidden {
			t.Errorf("connect answered %d", answered.Code)
		}
	})

	t.Run("a body that is not an object, or too long, is refused", func(t *testing.T) {
		for _, body := range []string{`[1]`, `{"return_path": "/` + strings.Repeat("a", 512) + `"}`, `{"account_id": "` + strings.Repeat("a", 65) + `"}`} {
			if answered := s.as(alice, http.MethodPost, ConnectPath, body, ""); answered.Code != http.StatusUnprocessableEntity {
				t.Errorf("%.30s answered %d", body, answered.Code)
			}
		}
	})

	var aliceID string
	t.Run("the browser that started it connects the mailbox, tokens sealed", func(t *testing.T) {
		cookie, state, query := s.begin(alice, `{"return_path": "/dashboard/settings/connectors?tab=mail", "account_id": null}`)
		if query.Has("login_hint") {
			t.Errorf("a new connect hinted %q", query.Get("login_hint"))
		}
		path, outcome := s.finish(cookie, "code=good-code&state="+url.QueryEscape(state))
		if path != "/dashboard/settings/connectors" || outcome != outcomeConnected {
			t.Fatalf("landed on %s with %q", path, outcome)
		}
		held := s.records(alice)
		if len(held) != 1 {
			t.Fatalf("stored %d mailboxes", len(held))
		}
		box := held[0]
		aliceID = box.Text("id")
		access, expires, refresh, err := storedTokens(box, secretKey)
		if err != nil || access != "fake-access-first" || refresh != "fake-refresh-1" || !expires.Equal(s.now.Add(time.Hour)) {
			t.Errorf("stored tokens %q until %v, %q, %v", access, expires, refresh, err)
		}
		if !box.IsMicrosoft() || box.Text("username") != "ada@example.test" || box.Text("label") != "ada@example.test" ||
			box.Text("host") != IMAPHost || box.Text(subjectKey) != "tenant-1:object-1" || !box.Truth(encryptedKey) ||
			box.Text("created_at") != "2026-11-12T09:00:00.000000+00:00" {
			t.Errorf("the record is %v", box)
		}
		// Nothing readable is stored.
		for key, value := range box {
			if strings.Contains(string(value), "fake-") {
				t.Errorf("%s is stored in the clear", key)
			}
		}
		// The mail connection is the one password mailboxes share.
		var name string
		if err := s.pool.QueryRow(context.Background(), `SELECT name FROM search_source_connectors WHERE user_id = $1`, alice).Scan(&name); err != nil || name != mailaccounts.ConnectionName {
			t.Errorf("the connection is named %q (%v)", name, err)
		}
	})

	t.Run("a callback without this browser's cookie, or with another state, saves nothing", func(t *testing.T) {
		cookie, state, _ := s.begin(bob, "")
		for name, attempt := range map[string][2]string{
			"another browser":       {"", "code=good-code&state=" + url.QueryEscape(state)},
			"another state":         {cookie, "code=good-code&state=forged"},
			"a state that is bytes": {cookie, "code=good-code&state=%C3%A9%00"},
			"no state":              {cookie, "code=good-code"},
		} {
			path, outcome := s.finish(attempt[0], attempt[1])
			if path != connectorsPage || outcome != outcomeExpired {
				t.Errorf("%s: landed on %s with %q", name, path, outcome)
			}
		}
		// Ten minutes on, the attempt has run out.
		s.now = s.now.Add(attemptLifetime)
		if _, outcome := s.finish(cookie, "code=good-code&state="+url.QueryEscape(state)); outcome != outcomeExpired {
			t.Errorf("a stale attempt ended %q", outcome)
		}
		s.now = s.now.Add(-attemptLifetime)
		if len(s.records(bob)) != 0 || s.connections() != 1 {
			t.Error("a refused callback stored something")
		}
	})

	t.Run("each way Microsoft can refuse lands on the page with its own outcome", func(t *testing.T) {
		for name, test := range map[string]struct {
			arrange func()
			query   string
			want    string
		}{
			"declined on Microsoft's page": {query: "error=access_denied", want: outcomeDenied},
			"only an admin may consent":    {query: "error=access_denied&error_description=AADSTS65001%3A+needs+admin", want: outcomeAdminApproval},
			"consent required":             {query: "error=consent_required", want: outcomeAdminApproval},
			"some other error":             {query: "error=server_error&error_description=fake-noise", want: outcomeFailed},
			"no code":                      {query: "", want: outcomeFailed},
			"a code Microsoft refuses":     {query: "code=stale-code", want: outcomeFailed},
			"the exchange needs an admin":  {arrange: func() { s.microsoft.refusal = "consent_required" }, query: "code=good-code", want: outcomeAdminApproval},
			"mailbox access unticked":      {arrange: func() { s.microsoft.scope = "openid email profile" }, query: "code=good-code", want: outcomeScopeMissing},
			"no refresh token":             {arrange: func() { s.microsoft.refreshToken = "" }, query: "code=good-code", want: outcomeNoRefresh},
			"an id_token for another app": {arrange: func() { s.microsoft.claims = map[string]any{"aud": "another-app", "email": "bob@example.test"} },
				query: "code=good-code", want: outcomeNoAddress},
			"an account with no address": {arrange: func() { s.microsoft.claims = map[string]any{"aud": clientID, "oid": "o"} },
				query: "code=good-code", want: outcomeNoAddress},
		} {
			t.Run(name, func(t *testing.T) {
				refusal, scope, refreshToken, claims := s.microsoft.refusal, s.microsoft.scope, s.microsoft.refreshToken, s.microsoft.claims
				defer func() {
					s.microsoft.mu.Lock()
					s.microsoft.refusal, s.microsoft.scope, s.microsoft.refreshToken, s.microsoft.claims = refusal, scope, refreshToken, claims
					s.microsoft.mu.Unlock()
				}()
				cookie, state, _ := s.begin(bob, `{"return_path": "/dashboard/notebooks/7"}`)
				if test.arrange != nil {
					s.microsoft.mu.Lock()
					test.arrange()
					s.microsoft.mu.Unlock()
				}
				path, outcome := s.finish(cookie, test.query+"&state="+url.QueryEscape(state))
				if path != "/dashboard/notebooks/7" || outcome != test.want {
					t.Errorf("landed on %s with %q, want %q", path, outcome, test.want)
				}
			})
		}
		if len(s.records(bob)) != 0 {
			t.Error("a refused connect stored a mailbox")
		}
	})

	t.Run("reconnecting replaces the same mailbox's tokens and hints its address", func(t *testing.T) {
		// A password mailbox beside it, and the Microsoft one flagged.
		testpostgres.Exec(t, s.pool, `UPDATE search_source_connectors
			SET config = jsonb_set(jsonb_set(config, '{accounts,0,needs_reconnect}', 'true'::jsonb), '{accounts}',
			    config -> 'accounts' || '[{"id": "pw-1", "username": "ada@example.test", "app_password": "sealed", "_encrypted": true}]'::jsonb)
			WHERE user_id = '`+alice+`'`)
		s.microsoft.mu.Lock()
		s.microsoft.refreshToken = "fake-refresh-2"
		s.microsoft.mu.Unlock()
		cookie, state, query := s.begin(alice, `{"account_id": "`+aliceID+`"}`)
		if query.Get("login_hint") != "ada@example.test" {
			t.Errorf("the sign-in page was hinted %q", query.Get("login_hint"))
		}
		if path, outcome := s.finish(cookie, "code=good-code&state="+url.QueryEscape(state)); path != connectorsPage || outcome != outcomeConnected {
			t.Fatalf("landed on %s with %q", path, outcome)
		}
		held := s.records(alice)
		if len(held) != 2 || held[0].Text("id") != aliceID || held[0].Truth(mailsignin.ReconnectKey) || held[1].Text("id") != "pw-1" || s.connections() != 1 {
			t.Fatalf("after reconnecting: %v", held)
		}
		if _, _, refresh, err := storedTokens(held[0], secretKey); err != nil || refresh != "fake-refresh-2" {
			t.Errorf("the refresh token is %q (%v)", refresh, err)
		}
	})

	t.Run("one account never reads, hints or changes another's mailbox", func(t *testing.T) {
		// Bob names Alice's mailbox: no hint, and what he connects is his.
		cookie, state, query := s.begin(bob, `{"account_id": "`+aliceID+`"}`)
		if query.Has("login_hint") {
			t.Errorf("another account's mailbox was hinted: %q", query.Get("login_hint"))
		}
		before := s.records(alice)
		if _, outcome := s.finish(cookie, "code=good-code&state="+url.QueryEscape(state)); outcome != outcomeConnected {
			t.Fatalf("Bob's connect ended %q", outcome)
		}
		// The same Microsoft account, and still two separate records.
		theirs := s.records(bob)
		after := s.records(alice)
		if len(theirs) != 1 || theirs[0].Text("id") == aliceID || len(after) != 2 ||
			string(after[0][mailsignin.RefreshKey]) != string(before[0][mailsignin.RefreshKey]) || s.connections() != 2 {
			t.Fatalf("Bob has %v; Alice has %v", theirs, after)
		}
		rows := Store{Pool: s.pool}
		if found, err := rows.Mailbox(context.Background(), bob, aliceID); err != nil || found != nil {
			t.Errorf("Bob read Alice's mailbox: %s, %v", found, err)
		}
		if found, err := rows.Mailbox(context.Background(), alice, "pw-1"); err != nil || found != nil {
			t.Errorf("a password mailbox was read as a Microsoft one: %s, %v", found, err)
		}
		if changed, err := rows.Merge(context.Background(), bob, aliceID, []byte(`{"needs_reconnect": true}`)); err != nil || changed {
			t.Errorf("Bob changed Alice's mailbox: %v, %v", changed, err)
		}
		if changed, err := rows.Merge(context.Background(), alice, "pw-1", []byte(`{"needs_reconnect": true}`)); err != nil || changed {
			t.Errorf("a password mailbox was merged into: %v, %v", changed, err)
		}
		if again := s.records(alice); again[0].Truth(mailsignin.ReconnectKey) || again[1].Truth(mailsignin.ReconnectKey) {
			t.Errorf("Alice's mailboxes changed: %v", again)
		}
		// An account that is gone connects nothing.
		err := rows.Save(context.Background(), "33333333-3333-4333-8333-333333333333",
			func(accounts []json.RawMessage) ([]json.RawMessage, error) { return accounts, nil })
		if !errors.Is(err, errAccountGone) {
			t.Errorf("saving for no account: %v", err)
		}
	})
}

func TestSigningInToAStoredMicrosoftMailbox(t *testing.T) {
	s := newMailSite(t, true)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	for _, owner := range []string{alice, bob} {
		cookie, state, _ := s.begin(owner, "")
		if _, outcome := s.finish(cookie, "code=good-code&state="+url.QueryEscape(state)); outcome != outcomeConnected {
			t.Fatalf("connecting ended %q", outcome)
		}
	}
	signIn := s.signIn()
	stored := func(owner string) []byte {
		raw, err := json.Marshal(s.records(owner)[0])
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	refusedWith := func(err error) string {
		var refused *mailaccounts.ConnectionError
		if errors.As(err, &refused) {
			return refused.Message
		}
		return ""
	}

	// A token with time left is used as it is: Microsoft is not asked.
	settings, err := signIn.Mailbox(context.Background(), alice, stored(alice))
	want := mailaccounts.Settings{Host: "outlook.office365.com", Port: 993, UseTLS: true, Username: "ada@example.test", AccessToken: "fake-access-first"}
	if err != nil || settings != want || s.microsoft.refreshes != 0 {
		t.Fatalf("signed in with %+v, %v, after %d refreshes", settings, err, s.microsoft.refreshes)
	}

	// Under five minutes left: refreshed, and both new tokens stored
	// sealed in Alice's record only.
	s.now = s.now.Add(56 * time.Minute)
	bobBefore := stored(bob)
	settings, err = signIn.Mailbox(context.Background(), alice, stored(alice))
	if err != nil || settings.AccessToken != "fake-access-renewed" || s.microsoft.refreshes != 1 {
		t.Fatalf("signed in with %+v, %v, after %d refreshes", settings, err, s.microsoft.refreshes)
	}
	access, expires, refresh, err := storedTokens(s.records(alice)[0], secretKey)
	if err != nil || access != "fake-access-renewed" || refresh != "fake-refresh-1-rotated" || !expires.Equal(s.now.Add(time.Hour)) {
		t.Errorf("stored after a refresh: %q until %v, %q, %v", access, expires, refresh, err)
	}
	if string(stored(bob)) != string(bobBefore) {
		t.Error("Alice's refresh changed Bob's mailbox")
	}
	if _, err = signIn.Mailbox(context.Background(), alice, stored(alice)); err != nil || s.microsoft.refreshes != 1 {
		t.Errorf("the stored token was not reused: %v, %d refreshes", err, s.microsoft.refreshes)
	}

	// This deployment's secret refused, or Microsoft down: the mailbox is
	// left alone and its owner told which.
	s.now = s.now.Add(2 * time.Hour)
	s.microsoft.mu.Lock()
	s.microsoft.refusal = "invalid_client"
	s.microsoft.mu.Unlock()
	if _, err = signIn.Mailbox(context.Background(), alice, stored(alice)); refusedWith(err) != serverRefusedMessage {
		t.Errorf("a refused client secret: %v", err)
	}
	s.microsoft.mu.Lock()
	s.microsoft.refusal = "temporarily_unavailable"
	s.microsoft.mu.Unlock()
	if _, err = signIn.Mailbox(context.Background(), alice, stored(alice)); refusedWith(err) != unreachableMessage {
		t.Errorf("Microsoft unavailable: %v", err)
	}
	if s.records(alice)[0].Truth(mailsignin.ReconnectKey) {
		t.Error("a failure that was not the grant's flagged the mailbox")
	}

	// The grant itself refused: flagged, and from then on Microsoft is not
	// asked again.
	s.microsoft.mu.Lock()
	s.microsoft.refusal = ""
	s.microsoft.dead["fake-refresh-1-rotated"] = true
	s.microsoft.mu.Unlock()
	if _, err = signIn.Mailbox(context.Background(), alice, stored(alice)); refusedWith(err) != reconnectMessage {
		t.Errorf("a revoked grant: %v", err)
	}
	asked := s.microsoft.refreshes
	if !s.records(alice)[0].Truth(mailsignin.ReconnectKey) || s.records(bob)[0].Truth(mailsignin.ReconnectKey) {
		t.Error("the revoked mailbox, and only it, should be flagged")
	}
	if _, err = signIn.Mailbox(context.Background(), alice, stored(alice)); refusedWith(err) != reconnectMessage || s.microsoft.refreshes != asked {
		t.Errorf("a flagged mailbox: %v, %d more requests", err, s.microsoft.refreshes-asked)
	}

	// A record sealed under another key cannot be opened: reconnect.
	other := s.settings
	other.SecretKey = "another deployment's key"
	elsewhere, err := NewSignIn(Store{Pool: s.pool}, other, http.DefaultTransport, func() time.Time { return s.now },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = elsewhere.Mailbox(context.Background(), bob, stored(bob)); refusedWith(err) != reconnectMessage || !s.records(bob)[0].Truth(mailsignin.ReconnectKey) {
		t.Errorf("tokens this key cannot open: %v", err)
	}
	// A password mailbox is not this package's to sign in to.
	if _, err = signIn.Mailbox(context.Background(), alice, []byte(`{"id": "pw-1", "app_password": "x"}`)); err == nil || refusedWith(err) != "" {
		t.Errorf("a password mailbox: %v", err)
	}
}

func TestTheRoutesRefuseSettingsThatCannotWork(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	good := Settings{ClientID: clientID, ClientSecret: "fake-client-secret", Tenant: "common",
		RedirectURI: "https://api.notes.example" + CallbackPath, FrontendURL: "https://notes.example", SecretKey: secretKey,
		AuthorizeURL: AuthorizeURL("common"), TokenURL: TokenURL("common")}
	build := func(change func(*Settings)) error {
		settings := good
		change(&settings)
		_, err := NewRoutes(Store{}, settings, http.DefaultTransport, time.Now, errorenvelope.Writer{}, logger)
		return err
	}
	if err := build(func(*Settings) {}); err != nil {
		t.Fatalf("good settings: %v", err)
	}
	// Not set up at all is a deployment's choice, not a fault.
	if err := build(func(s *Settings) { *s = Settings{SecretKey: secretKey} }); err != nil {
		t.Errorf("no Microsoft app: %v", err)
	}
	for name, change := range map[string]func(*Settings){
		"no secret key":           func(s *Settings) { s.SecretKey = "" },
		"no website":              func(s *Settings) { s.FrontendURL = "" },
		"another callback path":   func(s *Settings) { s.RedirectURI = "https://api.notes.example/elsewhere" },
		"no token endpoint named": func(s *Settings) { s.TokenURL = "" },
	} {
		if build(change) == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
