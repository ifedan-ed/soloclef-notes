package driveconnect

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// databaseVariable names a throwaway Postgres for this package's tests.
// **Every account in it is deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_DRIVECONNECT_DATABASE"

const secretKey = "a secret key made up for this test"

// fakeGoogle is Google's token, about and revoke endpoints.
type fakeGoogle struct {
	*httptest.Server
	mu sync.Mutex
	// challenge is the S256 challenge the consent page was last given; the
	// code exchange must come with the verifier behind it.
	challenge string
	// refreshToken, scope and email are what the next exchange hands over.
	refreshToken, scope, email string
	// dead are refresh tokens Google no longer accepts.
	dead    map[string]bool
	revoked []string
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	g := &fakeGoogle{refreshToken: "fake-refresh-1", scope: driveFileScope, email: "ada@gmail.test", dead: map[string]bool{}}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/token":
			if r.Form.Get("client_id") != "123456-web.apps.googleusercontent.com" || r.Form.Get("client_secret") != "fake-client-secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if r.Form.Get("code") != "good-code" || base64.RawURLEncoding.EncodeToString(digest[:]) != g.challenge {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error": "invalid_grant"}`)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fake-access-first",
					"refresh_token": g.refreshToken, "scope": g.scope, "expires_in": 3600})
			case "refresh_token":
				if g.dead[r.Form.Get("refresh_token")] {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error": "invalid_grant"}`)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "picker-token-for-" + strings.TrimPrefix(r.Form.Get("refresh_token"), "fake-"), "expires_in": 3600})
			}
		case "/about":
			if r.Header.Get("Authorization") != "Bearer fake-access-first" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]string{"emailAddress": g.email}})
		case "/revoke":
			g.revoked = append(g.revoked, r.Form.Get("token"))
			g.dead[r.Form.Get("token")] = true
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

type driveSite struct {
	t       *testing.T
	pool    *pgxpool.Pool
	google  *fakeGoogle
	now     time.Time
	handler http.Handler
	// caller is who is signed in; nil is nobody, as at Google's callback.
	caller *principal.Principal
}

func newDriveSite(t *testing.T, configured bool) *driveSite {
	t.Helper()
	s := &driveSite{t: t, pool: testpostgres.Open(t, databaseVariable), google: newFakeGoogle(t),
		now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	settings := Settings{
		ClientSecret: "fake-client-secret", RedirectURI: "https://notes.example" + CallbackPath,
		PickerAPIKey: "browser-key", FrontendURL: "https://notes.example/", SecretKey: secretKey,
		AuthURL: "https://accounts.google.test/auth", TokenURL: s.google.URL + "/token",
		RevokeURL: s.google.URL + "/revoke", AboutURL: s.google.URL + "/about",
	}
	if configured {
		settings.ClientID = "123456-web.apps.googleusercontent.com"
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes, err := NewRoutes(Store{Pool: s.pool}, settings, http.DefaultTransport, func() time.Time { return s.now },
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

// as sends a request as a session of userID ("" for nobody), with cookie
// as the browser's cookie header.
func (s *driveSite) as(userID, method, path, body, cookie string) *httptest.ResponseRecorder {
	s.t.Helper()
	s.caller = nil
	if userID != "" {
		s.caller = &principal.Principal{UserID: userID, Method: principal.MethodSession}
	}
	return s.send(method, path, body, cookie)
}

func (s *driveSite) send(method, path, body, cookie string) *httptest.ResponseRecorder {
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
	// No answer or redirect holds a stored token, the client secret or a
	// sealed value. Every invented secret here begins "fake-".
	whole := recorder.Body.String() + recorder.Header().Get("Location")
	if strings.Contains(whole, "fake-") || strings.Contains(whole, "gAAAA") {
		s.t.Fatalf("%s %s answered a credential: %s", method, path, whole)
	}
	return recorder
}

// begin starts a connect as userID and returns the browser's cookie and
// the state Google would echo.
func (s *driveSite) begin(userID, method, path, body string) (cookie, state string) {
	s.t.Helper()
	started := s.as(userID, method, path, body, "")
	var answer struct {
		AuthURL string `json:"auth_url"`
	}
	if started.Code != http.StatusOK || json.Unmarshal(started.Body.Bytes(), &answer) != nil {
		s.t.Fatalf("starting answered %d %s", started.Code, started.Body)
	}
	consent, err := url.Parse(answer.AuthURL)
	if err != nil || consent.Host != "accounts.google.test" {
		s.t.Fatalf("the consent page is %q", answer.AuthURL)
	}
	query := consent.Query()
	for key, want := range map[string]string{
		"client_id": "123456-web.apps.googleusercontent.com", "redirect_uri": "https://notes.example" + CallbackPath,
		"scope": driveFileScope, "response_type": "code", "code_challenge_method": "S256",
		"access_type": "offline", "prompt": "consent",
	} {
		if query.Get(key) != want {
			s.t.Errorf("the consent page's %s is %q, want %q", key, query.Get(key), want)
		}
	}
	s.google.mu.Lock()
	s.google.challenge = query.Get("code_challenge")
	s.google.mu.Unlock()
	set := started.Result().Cookies()
	if len(set) != 1 || set[0].Name != cookieName || !set[0].HttpOnly || !set[0].Secure || set[0].Path != CallbackPath ||
		set[0].SameSite != http.SameSiteLaxMode || set[0].MaxAge != 600 {
		s.t.Fatalf("the attempt's cookie is %+v", set)
	}
	return cookieName + "=" + set[0].Value, query.Get("state")
}

func callbackURL(state, code string) string {
	return CallbackPath + "?" + url.Values{"state": {state}, "code": {code}}.Encode()
}

// stored is the account's Drive rows.
func (s *driveSite) stored(userID string) (ids []int64, configs []map[string]any) {
	s.t.Helper()
	rows, err := s.pool.Query(context.Background(), `SELECT id, config FROM search_source_connectors
		WHERE user_id = $1 AND connector_type = 'GOOGLE_DRIVE_CONNECTOR' ORDER BY id`, userID)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			s.t.Fatal(err)
		}
		var config map[string]any
		_ = json.Unmarshal(raw, &config)
		ids, configs = append(ids, id), append(configs, config)
	}
	return
}

func (s *driveSite) opened(sealed any) string {
	s.t.Helper()
	text, _ := sealed.(string)
	plain, err := storedtoken.Open(secretKey, text)
	if err != nil {
		s.t.Fatalf("a stored token does not open: %v", err)
	}
	return plain
}

func TestConnectingGoogleDrive(t *testing.T) {
	s := newDriveSite(t, true)
	const alice, bob = testpostgres.Alice, testpostgres.Bob

	cookie, state := s.begin(alice, http.MethodPost, ConnectPath, `{"return_path": "/dashboard/notebooks/7"}`)

	t.Run("the callback is only for the browser that started, and only once it has Google's own state", func(t *testing.T) {
		if strings.Contains(cookie, state) {
			t.Error("the cookie shows the state it must match")
		}
		for name, attempt := range map[string][2]string{
			"another browser":      {"", callbackURL(state, "good-code")},
			"a state it never had": {cookie, callbackURL("made-up", "good-code")},
			"a forged cookie":      {cookieName + "=gAAAAAforged", callbackURL(state, "good-code")},
		} {
			answer := s.as("", http.MethodGet, attempt[1], "", attempt[0])
			if answer.Code != http.StatusSeeOther || !strings.Contains(answer.Header().Get("Location"), "error="+outcomeExpired) {
				t.Errorf("%s: %d to %s", name, answer.Code, answer.Header().Get("Location"))
			}
		}
		// Ten minutes and no more.
		s.now = s.now.Add(attemptLifetime)
		if answer := s.as("", http.MethodGet, callbackURL(state, "good-code"), "", cookie); !strings.Contains(answer.Header().Get("Location"), outcomeExpired) {
			t.Errorf("an attempt past its time went to %s", answer.Header().Get("Location"))
		}
		s.now = s.now.Add(-attemptLifetime)
		if ids, _ := s.stored(alice); len(ids) != 0 {
			t.Fatal("a refused callback saved a connection")
		}
	})

	t.Run("the grant is saved sealed, in the fields every reader of a Drive row reads", func(t *testing.T) {
		answer := s.as("", http.MethodGet, callbackURL(state, "good-code"), "", cookie)
		if answer.Code != http.StatusSeeOther || answer.Header().Get("Location") != "https://notes.example/dashboard/notebooks/7?google_drive=connected" {
			t.Fatalf("answered %d to %s", answer.Code, answer.Header().Get("Location"))
		}
		if cleared := answer.Result().Cookies(); len(cleared) != 1 || cleared[0].MaxAge >= 0 {
			t.Errorf("the attempt's cookie was not cleared: %+v", cleared)
		}
		ids, configs := s.stored(alice)
		if len(ids) != 1 {
			t.Fatalf("rows %v", ids)
		}
		config := configs[0]
		if s.opened(config["refresh_token"]) != "fake-refresh-1" || s.opened(config["token"]) != "fake-access-first" ||
			s.opened(config["client_secret"]) != "fake-client-secret" {
			t.Error("the sealed fields do not open to what Google handed over")
		}
		if config["_token_encrypted"] != true || config["account_email"] != "ada@gmail.test" || config["auth_expired"] != false ||
			config["client_id"] != "123456-web.apps.googleusercontent.com" || config["token_uri"] != s.google.URL+"/token" {
			t.Errorf("stored %v", config)
		}
		var name string
		var indexable, periodic bool
		var minutes int64
		var next time.Time
		if err := s.pool.QueryRow(context.Background(), `SELECT name, is_indexable, periodic_indexing_enabled, indexing_frequency_minutes, next_scheduled_at
			FROM search_source_connectors WHERE id = $1`, ids[0]).Scan(&name, &indexable, &periodic, &minutes, &next); err != nil {
			t.Fatal(err)
		}
		if name != "Google Drive (ada@gmail.test)" || !indexable || !periodic || minutes != 1440 || !next.Equal(s.now.Add(24*time.Hour)) {
			t.Errorf("the row is %q %v %v %d %v", name, indexable, periodic, minutes, next)
		}
		status := s.as(alice, http.MethodGet, ConnectionPath, "", "")
		if want := `{"configured":true,"missing":[],"connector_id":` + jsonID(ids[0]) + `,"account_email":"ada@gmail.test","needs_reconnect":false}`; status.Body.String() != want {
			t.Errorf("status %s", status.Body)
		}
		if his := s.as(bob, http.MethodGet, ConnectionPath, "", ""); !strings.Contains(his.Body.String(), `"connector_id":null`) {
			t.Errorf("the other account's status is %s", his.Body)
		}
	})

	ids, _ := s.stored(alice)
	picker := strings.Replace(PickerPath, "{connector_id}", jsonID(ids[0]), 1)
	disconnect := strings.Replace(DisconnectPath, "{connector_id}", jsonID(ids[0]), 1)

	t.Run("only the owner gets a Picker session, reconnects or disconnects", func(t *testing.T) {
		for _, attempt := range [][2]string{
			{http.MethodGet, picker}, {http.MethodDelete, disconnect},
			{http.MethodGet, ReauthPath + "?connector_id=" + jsonID(ids[0])},
		} {
			if answer := s.as(bob, attempt[0], attempt[1], "", ""); answer.Code != http.StatusNotFound {
				t.Errorf("%s %s as the other account answered %d", attempt[0], attempt[1], answer.Code)
			}
		}
		key := principal.Principal{UserID: alice, Method: principal.MethodPersonalAccessToken}
		s.caller = &key
		if answer := s.send(http.MethodGet, picker, "", ""); answer.Code != http.StatusForbidden {
			t.Errorf("an API key's Picker session answered %d", answer.Code)
		}
		if after, _ := s.stored(alice); len(after) != 1 || len(s.google.revoked) != 0 {
			t.Error("her connection was touched")
		}
		answer := s.as(alice, http.MethodGet, picker, "", "")
		if want := `{"access_token":"picker-token-for-refresh-1","api_key":"browser-key","app_id":"123456"}`; answer.Code != http.StatusOK || answer.Body.String() != want {
			t.Errorf("her Picker session: %d %s", answer.Code, answer.Body)
		}
	})

	t.Run("a grant Google stopped accepting is flagged, and reconnecting replaces it in the same row", func(t *testing.T) {
		testpostgres.Exec(t, s.pool, `UPDATE search_source_connectors SET enabled = false,
			config = config || '{"selected_files": [{"id": "f1"}], "indexing_options": {"incremental_sync": true}}'::jsonb WHERE id = `+jsonID(ids[0]))
		s.google.dead["fake-refresh-1"] = true
		answer := s.as(alice, http.MethodGet, picker, "", "")
		if answer.Code != http.StatusBadRequest || !strings.Contains(answer.Body.String(), `"code":"`+reconnectCode+`"`) {
			t.Errorf("answered %d %s", answer.Code, answer.Body)
		}
		if status := s.as(alice, http.MethodGet, ConnectionPath, "", ""); !strings.Contains(status.Body.String(), `"needs_reconnect":true`) {
			t.Errorf("status %s", status.Body)
		}

		s.google.refreshToken = "fake-refresh-2"
		cookie, state := s.begin(alice, http.MethodGet, ReauthPath+"?connector_id="+jsonID(ids[0])+"&return_url=//evil.example/x", "")
		done := s.as("", http.MethodGet, callbackURL(state, "good-code"), "", cookie)
		// The return address was not a path of this site: the connectors
		// page it is, and never the other host.
		if want := "https://notes.example/dashboard/connectors/callback?connector=google-drive-connector&connectorId=" + jsonID(ids[0]) + "&success=true"; done.Header().Get("Location") != want {
			t.Errorf("landed on %s", done.Header().Get("Location"))
		}
		after, configs := s.stored(alice)
		if len(after) != 1 || after[0] != ids[0] {
			t.Fatalf("rows %v, want the one row %v", after, ids)
		}
		config := configs[0]
		if s.opened(config["refresh_token"]) != "fake-refresh-2" || config["auth_expired"] != false {
			t.Errorf("the new grant was not stored: %v", config)
		}
		// The same Google account: the picks and the options stay.
		if config["selected_files"] == nil || config["indexing_options"] == nil {
			t.Errorf("the picks were lost: %v", config)
		}
		if len(s.google.revoked) != 1 || s.google.revoked[0] != "fake-refresh-1" {
			t.Errorf("revoked at Google: %v", s.google.revoked)
		}
		var enabled bool
		_ = s.pool.QueryRow(context.Background(), `SELECT enabled FROM search_source_connectors WHERE id = $1`, ids[0]).Scan(&enabled)
		if !enabled {
			t.Error("reconnecting left the connection switched off")
		}

		// Another Google account: its picks are not this one's.
		s.google.refreshToken, s.google.email = "fake-refresh-3", "other@gmail.test"
		cookie, state = s.begin(alice, http.MethodGet, AddPath, "")
		s.as("", http.MethodGet, callbackURL(state, "good-code"), "", cookie)
		_, configs = s.stored(alice)
		if configs[0]["selected_files"] != nil || configs[0]["indexing_options"] == nil || configs[0]["account_email"] != "other@gmail.test" {
			t.Errorf("after another account connected: %v", configs[0])
		}
	})

	t.Run("a consent that cannot be used is not saved", func(t *testing.T) {
		before, _ := s.stored(alice)
		beforeConfig := s.as(alice, http.MethodGet, ConnectionPath, "", "").Body.String()
		for outcome, arrange := range map[string]func() string{
			outcomeNoScope:   func() string { s.google.scope = "openid"; return "good-code" },
			outcomeNoRefresh: func() string { s.google.scope, s.google.refreshToken = driveFileScope, ""; return "good-code" },
			outcomeFailed:    func() string { return "a-code-google-never-issued" },
		} {
			code := arrange()
			cookie, state := s.begin(alice, http.MethodPost, ConnectPath, "")
			answer := s.as("", http.MethodGet, callbackURL(state, code), "", cookie)
			if !strings.Contains(answer.Header().Get("Location"), "error="+outcome) {
				t.Errorf("%s: landed on %s", outcome, answer.Header().Get("Location"))
			}
		}
		cookie, state := s.begin(alice, http.MethodPost, ConnectPath, "")
		denied := s.as("", http.MethodGet, CallbackPath+"?state="+state+"&error=access_denied", "", cookie)
		if !strings.Contains(denied.Header().Get("Location"), "error="+outcomeDenied) {
			t.Errorf("a refusal at Google landed on %s", denied.Header().Get("Location"))
		}
		if after, _ := s.stored(alice); len(after) != len(before) || s.as(alice, http.MethodGet, ConnectionPath, "", "").Body.String() != beforeConfig {
			t.Error("an unusable consent changed the connection")
		}
	})

	t.Run("disconnecting ends the grant at Google and keeps the imported files", func(t *testing.T) {
		testpostgres.Exec(t, s.pool, `INSERT INTO documents (id, user_id, title, document_type, content, content_hash, connector_id)
			VALUES (80, '`+alice+`', 'From Drive', 'FILE', 'text', 'hash-80', `+jsonID(ids[0])+`)`)
		answer := s.as(alice, http.MethodDelete, disconnect, "", "")
		if answer.Code != http.StatusOK || answer.Body.String() != `{"revoked":true}` {
			t.Errorf("answered %d %s", answer.Code, answer.Body)
		}
		if after, _ := s.stored(alice); len(after) != 0 {
			t.Error("the connection is still there")
		}
		if last := s.google.revoked[len(s.google.revoked)-1]; last != "fake-refresh-3" {
			t.Errorf("revoked at Google: %v", s.google.revoked)
		}
		var kept int
		_ = s.pool.QueryRow(context.Background(), `SELECT count(*) FROM documents WHERE id = 80 AND connector_id IS NULL`).Scan(&kept)
		if kept != 1 {
			t.Error("the imported document did not stay")
		}
	})
}

func TestADeploymentThatHasNotSetDriveUp(t *testing.T) {
	s := newDriveSite(t, false)
	status := s.as(testpostgres.Alice, http.MethodGet, ConnectionPath, "", "")
	if want := `{"configured":false,"missing":["GOOGLE_OAUTH_CLIENT_ID","GOOGLE_PICKER_APP_ID"],"connector_id":null,"account_email":null,"needs_reconnect":false}`; status.Body.String() != want {
		t.Errorf("status %s", status.Body)
	}
	for _, attempt := range [][2]string{{http.MethodPost, ConnectPath}, {http.MethodGet, AddPath}} {
		answer := s.as(testpostgres.Alice, attempt[0], attempt[1], "", "")
		if answer.Code != http.StatusServiceUnavailable || !strings.Contains(answer.Body.String(), notConfiguredCode) || len(answer.Result().Cookies()) != 0 {
			t.Errorf("%s %s answered %d %s", attempt[0], attempt[1], answer.Code, answer.Body)
		}
	}
}

func TestOnlyAPathOfThisSiteIsReturnedTo(t *testing.T) {
	for path, kept := range map[string]bool{
		"/dashboard/notebooks/7": true, "/dashboard?tab=a": true,
		"": false, "dashboard": false, "//evil.example": false, "https://evil.example": false,
		"/\\evil.example": false, "/a\nb": false, "/" + strings.Repeat("a", 600): false,
	} {
		if got := safeReturnPath(path); (got != "") != kept {
			t.Errorf("safeReturnPath(%q) = %q", path, got)
		}
	}
}

func jsonID(id int64) string {
	text, _ := json.Marshal(id)
	return string(text)
}
