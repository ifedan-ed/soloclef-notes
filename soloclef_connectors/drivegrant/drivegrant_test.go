package drivegrant

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

const secretKey = "a secret key made up for this test"

func tokenEndpoint(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		answer(w, r)
	}))
	t.Cleanup(server.Close)
	return Client{ClientID: "project-1", ClientSecret: "project-secret", TokenURL: server.URL, HTTP: server.Client()}
}

func TestARefreshSendsTheProjectsCredentialsAndReadsTheGrant(t *testing.T) {
	client := tokenEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Form.Get("client_id") != "project-1" || r.Form.Get("client_secret") != "project-secret" ||
			r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "kept" {
			t.Errorf("Google was sent %v", r.Form)
		}
		_, _ = io.WriteString(w, `{"access_token": "fresh", "expires_in": 3600, "scope": "a b"}`)
	})
	granted, err := client.Refresh(t.Context(), "kept")
	if err != nil {
		t.Fatal(err)
	}
	if granted.AccessToken != "fresh" || len(granted.Scopes) != 2 || time.Until(granted.Expiry) < 59*time.Minute {
		t.Errorf("read %+v", granted)
	}
}

func TestARefusedGrantIsToldApartFromGoogleFailing(t *testing.T) {
	refused := tokenEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error": "invalid_grant", "error_description": "Token has been expired or revoked."}`)
	})
	if _, err := refused.Refresh(t.Context(), "dead"); !errors.Is(err, ErrInvalidGrant) {
		t.Errorf("a revoked grant gave %v", err)
	}
	broken := tokenEndpoint(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `the body names secret-detail`)
	})
	_, err := broken.Refresh(t.Context(), "kept")
	var failure *Error
	if !errors.As(err, &failure) || errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("a 503 gave %v", err)
	}
	// The error reaches a log, so nothing of the reply's body is in it.
	if strings.Contains(err.Error(), "secret-detail") {
		t.Errorf("the error repeats Google's reply: %v", err)
	}
	empty := tokenEndpoint(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{}`) })
	if _, err := empty.Refresh(t.Context(), "kept"); !errors.As(err, &failure) {
		t.Errorf("a reply with no token gave %v", err)
	}
}

func sealedConfig(t *testing.T, now time.Time, expiry time.Time) []byte {
	t.Helper()
	seal := func(plain string) string {
		sealed, err := storedtoken.Seal(secretKey, plain, now, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return sealed
	}
	config, err := json.Marshal(map[string]any{
		"token": seal("access-1"), "refresh_token": seal("refresh-1"), "_token_encrypted": true,
		"expiry": expiry.UTC().Format(ExpiryLayout), "account_email": "ada@gmail.test", "selected_files": []string{"kept"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func TestAStoredGrantOpensOnlyWithItsKeyAndOnlyWhileItLasts(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	stored := Read(sealedConfig(t, now, now.Add(10*time.Minute)))

	if token, ok := stored.OpenRefreshToken(secretKey); !ok || token != "refresh-1" {
		t.Errorf("the refresh token opened as %q, %v", token, ok)
	}
	if _, ok := stored.OpenRefreshToken("another deployment's key"); ok {
		t.Error("another key opened the refresh token")
	}
	if token, ok := stored.OpenAccessToken(secretKey, now, 5*time.Minute); !ok || token != "access-1" {
		t.Errorf("a token with ten minutes left, five wanted, opened as %q, %v", token, ok)
	}
	// Exactly the margin left is not enough: the work may take all of it.
	if _, ok := stored.OpenAccessToken(secretKey, now, 10*time.Minute); ok {
		t.Error("a token with no time to spare was handed out")
	}
	if _, ok := Read([]byte(`{"token": "plain", "expiry": "not a time"}`)).OpenAccessToken(secretKey, now, 0); ok {
		t.Error("a token with no readable expiry was handed out")
	}
	if _, ok := Read([]byte(`[1, 2]`)).OpenRefreshToken(secretKey); ok {
		t.Error("a config that is not an object held a grant")
	}
}

func TestARenewalChangesOnlyTheAccessTokenAndKeepsTheRowsForm(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	renewed := Grant{AccessToken: "access-2", Expiry: now.Add(time.Hour)}

	patch, err := Read(sealedConfig(t, now, now)).Renewed(secretKey, renewed, now, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]string
	if err := json.Unmarshal(patch, &keys); err != nil || len(keys) != 2 {
		t.Fatalf("the patch is %s (%v)", patch, err)
	}
	if strings.Contains(string(patch), "access-2") {
		t.Errorf("a sealed row was given a token in the clear: %s", patch)
	}
	if opened, err := storedtoken.Open(secretKey, keys["token"]); err != nil || opened != "access-2" {
		t.Errorf("the sealed token opens as %q (%v)", opened, err)
	}
	if keys["expiry"] != "2026-11-12T10:00:00.000000Z" {
		t.Errorf("expiry = %q", keys["expiry"])
	}

	// A row that was never sealed stays readable by what reads it.
	plain, err := Read([]byte(`{"token": "old", "refresh_token": "r"}`)).Renewed(secretKey, renewed, now, rand.Reader)
	if err != nil || !strings.Contains(string(plain), `"token":"access-2"`) {
		t.Errorf("an unsealed row's patch is %s (%v)", plain, err)
	}
}
