package connectionrevocation

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
)

// A token the Python tier's own library wrote for a secret made up for
// these tests (soloclef_connectors/storedtoken's tests say how): it opens as
// "app-password-Abc12-xyZ34 é".
const (
	secret    = "test-vector-secret-not-a-real-key"
	encrypted = "gAAAAABq9Iq7NAQ3pWlKWW79LYRYl2pUa2e-F60LMLbmkg_d7PBJjjTciR3kxkfQw9zp6_TjtxReKRTayvKrw6CQrg83OsSDHGne0cjbivazpSPUqjsQo-s="
	opened    = "app-password-Abc12-xyZ34 é"
)

// provider is a stand-in for Google or Nextcloud: it records what it was
// sent and answers with a status of the test's choosing.
type provider struct {
	mu       sync.Mutex
	status   int
	location string
	requests []seen
	server   *httptest.Server
}

type seen struct {
	method, path, token, user, password, ocs string
}

func newProvider(t *testing.T, status int) *provider {
	t.Helper()
	p := &provider{status: status}
	p.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_ = request.ParseForm()
		user, password, _ := request.BasicAuth()
		p.mu.Lock()
		p.requests = append(p.requests, seen{request.Method, request.URL.Path, request.PostForm.Get("token"), user, password, request.Header.Get("OCS-APIRequest")})
		p.mu.Unlock()
		if p.location != "" {
			response.Header().Set("Location", p.location)
		}
		response.WriteHeader(p.status)
	}))
	t.Cleanup(p.server.Close)
	return p
}

// revocation is a Revocation whose Google is the stand-in, with its log
// kept for the tests that read it.
func revocation(t *testing.T, google *provider, nextcloud *nextcloud.Credentials) (*Revocation, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	r, err := New(secret, nextcloud, http.DefaultTransport, slog.New(slog.NewTextHandler(&log, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if google != nil {
		r.GoogleURL = google.server.URL + "/revoke"
	}
	return r, &log
}

func config(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAGoogleGrantIsRevokedByItsRefreshToken(t *testing.T) {
	for name, test := range map[string]struct {
		config map[string]any
		token  string
	}{
		"encrypted, as the sign-in stores it": {
			map[string]any{"refresh_token": encrypted, "token": "ignored", "_token_encrypted": true}, opened},
		"in the clear, as older rows hold it": {
			map[string]any{"refresh_token": "1//refresh", "token": "ya29.access"}, "1//refresh"},
		"no refresh token: the access token": {
			map[string]any{"token": "ya29.access"}, "ya29.access"},
	} {
		t.Run(name, func(t *testing.T) {
			google := newProvider(t, http.StatusOK)
			r, log := revocation(t, google, nil)
			r.Revoke(context.Background(), 800, GoogleDrive, config(t, test.config))
			if len(google.requests) != 1 || google.requests[0].method != http.MethodPost || google.requests[0].token != test.token {
				t.Fatalf("Google was sent %+v, want one POST of the token", google.requests)
			}
			if !strings.Contains(log.String(), "outcome=revoked") {
				t.Errorf("logged %s", log)
			}
			// The log names the connection and never the credential.
			if strings.Contains(log.String(), test.token) {
				t.Errorf("the token is in the log: %s", log)
			}
		})
	}
}

// 400 invalid_token is a grant that is already ended, which is the goal;
// anything else is a failure that is logged and does not stop the removal.
func TestWhatGoogleAnswersIsReadAsRevokedOrNot(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusOK: "outcome=revoked", http.StatusBadRequest: "outcome=revoked",
		http.StatusServiceUnavailable: `outcome="not revoked: Google answered 503"`,
	} {
		google := newProvider(t, status)
		r, log := revocation(t, google, nil)
		r.Revoke(context.Background(), 800, GoogleDrive, config(t, map[string]any{"refresh_token": "1//refresh"}))
		if !strings.Contains(log.String(), want) {
			t.Errorf("status %d: logged %s, want %s", status, log, want)
		}
	}
}

func TestAGoogleTokenThatCannotBeOpenedIsNotSentAnywhere(t *testing.T) {
	google := newProvider(t, http.StatusOK)
	r, log := revocation(t, google, nil)
	for _, stored := range []map[string]any{
		// Encrypted under another deployment's key.
		{"refresh_token": "gAAAAAB-not-a-token-this-key-wrote", "_token_encrypted": true},
		// Nothing stored at all.
		{"account_email": "someone@example.com"},
	} {
		r.Revoke(context.Background(), 800, GoogleDrive, config(t, stored))
	}
	if len(google.requests) != 0 {
		t.Errorf("Google was sent %+v", google.requests)
	}
	if !strings.Contains(log.String(), "cannot be opened") || !strings.Contains(log.String(), "no credential was stored") {
		t.Errorf("logged %s", log)
	}
}

func TestANextcloudAppPasswordIsDeletedAtTheDeploymentsServer(t *testing.T) {
	cloud := newProvider(t, http.StatusOK)
	credentials, err := nextcloud.NewCredentials(secret, cloud.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	r, log := revocation(t, nil, &credentials)
	r.Revoke(context.Background(), 802, Nextcloud, config(t, map[string]any{
		"server_url": cloud.server.URL, "username": "someone", "login_name": "someone@login",
		"app_password": encrypted, "_token_encrypted": true,
	}))
	want := seen{method: http.MethodDelete, path: "/ocs/v2.php/core/apppassword", user: "someone@login", password: opened, ocs: "true"}
	if len(cloud.requests) != 1 || cloud.requests[0] != want {
		t.Fatalf("Nextcloud was sent %+v, want %+v", cloud.requests, want)
	}
	if !strings.Contains(log.String(), "outcome=revoked") || strings.Contains(log.String(), opened) {
		t.Errorf("logged %s", log)
	}
}

// The password goes to the deployment's server and nowhere else: not to
// the server a row names, and not where a reply points.
func TestANextcloudPasswordIsNeverSentElsewhere(t *testing.T) {
	cloud, elsewhere := newProvider(t, http.StatusFound), newProvider(t, http.StatusOK)
	cloud.location = elsewhere.server.URL + "/ocs/v2.php/core/apppassword"
	credentials, err := nextcloud.NewCredentials(secret, cloud.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	r, log := revocation(t, nil, &credentials)

	// A row that names another server: nothing is sent at all.
	r.Revoke(context.Background(), 802, Nextcloud, config(t, map[string]any{
		"server_url": elsewhere.server.URL, "username": "someone", "app_password": "in-the-clear"}))
	if len(cloud.requests) != 0 || len(elsewhere.requests) != 0 {
		t.Fatalf("a row for another server was acted on: %+v %+v", cloud.requests, elsewhere.requests)
	}

	// The deployment's server answering with a redirect: not followed.
	r.Revoke(context.Background(), 802, Nextcloud, config(t, map[string]any{
		"server_url": cloud.server.URL, "username": "someone", "app_password": "in-the-clear"}))
	if len(cloud.requests) != 1 || len(elsewhere.requests) != 0 {
		t.Errorf("a redirect was followed: %+v %+v", cloud.requests, elsewhere.requests)
	}
	if !strings.Contains(log.String(), "Nextcloud answered 302") {
		t.Errorf("logged %s", log)
	}
}

func TestADeploymentWithNoNextcloudServerSaysSoAndSendsNothing(t *testing.T) {
	r, log := revocation(t, nil, nil)
	r.Revoke(context.Background(), 802, Nextcloud, config(t, map[string]any{
		"server_url": "https://cloud.example", "username": "someone", "app_password": "in-the-clear"}))
	if !strings.Contains(log.String(), "names no Nextcloud server") {
		t.Errorf("logged %s", log)
	}
}

// An MCP server's key and a mailbox's password are the person's own to
// retire; nothing is sent and nothing is logged.
func TestOtherKindsOfConnectionAreLeftAlone(t *testing.T) {
	google := newProvider(t, http.StatusOK)
	r, log := revocation(t, google, nil)
	for _, kind := range []string{"MCP_CONNECTOR", "EMAIL_IMAP_CONNECTOR", "WEBCRAWLER_CONNECTOR"} {
		r.Revoke(context.Background(), 810, kind, config(t, map[string]any{"token": "something", "refresh_token": "something"}))
	}
	if len(google.requests) != 0 || log.Len() != 0 {
		t.Errorf("sent %+v, logged %s", google.requests, log)
	}
}

func TestNewRefusesAnIncompleteRevocation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	if _, err := New("", nil, http.DefaultTransport, logger); err == nil {
		t.Error("built with no secret key")
	}
	if _, err := New(secret, nil, nil, logger); err == nil {
		t.Error("built with no transport")
	}
	if _, err := New(secret, nil, http.DefaultTransport, nil); err == nil {
		t.Error("built with no logger")
	}
}
