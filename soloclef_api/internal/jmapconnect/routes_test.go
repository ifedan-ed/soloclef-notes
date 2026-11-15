package jmapconnect

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

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
)

const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
	box = "5a5a5a5a-0000-4000-8000-00000000000a"
)

// mailboxes stands in for jmapaccounts.Mail: Ada has one mailbox, the app
// password "fake-right" signs in, and what it was asked is kept.
type mailboxes struct {
	connected []string
	granted   []string
}

func (m *mailboxes) Accounts(_ context.Context, ownerID string) ([]jmapaccounts.Account, error) {
	if ownerID != ada {
		return nil, nil
	}
	return []jmapaccounts.Account{{ID: box, Label: "Stalwart", ServerURL: "https://mail.example.test", Username: "ada@example.test",
		Auth: jmapaccounts.AuthOAuth, CanSend: true, CreatedAt: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}}, nil
}

func (m *mailboxes) Test(_ context.Context, ownerID, accountID string) (jmapaccounts.Probe, error) {
	if ownerID != ada || accountID != box {
		return jmapaccounts.Probe{}, jmapaccounts.ErrNoAccount
	}
	return jmapaccounts.Probe{Mailboxes: 5, CanSend: true}, nil
}

func (m *mailboxes) ConnectAppPassword(_ context.Context, ownerID, label, serverURL, username, password string) (jmapaccounts.Account, error) {
	m.connected = append(m.connected, ownerID+"|"+serverURL+"|"+username)
	if password != "fake-right" {
		return jmapaccounts.Account{}, &jmapmail.Failure{Message: "The mail server refused the sign-in.", SignIn: true}
	}
	return jmapaccounts.Account{ID: box, Label: label, ServerURL: serverURL, Username: username, Auth: jmapaccounts.AuthAppPassword}, nil
}

func (m *mailboxes) ConnectGrant(_ context.Context, ownerID, serverURL, clientID string, metadata jmapaccounts.Metadata, grant jmapaccounts.Grant) (jmapaccounts.Account, error) {
	m.granted = append(m.granted, ownerID+"|"+serverURL+"|"+clientID+"|"+metadata.TokenEndpoint+"|"+grant.AccessToken)
	return jmapaccounts.Account{ID: box}, nil
}

func (m *mailboxes) Disconnect(_ context.Context, ownerID, accountID string) (bool, error) {
	if ownerID != ada || accountID != box {
		return false, jmapaccounts.ErrNoAccount
	}
	return true, nil
}

// server stands in for a Stalwart server's sign-in. It answers on two
// names: mail.example.test, which calls itself so, and
// alias.example.test, whose metadata names mail.example.test (what
// jmapaccounts.OAuth.Discover reports after following it).
type server struct {
	preflight   error
	exchanged   []string
	preflighted []string
}

func (s *server) Discover(_ context.Context, serverURL string) (jmapaccounts.Metadata, error) {
	if serverURL != "https://mail.example.test" && serverURL != "https://alias.example.test" {
		return jmapaccounts.Metadata{}, &jmapaccounts.Refused{Message: "This server does not offer Stalwart's sign-in"}
	}
	return jmapaccounts.Metadata{Server: "https://mail.example.test", Issuer: "https://mail.example.test", AuthorizationEndpoint: "https://mail.example.test/login",
		TokenEndpoint: "https://mail.example.test/auth/token", RevocationEndpoint: "https://mail.example.test/auth/revoke",
		IssuerInResponse: true}, nil
}

func (s *server) Preflight(_ context.Context, serverURL, _ string) error {
	s.preflighted = append(s.preflighted, serverURL)
	return s.preflight
}

func (s *server) Exchange(_ context.Context, tokenEndpoint, clientID, redirectURI, code, verifier string, _ time.Time) (jmapaccounts.Grant, error) {
	s.exchanged = append(s.exchanged, tokenEndpoint+"|"+code+"|"+redirectURI)
	scope := jmapaccounts.MailScope
	if code == "no-mail" {
		scope = "openid"
	}
	return jmapaccounts.Grant{AccessToken: "fake-access", RefreshToken: "fake-refresh", Scope: scope, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

var configured = Settings{ClientID: "https://notes.example.test" + ClientDocumentPath, RedirectURI: "https://notes.example.test" + CallbackPath,
	Name: "Notes", FrontendURL: "https://notes.example.test", SecretKey: "a secret key made up for this test"}

type site struct {
	t       *testing.T
	boxes   *mailboxes
	server  *server
	handler http.Handler
	caller  *principal.Principal
}

func newSite(t *testing.T, settings Settings) *site {
	t.Helper()
	s := &site{t: t, boxes: &mailboxes{}, server: &server{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes := Routes{Mailboxes: s.boxes, OAuth: s.server, Settings: settings, Now: time.Now,
		Errors: errorenvelope.Writer{ReportURL: "https://notes.example.test/contact"}, Logger: logger}
	if err := routes.Check(); err != nil {
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
	// Every body these tests send decodes, so the middleware's other
	// branches are not reached.
	bodies := requestbody.Middleware{Covers: requestbody.CoveringRoutes(BodyRoutes()...)}
	s.handler = requestcontext.Middleware(logger, bodies.Wrap(withCaller))
	return s
}

func (s *site) do(caller *principal.Principal, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	s.t.Helper()
	s.caller = caller
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	if strings.Contains(recorder.Body.String(), "fake-") {
		s.t.Fatalf("%s %s answered a credential: %s", method, path, recorder.Body.String())
	}
	return recorder
}

func session(userID string) *principal.Principal {
	return &principal.Principal{UserID: userID, Method: principal.MethodSession}
}

func apiKey(userID string) *principal.Principal {
	return &principal.Principal{UserID: userID, Method: principal.MethodPersonalAccessToken}
}

func TestAppPasswordMailboxes(t *testing.T) {
	s := newSite(t, Settings{SecretKey: "k"})
	body := `{"server_url":"https://mail.example.test","username":"ada@example.test","app_password":" fake-right ","label":"Mine"}`
	// Connecting stores a credential and makes this process connect: a
	// person in a session only.
	if got := s.do(apiKey(ada), http.MethodPost, AccountsPath, body); got.Code != http.StatusForbidden {
		t.Fatalf("an API key connected a mailbox: %d", got.Code)
	}
	if got := s.do(session(ada), http.MethodPost, AccountsPath, strings.Replace(body, "https://", "http://", 1)); got.Code != http.StatusBadRequest {
		t.Errorf("a plain-http server was taken: %d %s", got.Code, got.Body)
	}
	if got := s.do(session(ada), http.MethodPost, AccountsPath, `{"server_url":"https://mail.example.test"}`); got.Code != http.StatusUnprocessableEntity {
		t.Errorf("a form without its fields: %d", got.Code)
	}
	wrong := s.do(session(ada), http.MethodPost, AccountsPath, strings.Replace(body, "fake-right", "wrong", 1))
	if wrong.Code != http.StatusBadRequest || !strings.Contains(wrong.Body.String(), "refused the sign-in") {
		t.Errorf("a refused sign-in: %d %s", wrong.Code, wrong.Body)
	}
	added := s.do(session(ada), http.MethodPost, AccountsPath, body)
	var account shownAccount
	if added.Code != http.StatusCreated || json.Unmarshal(added.Body.Bytes(), &account) != nil || account.Auth != jmapaccounts.AuthAppPassword || account.Label != "Mine" {
		t.Fatalf("adding = %d %s", added.Code, added.Body)
	}
	listed := s.do(apiKey(ada), http.MethodGet, AccountsPath, "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"can_send":true`) {
		t.Errorf("list = %d %s", listed.Code, listed.Body)
	}
	tested := s.do(session(ada), http.MethodPost, strings.Replace(AccountTestPath, "{account_id}", box, 1), "")
	if tested.Code != http.StatusOK || !strings.Contains(tested.Body.String(), `"mailbox_count":5`) {
		t.Errorf("test = %d %s", tested.Code, tested.Body)
	}
	if got := s.do(session(ben), http.MethodDelete, strings.Replace(AccountPath, "{account_id}", box, 1), ""); got.Code != http.StatusNotFound {
		t.Errorf("Ben removed Ada's mailbox: %d", got.Code)
	}
	removed := s.do(session(ada), http.MethodDelete, strings.Replace(AccountPath, "{account_id}", box, 1), "")
	if removed.Code != http.StatusOK || removed.Body.String() != `{"revoked": true}` && removed.Body.String() != `{"revoked":true}` {
		t.Errorf("remove = %d %s", removed.Code, removed.Body)
	}
}

func TestStalwartSignIn(t *testing.T) {
	unset := newSite(t, Settings{SecretKey: "k"})
	if got := unset.do(session(ada), http.MethodPost, ConnectPath, `{"server_url":"https://mail.example.test"}`); got.Code != http.StatusServiceUnavailable ||
		!strings.Contains(got.Body.String(), notConfiguredCode) {
		t.Fatalf("an unconfigured deployment: %d %s", got.Code, got.Body)
	}

	s := newSite(t, configured)
	s.server.preflight = &jmapaccounts.Refused{Message: "This Stalwart server does not accept this app's sign-in yet"}
	if got := s.do(session(ada), http.MethodPost, ConnectPath, `{"server_url":"https://mail.example.test"}`); got.Code != http.StatusBadRequest ||
		!strings.Contains(got.Body.String(), "does not accept this app") {
		t.Fatalf("a server that does not accept the app: %d %s", got.Code, got.Body)
	}
	s.server.preflight = nil
	started := s.do(session(ada), http.MethodPost, ConnectPath, `{"server_url":"https://mail.example.test/some/page","return_path":"/dashboard/settings/connectors"}`)
	var answer struct {
		AuthURL string `json:"auth_url"`
	}
	if started.Code != http.StatusOK || json.Unmarshal(started.Body.Bytes(), &answer) != nil {
		t.Fatalf("start = %d %s", started.Code, started.Body)
	}
	sent, _ := url.Parse(answer.AuthURL)
	query := sent.Query()
	if sent.Host != "mail.example.test" || query.Get("client_id") != configured.ClientID || query.Get("redirect_uri") != configured.RedirectURI ||
		query.Get("code_challenge_method") != "S256" || query.Get("scope") != jmapaccounts.MailScope || query.Get("state") == "" {
		t.Fatalf("the sign-in page = %s", answer.AuthURL)
	}
	cookies := started.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Path != CallbackPath || strings.Contains(cookies[0].Value, query.Get("state")) {
		t.Fatalf("the attempt's cookie = %+v", cookies)
	}
	callback := func(query string, cookie ...*http.Cookie) *httptest.ResponseRecorder {
		return s.do(nil, http.MethodGet, CallbackPath+"?"+query, "", cookie...)
	}
	landed := func(got *httptest.ResponseRecorder) string {
		location, _ := url.Parse(got.Header().Get("Location"))
		return location.Query().Get(outcomeParam)
	}
	state := url.QueryEscape(query.Get("state"))
	for name, try := range map[string]*httptest.ResponseRecorder{
		"no cookie (another browser)": callback("code=c&state=" + state + "&iss=https%3A%2F%2Fmail.example.test"),
		"another state":               callback("code=c&state=x&iss=https%3A%2F%2Fmail.example.test", cookies[0]),
		"another issuer":              callback("code=c&state="+state+"&iss=https%3A%2F%2Fevil.example", cookies[0]),
	} {
		if try.Code != http.StatusSeeOther || landed(try) != outcomeExpired {
			t.Errorf("%s: %d %s", name, try.Code, try.Header().Get("Location"))
		}
	}
	if len(s.server.exchanged) != 0 {
		t.Fatalf("a code was exchanged for a callback that was not this browser's: %v", s.server.exchanged)
	}
	if got := callback("error=access_denied&state="+state+"&iss=https%3A%2F%2Fmail.example.test", cookies[0]); landed(got) != "denied" {
		t.Errorf("declined: %s", got.Header().Get("Location"))
	}
	if got := callback("code=no-mail&state="+state+"&iss=https%3A%2F%2Fmail.example.test", cookies[0]); landed(got) != outcomeNoMail {
		t.Errorf("a grant without mail: %s", got.Header().Get("Location"))
	}
	done := callback("code=good&state="+state+"&iss=https%3A%2F%2Fmail.example.test", cookies[0])
	if landed(done) != outcomeConnected || !strings.HasPrefix(done.Header().Get("Location"), "https://notes.example.test/dashboard/settings/connectors?") {
		t.Fatalf("connected: %s", done.Header().Get("Location"))
	}
	// The grant is Ada's (the cookie said so), traded at the token endpoint
	// the start read from the server, never one the callback named.
	if len(s.boxes.granted) != 1 || s.boxes.granted[0] != ada+"|https://mail.example.test|"+configured.ClientID+"|https://mail.example.test/auth/token|fake-access" {
		t.Fatalf("granted = %v", s.boxes.granted)
	}
}

// A person who typed another name of the server signs in, is asked
// about, and connects with the address the server calls itself; the
// answer says which.
func TestStalwartSignInWithAnotherNameOfTheServer(t *testing.T) {
	s := newSite(t, configured)
	started := s.do(session(ada), http.MethodPost, ConnectPath, `{"server_url":"https://alias.example.test"}`)
	var answer struct {
		AuthURL   string `json:"auth_url"`
		ServerURL string `json:"server_url"`
	}
	if started.Code != http.StatusOK || json.Unmarshal(started.Body.Bytes(), &answer) != nil {
		t.Fatalf("start = %d %s", started.Code, started.Body)
	}
	sent, _ := url.Parse(answer.AuthURL)
	if answer.ServerURL != "https://mail.example.test" || sent.Host != "mail.example.test" {
		t.Fatalf("started with %q at %s", answer.ServerURL, answer.AuthURL)
	}
	if len(s.server.preflighted) != 1 || s.server.preflighted[0] != "https://mail.example.test" {
		t.Fatalf("the preflight asked %v", s.server.preflighted)
	}
	state := url.QueryEscape(sent.Query().Get("state"))
	done := s.do(nil, http.MethodGet, CallbackPath+"?code=good&state="+state+"&iss=https%3A%2F%2Fmail.example.test", "", started.Result().Cookies()...)
	if location, _ := url.Parse(done.Header().Get("Location")); location.Query().Get(outcomeParam) != outcomeConnected {
		t.Fatalf("callback = %s", done.Header().Get("Location"))
	}
	// The attempt held the server's own address: the code was traded and
	// the mailbox stored there, never at the typed name.
	if len(s.server.exchanged) != 1 || !strings.HasPrefix(s.server.exchanged[0], "https://mail.example.test/auth/token|") {
		t.Fatalf("exchanged = %v", s.server.exchanged)
	}
	if len(s.boxes.granted) != 1 || !strings.HasPrefix(s.boxes.granted[0], ada+"|https://mail.example.test|") {
		t.Fatalf("granted = %v", s.boxes.granted)
	}
}

func TestTheClientDocument(t *testing.T) {
	s := newSite(t, configured)
	got := s.do(nil, http.MethodGet, ClientDocumentPath, "")
	var document map[string]any
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &document) != nil {
		t.Fatalf("document = %d %s", got.Code, got.Body)
	}
	uris, _ := document["redirect_uris"].([]any)
	if document["client_id"] != configured.ClientID || len(uris) != 1 || uris[0] != configured.RedirectURI ||
		document["token_endpoint_auth_method"] != "none" || document["scope"] != jmapaccounts.MailScope {
		t.Fatalf("document = %v", document)
	}
	if _, secret := document["client_secret"]; secret {
		t.Fatal("the document carries a secret")
	}
}

func TestSettingsSetUpHalfwayAreRefusedAtStart(t *testing.T) {
	for name, settings := range map[string]Settings{
		"client id only":        {ClientID: configured.ClientID, SecretKey: "k"},
		"another document path": {ClientID: "https://notes.example.test/client.json", RedirectURI: configured.RedirectURI, Name: "n", FrontendURL: "f", SecretKey: "k"},
		"another callback":      {ClientID: configured.ClientID, RedirectURI: "https://notes.example.test/cb", Name: "n", FrontendURL: "f", SecretKey: "k"},
		"plain http":            {ClientID: strings.Replace(configured.ClientID, "https", "http", 1), RedirectURI: configured.RedirectURI, Name: "n", FrontendURL: "f", SecretKey: "k"},
	} {
		if settings.Check() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
