package apphandoff

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

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/appreturn"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/driveconnect"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/drivesync"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/microsoftmail"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice/parserchoicetest"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// databaseVariable names a throwaway Postgres for this package's tests.
// **Every account in it is deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_APPHANDOFF_DATABASE"

const secretKey = "a secret key made up for this test"

// google is Google's token, about and revoke endpoints: a code exchange
// must come with the verifier behind the consent page's challenge.
type google struct {
	*httptest.Server
	mu        sync.Mutex
	challenge string
	refresh   string
}

func newGoogle(t *testing.T) *google {
	t.Helper()
	g := &google{refresh: "fake-refresh-1"}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/token":
			if r.Form.Get("grant_type") == "authorization_code" {
				digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if r.Form.Get("code") != "good-code" || base64.RawURLEncoding.EncodeToString(digest[:]) != g.challenge {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error": "invalid_grant"}`)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fake-access-first",
					"refresh_token": g.refresh, "scope": "https://www.googleapis.com/auth/drive.file", "expires_in": 3600})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "picker-token-for-" + r.Form.Get("refresh_token"), "expires_in": 3600})
		case "/about":
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]string{"emailAddress": "ada@gmail.test"}})
		case "/revoke":
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

type queue struct{}

func (queue) Enqueue(context.Context, produce.Message) (string, error) { return "id", nil }

// flowSite is the hand-off over the real packages it calls and a real
// database: driveconnect (with Google stood in for), drivesync, the
// accounts and the notebooks.
type flowSite struct {
	t       *testing.T
	pool    *pgxpool.Pool
	google  *google
	handler http.Handler
}

func newFlowSite(t *testing.T) *flowSite {
	t.Helper()
	s := &flowSite{t: t, pool: testpostgres.Open(t, databaseVariable), google: newGoogle(t)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	errors := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	secrets := Secrets{Store: newMemory()}

	drive, err := driveconnect.NewRoutes(driveconnect.Store{Pool: s.pool}, driveconnect.Settings{
		ClientID: "123456-web.apps.googleusercontent.com", ClientSecret: "fake-client-secret",
		RedirectURI: "https://notes.example" + driveconnect.CallbackPath, PickerAPIKey: "browser-key",
		FrontendURL: "https://notes.example/", SecretKey: secretKey,
		AuthURL: "https://accounts.google.test/auth", TokenURL: s.google.URL + "/token",
		RevokeURL: s.google.URL + "/revoke", AboutURL: s.google.URL + "/about",
	}, http.DefaultTransport, time.Now, errors, logger)
	if err != nil {
		t.Fatal(err)
	}
	drive.AppHold = secrets.Holder(appreturn.DriveConnect)
	// Microsoft is not set up here: its half is the same code path, held
	// and saved through the same Secrets, and covered by routes_test.go.
	microsoft, err := microsoftmail.NewRoutes(microsoftmail.Store{Pool: s.pool}, microsoftmail.Settings{SecretKey: secretKey},
		http.DefaultTransport, time.Now, errors, logger)
	if err != nil {
		t.Fatal(err)
	}
	imports := drivesync.Routes{Rows: drivesync.Store{Pool: s.pool}, Parsers: parserchoicetest.Registry(t, parse.Local),
		Queue: queue{}, Configured: true, Errors: errors, Logger: logger}
	handoff := Routes{Secrets: secrets, Drive: drive, Microsoft: microsoft, Imports: imports,
		Notebooks: drivesync.Store{Pool: s.pool}, Accounts: principal.PostgresDirectory{Pool: s.pool, Logger: logger},
		Errors: errors, Logger: logger}
	if err := handoff.Check(); err != nil {
		t.Fatal(err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	drive.Register(api)
	handoff.Register(api)
	s.handler = requestcontext.Middleware(logger, api.Adapter())
	return s
}

func (s *flowSite) call(method, path, body, caller string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	s.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	if caller != "" {
		request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: caller, Method: principal.MethodSession}))
	}
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	return recorder
}

func (s *flowSite) count(query string, args ...any) int {
	s.t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}

func (s *flowSite) startedPath(caller, body string) string {
	s.t.Helper()
	got := s.call(http.MethodPost, StartPath, body, caller)
	if got.Code != http.StatusOK {
		s.t.Fatalf("start %s as %s: %d %s", body, caller, got.Code, got.Body)
	}
	var answer started
	_ = json.Unmarshal(got.Body.Bytes(), &answer)
	return answer.Path
}

// A Picker hand-off imports into its own account's notebook through its
// own account's connection, and another account can name neither.
func TestThePickerImportsIntoItsOwnAccountOnly(t *testing.T) {
	s := newFlowSite(t)
	testpostgres.Exec(t, s.pool, `
INSERT INTO search_source_connectors (id, user_id, name, connector_type, is_indexable, config) VALUES
 (1, '`+testpostgres.Alice+`', 'Google Drive', 'GOOGLE_DRIVE_CONNECTOR', true, '{"refresh_token": "alice-refresh", "auth_expired": false}'),
 (2, '`+testpostgres.Bob+`', 'Google Drive', 'GOOGLE_DRIVE_CONNECTOR', true, '{"refresh_token": "bob-refresh", "auth_expired": false}');
INSERT INTO notebooks (id, user_id, title) VALUES (10, '`+testpostgres.Alice+`', 'Alice''s'), (20, '`+testpostgres.Bob+`', 'Bob''s')`)

	// Bob naming Alice's notebook is the 404 of a notebook never made.
	got := s.call(http.MethodPost, StartPath, `{"purpose":"drive_picker","notebook_id":10}`, testpostgres.Bob)
	if status, _ := refusal(t, got); status != http.StatusNotFound {
		t.Fatalf("Bob into Alice's notebook: %d %s", status, got.Body)
	}

	page := s.call(http.MethodGet, s.startedPath(testpostgres.Bob, `{"purpose":"drive_picker","notebook_id":20}`), "", "")
	found := ticketInPage.FindStringSubmatch(page.Body.String())
	if page.Code != http.StatusOK || found == nil || !strings.Contains(page.Body.String(), "picker-token-for-bob-refresh") {
		t.Fatalf("Bob's page: %d %s", page.Code, page.Body)
	}
	files := `[{"id":"1BobFileIdFromDrive","name":"Plan.pdf","mime_type":"application/pdf"}]`
	imported := s.call(http.MethodPost, ImportPath, `{"ticket":"`+found[1]+`","files":`+files+`}`, "")
	if imported.Code != http.StatusOK || !strings.Contains(imported.Body.String(), `"queued":1`) {
		t.Fatalf("Bob's import: %d %s", imported.Code, imported.Body)
	}
	if bobs := s.count(`SELECT count(*) FROM notebook_sources WHERE notebook_id = 20 AND user_id = $1`, testpostgres.Bob); bobs != 1 {
		t.Fatalf("Bob's notebook has %d sources", bobs)
	}
	if theirs := s.count(`SELECT count(*) FROM documents WHERE user_id = $1`, testpostgres.Alice); theirs != 0 {
		t.Fatalf("Bob's pick made %d documents of Alice's", theirs)
	}
	if aliceNotebook := s.count(`SELECT count(*) FROM notebook_sources WHERE notebook_id = 10`); aliceNotebook != 0 {
		t.Fatalf("Alice's notebook gained %d sources", aliceNotebook)
	}
}

// consent follows an app's Drive connect from its page to Google's
// callback, as the phone's browser sheet would, and answers where the
// callback sent the sheet.
func (s *flowSite) consent(caller string) *url.URL {
	s.t.Helper()
	page := s.call(http.MethodGet, s.startedPath(caller, `{"purpose":"drive_connect","code_challenge":"`+challengeOf(verifier)+`"}`), "", "")
	if page.Code != http.StatusSeeOther {
		s.t.Fatalf("the page: %d %s", page.Code, page.Body)
	}
	consent, err := url.Parse(page.Header().Get("Location"))
	if err != nil || consent.Host != "accounts.google.test" {
		s.t.Fatalf("not Google's consent page: %q", page.Header().Get("Location"))
	}
	s.google.mu.Lock()
	s.google.challenge = consent.Query().Get("code_challenge")
	s.google.mu.Unlock()
	cookies := page.Result().Cookies()
	callback := s.call(http.MethodGet, driveconnect.CallbackPath+"?code=good-code&state="+url.QueryEscape(consent.Query().Get("state")), "", "", cookies...)
	back, err := url.Parse(callback.Header().Get("Location"))
	if callback.Code != http.StatusSeeOther || err != nil {
		s.t.Fatalf("the callback: %d %q", callback.Code, callback.Header().Get("Location"))
	}
	return back
}

// An app's Drive connect saves nothing at Google's callback, and what it
// holds is saved only by the account that asked, with its verifier.
func TestAnAppsDriveIsSavedOnlyWhenItsOwnAppConfirms(t *testing.T) {
	s := newFlowSite(t)
	back := s.consent(testpostgres.Alice)
	if back.Scheme != "soloclef" || back.Host != "connectors" || back.Query().Get("status") != appreturn.StatusConfirm ||
		back.Query().Get("purpose") != appreturn.DriveConnect || back.Query().Get("completion") == "" {
		t.Fatalf("the callback sent the sheet to %s", back)
	}
	if held := s.count(`SELECT count(*) FROM search_source_connectors`); held != 0 {
		t.Fatalf("the callback saved %d connections", held)
	}

	// Bob holding the link's completion (and even the verifier) saves nothing.
	refused := s.call(http.MethodPost, CompletePath, completeBody(back.Query().Get("completion"), verifier), testpostgres.Bob)
	if status, code := refusal(t, refused); status != http.StatusForbidden || code != codeWrongAccount {
		t.Fatalf("Bob's completion: %d %s", status, code)
	}
	if held := s.count(`SELECT count(*) FROM search_source_connectors`); held != 0 {
		t.Fatalf("Bob's try saved %d connections", held)
	}

	back = s.consent(testpostgres.Alice)
	saved := s.call(http.MethodPost, CompletePath, completeBody(back.Query().Get("completion"), verifier), testpostgres.Alice)
	if saved.Code != http.StatusOK {
		t.Fatalf("Alice's completion: %d %s", saved.Code, saved.Body)
	}
	if hers := s.count(`SELECT count(*) FROM search_source_connectors WHERE user_id = $1 AND connector_type = 'GOOGLE_DRIVE_CONNECTOR'
		AND config->>'account_email' = 'ada@gmail.test'`, testpostgres.Alice); hers != 1 {
		t.Fatalf("Alice has %d Drive connections", hers)
	}
	if his := s.count(`SELECT count(*) FROM search_source_connectors WHERE user_id = $1`, testpostgres.Bob); his != 0 {
		t.Fatalf("Bob has %d connections", his)
	}
}
