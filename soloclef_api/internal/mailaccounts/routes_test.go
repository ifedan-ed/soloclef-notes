package mailaccounts

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
const databaseVariable = "SOLOCLEF_API_TEST_MAILACCOUNTS_DATABASE"

const secretKey = "a secret key made up for this test"

// fakeMail stands in for the mail servers: one host is private, one
// password is right, and what it was asked to sign in with is kept.
type fakeMail struct {
	mu   sync.Mutex
	seen []Settings
}

func (f *fakeMail) Reachable(_ context.Context, host string) bool {
	return host != "inside.example.test"
}

func (f *fakeMail) Folders(_ context.Context, settings Settings) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, settings)
	if settings.Password != "fake-right-password" && settings.AccessToken != "fake-ms-access" {
		return 0, &ConnectionError{Message: "The mail server refused the sign-in: NO"}
	}
	return 4, nil
}

// fakeMicrosoft stands in for internal/microsoftmail's sign-in: a mailbox
// flagged for reconnecting is refused in words, any other gets a token.
type fakeMicrosoft struct {
	// owners is who each call was made for.
	owners []string
}

func (f *fakeMicrosoft) Mailbox(_ context.Context, ownerID string, record []byte) (Settings, error) {
	f.owners = append(f.owners, ownerID)
	var held stored
	_ = json.Unmarshal(record, &held)
	if held.NeedsReconnect {
		return Settings{}, &ConnectionError{Message: "Reconnect this mailbox"}
	}
	return Settings{Host: "outlook.office365.com", Port: 993, UseTLS: true, Username: held.Username, AccessToken: "fake-ms-access"}, nil
}

type mailSite struct {
	t       *testing.T
	pool    *pgxpool.Pool
	mail    *fakeMail
	ms      *fakeMicrosoft
	handler http.Handler
	caller  principal.Principal
}

func newMailSite(t *testing.T) *mailSite {
	t.Helper()
	s := &mailSite{t: t, pool: testpostgres.Open(t, databaseVariable), mail: &fakeMail{}, ms: &fakeMicrosoft{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes := Routes{Rows: Store{Pool: s.pool}, Mail: s.mail, Microsoft: s.ms, SecretKey: secretKey,
		Now:    func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) },
		Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"}, Logger: logger}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(api)
	withCaller := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.Adapter().ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), s.caller)))
	})
	bodies := requestbody.Middleware{
		Covers: requestbody.CoveringRoutes(BodyRoutes()...),
	}
	s.handler = requestcontext.Middleware(logger, bodies.Wrap(withCaller))
	return s
}

func (s *mailSite) as(userID, method, path, body string) (int, string) {
	s.t.Helper()
	s.caller = principal.Principal{UserID: userID, Method: principal.MethodSession}
	return s.send(method, path, body)
}

func (s *mailSite) send(method, path, body string) (int, string) {
	s.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	answered := recorder.Body.String()
	// No answer holds a password or a sealed one ("gAAAA…").
	if strings.Contains(answered, "fake-") || strings.Contains(answered, "gAAAA") {
		s.t.Fatalf("%s %s answered a credential: %s", method, path, answered)
	}
	return recorder.Code, answered
}

// accounts is the owner's stored list, as the row holds it.
func (s *mailSite) accounts(userID string) string {
	s.t.Helper()
	var text string
	if err := s.pool.QueryRow(context.Background(), `SELECT coalesce(string_agg(config::text, ' | ' ORDER BY id), '')
		FROM search_source_connectors WHERE user_id = $1 AND connector_type = 'EMAIL_IMAP_CONNECTOR'`, userID).Scan(&text); err != nil {
		s.t.Fatal(err)
	}
	return text
}

func mailbox(label, password string) string {
	return `{"label": "` + label + `", "host": "imap.example.test", "username": "ada@example.test", "app_password": "` + password + `"}`
}

func accountPath(id string) string { return strings.Replace(AccountPath, "{account_id}", id, 1) }
func testPath(id string) string    { return strings.Replace(AccountTestPath, "{account_id}", id, 1) }

func TestAnAccountsMailboxes(t *testing.T) {
	s := newMailSite(t)
	const alice, bob = testpostgres.Alice, testpostgres.Bob

	status, answered := s.as(alice, http.MethodPost, AccountsPath, mailbox("Work", "  fake-right-password "))
	var added shownAccount
	if status != http.StatusCreated || json.Unmarshal([]byte(answered), &added) != nil || added.ID == "" {
		t.Fatalf("adding answered %d %s", status, answered)
	}
	// The defaults: port 993, TLS on; and the nine fields, no password.
	if added.Port != 993 || !added.UseTLS || added.Label != "Work" || added.Auth != "password" || added.CreatedAt == nil ||
		*added.CreatedAt != "2026-11-12T09:00:00.000000+00:00" {
		t.Errorf("added %+v", added)
	}

	t.Run("the password is stored sealed, in the form every stored credential has", func(t *testing.T) {
		raw := s.accounts(alice)
		if strings.Contains(raw, "fake-right-password") {
			t.Fatalf("the password is stored in the clear: %s", raw)
		}
		var config struct {
			Accounts []stored `json:"accounts"`
		}
		if err := json.Unmarshal([]byte(raw), &config); err != nil || len(config.Accounts) != 1 || !config.Accounts[0].Encrypted {
			t.Fatalf("stored %s (%v)", raw, err)
		}
		opened, err := storedtoken.Open(secretKey, config.Accounts[0].AppPassword)
		if err != nil || opened != "fake-right-password" {
			t.Errorf("the sealed password opens as %q, %v", opened, err)
		}
	})

	t.Run("the other account sees, tests and removes nothing of hers", func(t *testing.T) {
		before := s.accounts(alice)
		if _, his := s.as(bob, http.MethodGet, AccountsPath, ""); his != "[]" {
			t.Errorf("his list is %s", his)
		}
		if status, _ := s.as(bob, http.MethodPost, testPath(added.ID), ""); status != http.StatusNotFound {
			t.Errorf("his test of her mailbox answered %d", status)
		}
		if status, _ := s.as(bob, http.MethodDelete, accountPath(added.ID), ""); status != http.StatusNotFound {
			t.Errorf("his removal of her mailbox answered %d", status)
		}
		if len(s.mail.seen) != 0 {
			t.Error("her stored password was used for him")
		}
		if after := s.accounts(alice); after != before {
			t.Errorf("her mailboxes changed:\n before %s\n after  %s", before, after)
		}
	})

	t.Run("a saved mailbox is tested with its stored password", func(t *testing.T) {
		status, answered := s.as(alice, http.MethodPost, testPath(added.ID), "")
		if status != http.StatusOK || answered != `{"status":"ok","folder_count":4,"message":null}` {
			t.Fatalf("answered %d %s", status, answered)
		}
		used := s.mail.seen[len(s.mail.seen)-1]
		if used.Password != "fake-right-password" || used.Host != "imap.example.test" || used.Port != 993 || !used.UseTLS {
			t.Errorf("signed in with %+v", used)
		}
	})

	t.Run("settings are tested before they are saved, and nothing is stored", func(t *testing.T) {
		before := s.accounts(alice)
		status, answered := s.as(alice, http.MethodPost, TestPath, mailbox("", "wrong"))
		if status != http.StatusOK || answered != `{"status":"error","folder_count":null,"message":"The mail server refused the sign-in: NO"}` {
			t.Errorf("answered %d %s", status, answered)
		}
		if after := s.accounts(alice); after != before {
			t.Error("a test stored something")
		}
	})

	t.Run("what is refused", func(t *testing.T) {
		before := s.accounts(alice)
		for name, attempt := range map[string]struct {
			body   string
			status int
		}{
			"a host inside the network": {`{"host": "inside.example.test", "username": "u", "app_password": "p"}`, http.StatusBadRequest},
			"no password":               {`{"host": "imap.example.test", "username": "u"}`, http.StatusUnprocessableEntity},
			"a blank password":          {`{"host": "imap.example.test", "username": "u", "app_password": "   "}`, http.StatusUnprocessableEntity},
			"port 0":                    {`{"host": "imap.example.test", "port": 0, "username": "u", "app_password": "p"}`, http.StatusUnprocessableEntity},
			"port 70000":                {`{"host": "imap.example.test", "port": 70000, "username": "u", "app_password": "p"}`, http.StatusUnprocessableEntity},
		} {
			if status, answered := s.as(alice, http.MethodPost, AccountsPath, attempt.body); status != attempt.status {
				t.Errorf("%s: answered %d %s, want %d", name, status, answered, attempt.status)
			}
		}
		// Storing a password and connecting somewhere is a person's
		// decision: an API key is refused on the add and both tests.
		s.caller = principal.Principal{UserID: alice, Method: principal.MethodPersonalAccessToken}
		for _, attempt := range [][2]string{{AccountsPath, mailbox("By key", "p")}, {TestPath, mailbox("", "p")}, {testPath(added.ID), ""}} {
			if status, _ := s.send(http.MethodPost, attempt[0], attempt[1]); status != http.StatusForbidden {
				t.Errorf("an API key's POST %s answered %d", attempt[0], status)
			}
		}
		// It may list, though.
		if status, listed := s.send(http.MethodGet, AccountsPath, ""); status != http.StatusOK || !strings.Contains(listed, added.ID) {
			t.Errorf("an API key's list answered %d %s", status, listed)
		}
		if after := s.accounts(alice); after != before {
			t.Error("a refused request stored something")
		}
	})

	t.Run("adds made at once all land, in one connection", func(t *testing.T) {
		// One site per goroutine would share nothing; the store is what is
		// under test, so it is called directly.
		store := Store{Pool: s.pool}
		var wait sync.WaitGroup
		for index := 0; index < 8; index++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				record, _ := json.Marshal(stored{ID: "concurrent-" + string(rune('a'+index)), Host: "h", Username: "u"})
				if err := store.Add(context.Background(), bob, record); err != nil {
					t.Error(err)
				}
			}()
		}
		wait.Wait()
		var rows, mailboxes int
		if err := s.pool.QueryRow(context.Background(), `SELECT count(*), coalesce(sum(jsonb_array_length(config -> 'accounts')), 0)
			FROM search_source_connectors WHERE user_id = $1 AND connector_type = 'EMAIL_IMAP_CONNECTOR'`, bob).Scan(&rows, &mailboxes); err != nil {
			t.Fatal(err)
		}
		if rows != 1 || mailboxes != 8 {
			t.Errorf("%d connections holding %d mailboxes, want 1 holding 8", rows, mailboxes)
		}
	})

	t.Run("removing one keeps the others, in order; a Microsoft mailbox is listed, and tested with a token", func(t *testing.T) {
		_, second := s.as(alice, http.MethodPost, AccountsPath, mailbox("Home", "fake-right-password"))
		var home shownAccount
		_ = json.Unmarshal([]byte(second), &home)
		testpostgres.Exec(t, s.pool, `UPDATE search_source_connectors
			SET config = jsonb_set(config, '{accounts}', config -> 'accounts' ||
			    '[{"id": "ms-1", "label": "Outlook", "host": "outlook.office365.com", "port": 993, "username": "ada@example.test",
			       "auth": "microsoft", "refresh_token": "fake-ms-grant", "_encrypted": true, "needs_reconnect": true}]'::jsonb)
			WHERE user_id = '`+alice+`' AND connector_type = 'EMAIL_IMAP_CONNECTOR'`)
		if status, _ := s.as(alice, http.MethodDelete, accountPath(added.ID), ""); status != http.StatusNoContent {
			t.Fatalf("removing answered %d", status)
		}
		_, listed := s.as(alice, http.MethodGet, AccountsPath, "")
		var shown []shownAccount
		if err := json.Unmarshal([]byte(listed), &shown); err != nil || len(shown) != 2 || shown[0].ID != home.ID ||
			shown[1].ID != "ms-1" || shown[1].Auth != "microsoft" || !shown[1].NeedsReconnect {
			t.Errorf("listed %s (%v)", listed, err)
		}
		if status, _ := s.as(alice, http.MethodDelete, accountPath(added.ID), ""); status != http.StatusNotFound {
			t.Errorf("removing it twice answered %d", status)
		}
		seen := len(s.mail.seen)
		status, answered := s.as(alice, http.MethodPost, testPath("ms-1"), "")
		// Flagged for reconnecting: the sign-in's own sentence, and no
		// mail server is asked.
		if status != http.StatusOK || !strings.Contains(answered, "Reconnect this mailbox") || len(s.mail.seen) != seen {
			t.Errorf("testing a Microsoft mailbox that needs reconnecting answered %d %s", status, answered)
		}
		testpostgres.Exec(t, s.pool, `UPDATE search_source_connectors
			SET config = jsonb_set(config, '{accounts,1,needs_reconnect}', 'false'::jsonb)
			WHERE user_id = '`+alice+`' AND connector_type = 'EMAIL_IMAP_CONNECTOR'`)
		status, answered = s.as(alice, http.MethodPost, testPath("ms-1"), "")
		last := s.mail.seen[len(s.mail.seen)-1]
		if status != http.StatusOK || !strings.Contains(answered, `"status":"ok"`) ||
			last.AccessToken != "fake-ms-access" || last.Password != "" || last.Username != "ada@example.test" ||
			s.ms.owners[len(s.ms.owners)-1] != alice {
			t.Errorf("testing a Microsoft mailbox answered %d %s, signing in with %+v", status, answered, last)
		}
	})
}
