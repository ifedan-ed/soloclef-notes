package mailsignin

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/imapmail"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

const secretKey = "fake-secret-key"

// merges records what a sign-in wrote into a mailbox's record.
type merges struct {
	written []map[string]any
	fail    error
}

func (m *merges) Merge(_ context.Context, ownerID, accountID string, fields []byte) (bool, error) {
	if m.fail != nil {
		return false, m.fail
	}
	var read map[string]any
	_ = json.Unmarshal(fields, &read)
	read["_owner"], read["_account"] = ownerID, accountID
	m.written = append(m.written, read)
	return true, nil
}

func sealed(t *testing.T, text string) string {
	t.Helper()
	out, err := storedtoken.Seal(secretKey, text, time.Unix(0, 0), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func encoded(t *testing.T, record map[string]any) []byte {
	t.Helper()
	out, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func signIn(rows Merger, microsoft *Endpoint, now time.Time) SignIn {
	return SignIn{SecretKey: secretKey, Microsoft: microsoft, Rows: rows, Now: func() time.Time { return now },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestAPasswordMailbox(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	record := map[string]any{"id": "pw-1", "label": "Home", "host": "mail.example.com", "port": 143, "use_tls": false,
		"username": "ada@example.com", "app_password": sealed(t, "fake-app-password"), "_encrypted": true}
	settings, err := signIn(&merges{}, nil, now).Settings(context.Background(), "owner-1", encoded(t, record))
	want := imapmail.Settings{Host: "mail.example.com", Port: 143, Username: "ada@example.com", Password: "fake-app-password"}
	if err != nil || settings != want {
		t.Fatalf("Settings = %+v, %v; want %+v", settings, err, want)
	}

	// A record from before TLS was a choice means TLS.
	delete(record, "use_tls")
	if settings, _ = signIn(&merges{}, nil, now).Settings(context.Background(), "owner-1", encoded(t, record)); !settings.UseTLS {
		t.Error("a record that does not say reads as no TLS")
	}

	// What a mailbox shows never includes what it is signed in with.
	shown, ok := Describe(encoded(t, record))
	if !ok || shown != (Account{ID: "pw-1", Label: "Home", Host: "mail.example.com", Username: "ada@example.com"}) {
		t.Errorf("Describe = %+v, %v", shown, ok)
	}
	if _, ok := Describe([]byte(`"not a record"`)); ok {
		t.Error("something that is not a record was described")
	}

	// A password sealed with another key, or not sealed at all, is not
	// used: the mailbox says to add it again.
	for name, change := range map[string]func(){
		"another key's": func() {
			other, _ := storedtoken.Seal("another key", "fake-app-password", now, rand.Reader)
			record["app_password"] = other
		},
		"unsealed": func() { record["app_password"], record["_encrypted"] = "fake-app-password", false },
		"missing":  func() { delete(record, "app_password") },
	} {
		change()
		_, err := signIn(&merges{}, nil, now).Settings(context.Background(), "owner-1", encoded(t, record))
		var refused *imapmail.ConnectionError
		if !errors.As(err, &refused) || refused.Message != PasswordUnreadable {
			t.Errorf("%s password: %v", name, err)
		}
	}
}

func TestAMicrosoftMailbox(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	answer := func(http.ResponseWriter, *http.Request) {}
	asked := 0
	microsoft := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		answer(w, r)
	}))
	defer microsoft.Close()
	endpoint := NewEndpoint("fake-client", "fake-client-secret", microsoft.URL, http.DefaultTransport)
	record := func(expires time.Time) []byte {
		return encoded(t, map[string]any{"id": "ms-1", "auth": "microsoft", "username": "ada@example.com",
			"host": "somewhere-else.example.org", "port": 1, "_encrypted": true,
			"access_token": sealed(t, "fake-access-1"), "refresh_token": sealed(t, "fake-refresh-1"),
			"access_token_expires_at": expires.Format(RecordTime)})
	}
	want := imapmail.Settings{Host: IMAPHost, Port: IMAPPort, UseTLS: true, Username: "ada@example.com"}

	t.Run("a token with time left is used as it is, and only ever sent to Microsoft's host", func(t *testing.T) {
		rows := &merges{}
		settings, err := signIn(rows, endpoint, now).Settings(context.Background(), "owner-1", record(now.Add(30*time.Minute)))
		want.AccessToken = "fake-access-1"
		if err != nil || settings != want || asked != 0 || len(rows.written) != 0 {
			t.Fatalf("Settings = %+v, %v; Microsoft asked %d times, %d writes", settings, err, asked, len(rows.written))
		}
	})

	t.Run("a token about to lapse is refreshed and the new pair stored sealed first", func(t *testing.T) {
		asked = 0
		answer = func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "fake-refresh-1" || r.Form.Get("client_secret") != "fake-client-secret" {
				t.Errorf("the refresh sent %v", r.Form)
			}
			_, _ = w.Write([]byte(`{"access_token":"fake-access-2","refresh_token":"fake-refresh-2","expires_in":3600}`))
		}
		rows := &merges{}
		settings, err := signIn(rows, endpoint, now).Settings(context.Background(), "owner-1", record(now.Add(2*time.Minute)))
		want.AccessToken = "fake-access-2"
		if err != nil || settings != want || asked != 1 || len(rows.written) != 1 {
			t.Fatalf("Settings = %+v, %v; Microsoft asked %d times, %d writes", settings, err, asked, len(rows.written))
		}
		written := rows.written[0]
		if written["_owner"] != "owner-1" || written["_account"] != "ms-1" || written[ReconnectKey] != false {
			t.Errorf("wrote %v", written)
		}
		for key, clear := range map[string]string{AccessKey: "fake-access-2", RefreshKey: "fake-refresh-2"} {
			held, _ := written[key].(string)
			if opened, err := storedtoken.Open(secretKey, held); err != nil || opened != clear {
				t.Errorf("%s was stored as %q", key, held)
			}
		}

		// New tokens that cannot be stored fail the sign-in: Microsoft may
		// have rotated the refresh token, and the row would hold a dead one.
		if _, err := signIn(&merges{fail: errors.New("the database is away")}, endpoint, now).
			Settings(context.Background(), "owner-1", record(now.Add(2*time.Minute))); err == nil {
			t.Error("a refresh that could not be stored was used")
		}
	})

	var refused *imapmail.ConnectionError
	t.Run("a grant Microsoft refuses flags the mailbox, and a flagged one does not ask again", func(t *testing.T) {
		asked = 0
		answer = func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		}
		rows := &merges{}
		_, err := signIn(rows, endpoint, now).Settings(context.Background(), "owner-1", record(now.Add(-time.Hour)))
		if !errors.As(err, &refused) || refused.Message != ReconnectMessage || len(rows.written) != 1 || rows.written[0][ReconnectKey] != true {
			t.Fatalf("a refused grant: %v, wrote %v", err, rows.written)
		}
		flagged := encoded(t, map[string]any{"id": "ms-1", "auth": "microsoft", ReconnectKey: true})
		_, err = signIn(rows, endpoint, now).Settings(context.Background(), "owner-1", flagged)
		if !errors.As(err, &refused) || refused.Message != ReconnectMessage || asked != 1 {
			t.Errorf("a flagged mailbox: %v, Microsoft asked %d times", err, asked)
		}
	})

	t.Run("this server's own registration being refused is not the mailbox's fault", func(t *testing.T) {
		answer = func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
		}
		rows := &merges{}
		_, err := signIn(rows, endpoint, now).Settings(context.Background(), "owner-1", record(now.Add(-time.Hour)))
		if !errors.As(err, &refused) || refused.Message != ServerRefusedMessage || len(rows.written) != 0 {
			t.Errorf("a refused client: %v, wrote %v", err, rows.written)
		}
	})

	t.Run("a deployment without Microsoft mail says so", func(t *testing.T) {
		_, err := signIn(&merges{}, nil, now).Settings(context.Background(), "owner-1", record(now.Add(time.Hour)))
		if !errors.As(err, &refused) || refused.Message != NotSetUpMessage {
			t.Errorf("no endpoint: %v", err)
		}
	})
}
