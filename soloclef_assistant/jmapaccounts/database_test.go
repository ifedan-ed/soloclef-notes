package jmapaccounts

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
)

// The opt-in test below asks a real Postgres what only it can answer:
// that a refresh holds the row's lock, so two refreshes that meet present
// the refresh token once, and that every statement names the owner. It is
// skipped unless the variable names a throwaway database at the schema
// soloclef_schema's `migrate up` makes; it empties it.
//
//	SOLOCLEF_API_TEST_JMAPACCOUNTS_DATABASE=postgres://check:check@127.0.0.1:39827/check \
//	  go test ./jmapaccounts/ -run OverARealDatabase -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_JMAPACCOUNTS_DATABASE"

const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
)

// handlerNetwork reaches an in-process handler for every server: the test
// stands in for Stalwart's token endpoint.
type handlerNetwork struct{ handler http.Handler }

func (h handlerNetwork) Client(string) (*http.Client, error) {
	return &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		h.handler.ServeHTTP(recorder, request)
		return recorder.Result(), nil
	})}, nil
}

type roundTripper func(*http.Request) (*http.Response, error)

func (r roundTripper) RoundTrip(request *http.Request) (*http.Response, error) { return r(request) }

// rotatingServer is Stalwart's token endpoint as its docs describe it: a
// refresh replaces the refresh token, and presenting a replaced one
// deletes the grant.
type rotatingServer struct {
	mu       sync.Mutex
	current  string
	dead     bool
	refreshs atomic.Int32
	revoked  []string
}

func (s *rotatingServer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	_ = request.ParseForm()
	s.mu.Lock()
	defer s.mu.Unlock()
	switch request.URL.Path {
	case "/auth/token":
		s.refreshs.Add(1)
		// Slow enough that a second refresh, were it not held back by the
		// lock, would arrive while this one is under way.
		time.Sleep(50 * time.Millisecond)
		if s.dead || request.Form.Get("refresh_token") != s.current || request.Form.Get("client_id") != "https://notes.example.org/client.json" {
			s.dead = true
			response.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(response, `{"error":"invalid_grant"}`)
			return
		}
		next := s.current + "+"
		s.current = next
		_, _ = io.WriteString(response, `{"access_token":"access-`+next+`","refresh_token":"`+next+`","expires_in":3600,"token_type":"Bearer","scope":"urn:ietf:params:oauth:scope:mail"}`)
	case "/auth/revoke":
		s.revoked = append(s.revoked, request.Form.Get("token"))
	}
}

func TestTheMailboxRowsOverARealDatabase(t *testing.T) {
	address := os.Getenv(databaseVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `TRUNCATE "user" CASCADE;
		INSERT INTO "user" (id, email) VALUES ('`+ada+`', 'ada@example.test'), ('`+ben+`', 'ben@example.test');`); err != nil {
		t.Fatal(err)
	}
	seal, err := NewSeal("deployment-secret")
	if err != nil {
		t.Fatal(err)
	}
	server := &rotatingServer{current: "r0"}
	now := time.Now()
	signIn := SignIn{Rows: Store{Pool: pool}, Seal: seal, OAuth: OAuth{Network: handlerNetwork{server}, Timeout: 5 * time.Second},
		Now: func() time.Time { return now }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := signIn.Check(); err != nil {
		t.Fatal(err)
	}
	store := Store{Pool: pool}

	// Ada connects by OAuth, with an access token that is about to lapse.
	sealed, err := seal.Close(ada, Secrets{RefreshToken: "r0", AccessToken: "old", AccessExpires: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	account, previous, err := store.Save(ctx, ada, NewMailbox{ServerURL: "https://mail.example.test", Username: "ada@example.test",
		Auth: AuthOAuth, OAuthClientID: "https://notes.example.org/client.json", TokenEndpoint: "https://mail.example.test/auth/token",
		RevocationEndpoint: "https://mail.example.test/auth/revoke", Sealed: sealed, CanSend: true})
	if err != nil || previous != nil || account.Label != "ada@example.test" {
		t.Fatalf("save = %+v, %v, %v", account, previous, err)
	}

	t.Run("two refreshes that meet present the refresh token once", func(t *testing.T) {
		var wait sync.WaitGroup
		tokens := make([]string, 4)
		failures := make([]error, 4)
		for index := range tokens {
			wait.Add(1)
			go func() {
				defer wait.Done()
				_, credential, err := signIn.Credential(ctx, ada, account.ID)
				tokens[index], failures[index] = credential.AccessToken, err
			}()
		}
		wait.Wait()
		for index, err := range failures {
			if err != nil || tokens[index] != "access-r0+" {
				t.Fatalf("call %d: %q, %v", index, tokens[index], err)
			}
		}
		if got := server.refreshs.Load(); got != 1 || server.dead {
			t.Fatalf("%d refreshes reached the server; grant dead: %v", got, server.dead)
		}
	})

	t.Run("the stored tokens are sealed to their owner", func(t *testing.T) {
		var stored string
		if err := pool.QueryRow(ctx, `SELECT credentials FROM jmap_mail_accounts WHERE id = $1`, account.ID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stored, "r0+") || !strings.HasPrefix(stored, "v1:") {
			t.Fatalf("the column holds the token in the clear: %q", stored)
		}
		// The same ciphertext on Ben's row does not open.
		benSealed, _ := seal.Close(ben, Secrets{Password: "p"})
		benAccount, _, err := store.Save(ctx, ben, NewMailbox{ServerURL: "https://mail.example.test", Username: "ben@example.test",
			Auth: AuthAppPassword, Sealed: benSealed})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE jmap_mail_accounts SET credentials = $1 WHERE id = $2`, stored, benAccount.ID); err != nil {
			t.Fatal(err)
		}
		_, _, err = signIn.Credential(ctx, ben, benAccount.ID)
		if !jmapmail.IsSignInRefused(err) {
			t.Fatalf("Ada's sealed tokens opened on Ben's row: %v", err)
		}
	})

	t.Run("another account sees and changes none of it", func(t *testing.T) {
		if _, _, err := signIn.Credential(ctx, ben, account.ID); !errors.Is(err, ErrNoAccount) {
			t.Errorf("Ben signed in to Ada's mailbox: %v", err)
		}
		if _, err := store.Remove(ctx, ben, account.ID); !errors.Is(err, ErrNoAccount) {
			t.Errorf("Ben removed Ada's mailbox: %v", err)
		}
		listed, err := store.Accounts(ctx, ben)
		if err != nil || len(listed) != 1 || listed[0].Username != "ben@example.test" {
			t.Errorf("Ben's list = %+v, %v", listed, err)
		}
	})

	t.Run("a refused grant marks the mailbox to reconnect", func(t *testing.T) {
		now = now.Add(2 * time.Hour)
		server.mu.Lock()
		server.dead = true
		server.mu.Unlock()
		_, _, err := signIn.Credential(ctx, ada, account.ID)
		if !jmapmail.IsSignInRefused(err) {
			t.Fatalf("a refused refresh: %v", err)
		}
		listed, _ := store.Accounts(ctx, ada)
		if !listed[0].NeedsReconnect {
			t.Fatal("the mailbox is not marked to reconnect")
		}
	})

	t.Run("connecting again replaces in place and hands back the old grant", func(t *testing.T) {
		fresh, _ := seal.Close(ada, Secrets{RefreshToken: "n0", AccessToken: "a", AccessExpires: now.Add(time.Hour)})
		again, previous, err := store.Save(ctx, ada, NewMailbox{ServerURL: "https://mail.example.test", Username: "ada@example.test",
			Auth: AuthOAuth, OAuthClientID: "https://notes.example.org/client.json", TokenEndpoint: "https://mail.example.test/auth/token",
			RevocationEndpoint: "https://mail.example.test/auth/revoke", Sealed: fresh, CanSend: true})
		if err != nil || previous == nil || again.ID != account.ID || again.NeedsReconnect {
			t.Fatalf("reconnect = %+v, %v, %v", again, previous, err)
		}
		if !signIn.Revoke(ctx, *previous) || len(server.revoked) != 1 || server.revoked[0] != "r0+" {
			t.Fatalf("the old grant was not revoked: %v", server.revoked)
		}
		removed, err := store.Remove(ctx, ada, account.ID)
		if err != nil || !signIn.Revoke(ctx, removed) || server.revoked[1] != "n0" {
			t.Fatalf("removing did not revoke: %v, %v", err, server.revoked)
		}
	})
}

// An app password typed with another name of the server is stored, in the
// real table, under the address the server calls itself, sealed; and
// connecting again with either name replaces that one row.
func TestAnAliasConnectsOverARealDatabase(t *testing.T) {
	address := os.Getenv(databaseVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `TRUNCATE "user" CASCADE;
		INSERT INTO "user" (id, email) VALUES ('`+ada+`', 'ada@example.test');`); err != nil {
		t.Fatal(err)
	}
	typed, own := newFakeName(t), newFakeName(t)
	typed.issuer, typed.api = own.origin, own.origin
	mail, _ := mailOver(t, network(typed, own))
	mail.SignIn.Rows = Store{Pool: pool}

	first, err := mail.ConnectAppPassword(ctx, ada, "", typed.origin, "ada@example.test", theAppPassword)
	if err != nil || first.ServerURL != own.origin || !first.CanSend {
		t.Fatalf("connect = %+v, %v", first, err)
	}
	for _, name := range []string{own.origin, typed.origin} {
		again, err := mail.ConnectAppPassword(ctx, ada, "", name, "ada@example.test", theAppPassword)
		if err != nil || again.ID != first.ID {
			t.Fatalf("connecting with %s again = %+v, %v (first %s)", name, again, err, first.ID)
		}
	}
	var server, sealed string
	var rows int
	if err := pool.QueryRow(ctx, `SELECT server_url, credentials, count(*) OVER () FROM jmap_mail_accounts WHERE user_id = $1`, ada).
		Scan(&server, &sealed, &rows); err != nil {
		t.Fatal(err)
	}
	if server != own.origin || rows != 1 || strings.Contains(sealed, theAppPassword) {
		t.Fatalf("row: server %q, %d rows, sealed %q", server, rows, sealed)
	}
	// What a read of the mailbox signs in with is the stored address.
	opened, err := mail.Open(ctx, ada, first.ID)
	if err != nil || opened.Session.Origin != own.origin {
		t.Fatalf("open = %+v, %v", opened.Session, err)
	}
}
