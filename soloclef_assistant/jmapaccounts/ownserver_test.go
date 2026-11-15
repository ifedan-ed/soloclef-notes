package jmapaccounts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"
)

// These tests stand up each name of a Stalwart server as its own loopback
// TLS server, so every name is its own origin as it is on the internet,
// and check which name the sign-in is made with and what each name was
// sent.

// The one password and the one token the fake servers accept.
const (
	theAppPassword = "fake-app-password"
	theAccessToken = "fake-access"
)

// seen is one request a fake server received.
type seen struct {
	path, authorization string
}

// fakeName is one name of a Stalwart server. Its metadata names the issuer
// the test sets ("" its own origin), with every endpoint on that issuer, as
// Stalwart builds them from the one address it calls itself; its JMAP
// session names the API's origin the same way.
type fakeName struct {
	server *httptest.Server
	origin string
	// issuer is what its metadata names; "" is its own origin.
	issuer string
	// api is the origin its session's URLs are on; "" is its own.
	api string
	// noS256 leaves PKCE's S256 out of its metadata.
	noS256 bool
	// noMetadata answers its metadata with 404: a JMAP server without
	// Stalwart's sign-in.
	noMetadata bool

	mu       sync.Mutex
	received []seen
}

func newFakeName(t *testing.T) *fakeName {
	t.Helper()
	f := &fakeName{}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	f.origin = f.server.URL
	return f
}

func (f *fakeName) serve(response http.ResponseWriter, request *http.Request) {
	f.mu.Lock()
	f.received = append(f.received, seen{path: request.URL.Path, authorization: request.Header.Get("Authorization")})
	issuer, api, noS256, noMetadata := f.issuer, f.api, f.noS256, f.noMetadata
	f.mu.Unlock()
	if issuer == "" {
		issuer = f.origin
	}
	if api == "" {
		api = f.origin
	}
	response.Header().Set("Content-Type", "application/json")
	switch request.URL.Path {
	case metadataPath:
		if noMetadata {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		methods := []string{"S256"}
		if noS256 {
			methods = []string{"plain"}
		}
		_ = json.NewEncoder(response).Encode(map[string]any{
			"issuer": issuer, "authorization_endpoint": issuer + "/login", "token_endpoint": issuer + "/auth/token",
			"revocation_endpoint": issuer + "/auth/revoke", "code_challenge_methods_supported": methods,
			"authorization_response_iss_parameter_supported": true, "client_id_metadata_document_supported": true,
		})
	case "/.well-known/jmap":
		_, password, ok := request.BasicAuth()
		if (!ok || password != theAppPassword) && request.Header.Get("Authorization") != "Bearer "+theAccessToken {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(response, `{"capabilities":{"urn:ietf:params:jmap:core":{},"urn:ietf:params:jmap:mail":{},"urn:ietf:params:jmap:submission":{}},`+
			`"accounts":{"A":{"name":"ada@example.test","accountCapabilities":{"urn:ietf:params:jmap:mail":{},"urn:ietf:params:jmap:submission":{}}}},`+
			`"primaryAccounts":{"urn:ietf:params:jmap:mail":"A"},"username":"ada@example.test","apiUrl":"`+api+`/jmap/"}`)
	default:
		response.WriteHeader(http.StatusNotFound)
	}
}

// requests is what this name received.
func (f *fakeName) requests() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seen(nil), f.received...)
}

// sentASecret reports whether any request to this name carried a
// password or a token.
func (f *fakeName) sentASecret() bool {
	for _, request := range f.requests() {
		if request.authorization != "" {
			return true
		}
	}
	return false
}

// names is the test's Network: each fake name by its origin, and anything
// else to the production policy, with a resolver that puts every name on
// a private address, so a request that escaped the fakes is refused the
// way a deployment refuses it.
type names map[string]*fakeName

func (n names) Client(serverURL string) (*http.Client, error) {
	origin, err := jmapmail.Origin(serverURL)
	if err != nil {
		return nil, err
	}
	if name, ok := n[origin]; ok {
		client := name.server.Client()
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		return client, nil
	}
	return PublicNetwork{Internet: mcptools.PublicInternet{ConnectTimeout: time.Second,
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.1.2.3")}, nil
		}}}.Client(serverURL)
}

func network(all ...*fakeName) names {
	out := names{}
	for _, name := range all {
		out[name.origin] = name
	}
	return out
}

func oauthOver(n names) OAuth { return OAuth{Network: n, Timeout: 5 * time.Second} }

// refusal is the person's words of a refusal, or "" if err is not one.
func refusal(err error) string {
	var refused *Refused
	if errors.As(err, &refused) {
		return refused.Message
	}
	return ""
}

func TestDiscoverFollowsTheIssuerOnce(t *testing.T) {
	typed, own := newFakeName(t), newFakeName(t)
	typed.issuer = own.origin
	metadata, err := oauthOver(network(typed, own)).Discover(context.Background(), typed.origin)
	if err != nil {
		t.Fatal(err)
	}
	// Everything of the sign-in is the issuer's: the server, the endpoints
	// the attempt records, the issuer the callback checks.
	if metadata.Server != own.origin || metadata.Issuer != own.origin || metadata.TokenEndpoint != own.origin+"/auth/token" ||
		metadata.AuthorizationEndpoint != own.origin+"/login" || metadata.RevocationEndpoint != own.origin+"/auth/revoke" {
		t.Fatalf("metadata = %+v", metadata)
	}
	// One request to each name, the metadata only, and no secret.
	if got := typed.requests(); len(got) != 1 || got[0].path != metadataPath {
		t.Errorf("the typed name received %+v", got)
	}
	if got := own.requests(); len(got) != 1 || got[0].path != metadataPath {
		t.Errorf("the issuer received %+v", got)
	}
}

func TestDiscoverOfTheServersOwnAddressMakesOneRequest(t *testing.T) {
	own := newFakeName(t)
	metadata, err := oauthOver(network(own)).Discover(context.Background(), own.origin+"/some/page")
	if err != nil || metadata.Server != own.origin {
		t.Fatalf("metadata = %+v, %v", metadata, err)
	}
	if got := own.requests(); len(got) != 1 {
		t.Errorf("%d requests for a server that names itself", len(got))
	}
}

func TestDiscoverRefusesWhatCannotBeConfirmed(t *testing.T) {
	for name, set := range map[string]func(typed, second, third *fakeName){
		// The issuer names a third address: a chain, not followed.
		"a chain": func(typed, second, third *fakeName) { typed.issuer, second.issuer = second.origin, third.origin },
		// The issuer names the typed address back.
		"a loop": func(typed, second, _ *fakeName) { typed.issuer, second.issuer = second.origin, typed.origin },
		// The issuer names nothing that is an address.
		"an issuer that does not name itself": func(typed, second, _ *fakeName) { typed.issuer, second.issuer = second.origin, "not an address" },
		// The issuer does not offer what this app needs: it is its
		// document that is checked, not the typed name's.
		"an issuer without S256": func(typed, second, _ *fakeName) { typed.issuer, second.noS256 = second.origin, true },
		// The issuer has no metadata at all.
		"an issuer without metadata": func(typed, second, _ *fakeName) { typed.issuer, second.noMetadata = second.origin, true },
	} {
		t.Run(name, func(t *testing.T) {
			typed, second, third := newFakeName(t), newFakeName(t), newFakeName(t)
			set(typed, second, third)
			_, err := oauthOver(network(typed, second, third)).Discover(context.Background(), typed.origin)
			if refusal(err) == "" {
				t.Fatalf("not refused: %v", err)
			}
			if got := third.requests(); len(got) != 0 {
				t.Errorf("a second hop was made: %+v", got)
			}
			if got := second.requests(); len(got) > 1 {
				t.Errorf("the issuer was asked %d times", len(got))
			}
		})
	}
}

func TestDiscoverRefusesAnIssuerOffThePublicHTTPSInternet(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		t.Error("the plain-http issuer was asked")
		response.WriteHeader(http.StatusTeapot)
	}))
	defer plain.Close()
	for name, issuer := range map[string]string{
		"plain http": plain.URL,
		// Refused by the production policy before any lookup.
		"an internal name": "https://stalwart.internal",
		// Refused by the production policy at the connection: the name
		// resolves to a private address.
		"a name on a private address": "https://stalwart.example.test",
	} {
		t.Run(name, func(t *testing.T) {
			typed := newFakeName(t)
			typed.issuer = issuer
			_, err := oauthOver(network(typed)).Discover(context.Background(), typed.origin)
			if refusal(err) == "" {
				t.Fatalf("not refused: %v", err)
			}
		})
	}
}

// mailOver is a Mail whose rows are kept in memory: these tests are about
// which server is asked, not about the table (the database test is).
func mailOver(t *testing.T, n names) (*Mail, *memoryRows) {
	t.Helper()
	seal, err := NewSeal("deployment-secret")
	if err != nil {
		t.Fatal(err)
	}
	rows := &memoryRows{}
	mail := &Mail{SignIn: SignIn{Rows: rows, Seal: seal, OAuth: oauthOver(n), Now: time.Now,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
		Client: jmapmail.Client{Network: n, Timeout: 5 * time.Second, AttachmentTimeout: 5 * time.Second}}
	if err := mail.Check(); err != nil {
		t.Fatal(err)
	}
	return mail, rows
}

// memoryRows keeps what was saved; nothing else is asked of it here.
type memoryRows struct {
	Rows
	saved []NewMailbox
}

func (m *memoryRows) Save(_ context.Context, _ string, mailbox NewMailbox) (Account, *Removed, error) {
	m.saved = append(m.saved, mailbox)
	return Account{ID: "box", ServerURL: mailbox.ServerURL, Username: mailbox.Username, Auth: mailbox.Auth}, nil, nil
}

// basic is the Authorization header of the test's app password.
func basic() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("ada@example.test:"+theAppPassword))
}

func TestAnAppPasswordIsStoredUnderTheServersOwnAddress(t *testing.T) {
	typed, own := newFakeName(t), newFakeName(t)
	// The typed name is an alias: its session and its metadata both name
	// the other, which names itself.
	typed.issuer, typed.api = own.origin, own.origin
	mail, rows := mailOver(t, network(typed, own))
	account, err := mail.ConnectAppPassword(context.Background(), ada, "Mine", typed.origin, "ada@example.test", theAppPassword)
	if err != nil {
		t.Fatal(err)
	}
	if account.ServerURL != own.origin || len(rows.saved) != 1 || rows.saved[0].ServerURL != own.origin {
		t.Fatalf("stored under %q (%+v)", account.ServerURL, rows.saved)
	}
	// The typed name was sent the password once, for the session the
	// person asked of it, and its metadata without one; the server's own
	// address its metadata without one, then the session.
	want := map[*fakeName][]seen{
		typed: {{path: "/.well-known/jmap", authorization: basic()}, {path: metadataPath}},
		own:   {{path: metadataPath}, {path: "/.well-known/jmap", authorization: basic()}},
	}
	for name, expected := range want {
		if got := name.requests(); !equalSeen(got, expected) {
			t.Errorf("%s received %+v", name.origin, got)
		}
	}
}

func TestAnAppPasswordIsNotSentWhereTheServerCannotBeConfirmed(t *testing.T) {
	for name, set := range map[string]func(typed, other *fakeName){
		// The session names another server, but the typed name's sign-in
		// names itself: two different servers' word, not one.
		"metadata naming the typed name": func(typed, other *fakeName) { typed.api = other.origin },
		// No sign-in metadata to confirm anything with.
		"no metadata": func(typed, other *fakeName) { typed.api, typed.noMetadata = other.origin, true },
		// The metadata names the other, but the other does not name
		// itself.
		"an issuer that does not confirm itself": func(typed, other *fakeName) {
			typed.api, typed.issuer, other.issuer = other.origin, other.origin, typed.origin
		},
	} {
		t.Run(name, func(t *testing.T) {
			typed, other := newFakeName(t), newFakeName(t)
			set(typed, other)
			mail, rows := mailOver(t, network(typed, other))
			_, err := mail.ConnectAppPassword(context.Background(), ada, "", typed.origin, "ada@example.test", theAppPassword)
			var failure *jmapmail.Failure
			if !errors.As(err, &failure) || !strings.Contains(failure.Message, "calls itself "+other.origin) {
				t.Fatalf("not the session's refusal: %v", err)
			}
			if other.sentASecret() || len(rows.saved) != 0 {
				t.Errorf("the password went to %s, or a mailbox was stored (%+v)", other.origin, rows.saved)
			}
		})
	}
}

func TestAnAppPasswordOnTheServersOwnAddressAsksNothingMore(t *testing.T) {
	own := newFakeName(t)
	mail, _ := mailOver(t, network(own))
	if _, err := mail.ConnectAppPassword(context.Background(), ada, "", own.origin, "ada@example.test", theAppPassword); err != nil {
		t.Fatal(err)
	}
	if got := own.requests(); len(got) != 1 || got[0].path != "/.well-known/jmap" {
		t.Fatalf("received %+v", got)
	}
}

func TestAStalwartSignInSendsNoSecretToTheTypedName(t *testing.T) {
	typed, own := newFakeName(t), newFakeName(t)
	typed.issuer, typed.api = own.origin, own.origin
	n := network(typed, own)
	metadata, err := oauthOver(n).Discover(context.Background(), typed.origin)
	if err != nil {
		t.Fatal(err)
	}
	// What the API's callback does next with the metadata's server: the
	// session with the granted token, and the stored mailbox.
	mail, rows := mailOver(t, n)
	grant := Grant{AccessToken: theAccessToken, RefreshToken: "fake-refresh", Scope: MailScope, ExpiresAt: time.Now().Add(time.Hour)}
	account, err := mail.ConnectGrant(context.Background(), ada, metadata.Server, "https://notes.example.test/client.json", metadata, grant)
	if err != nil {
		t.Fatal(err)
	}
	if typed.sentASecret() {
		t.Fatalf("the typed name was sent a secret: %+v", typed.requests())
	}
	if account.ServerURL != own.origin || len(rows.saved) != 1 || rows.saved[0].TokenEndpoint != own.origin+"/auth/token" {
		t.Fatalf("stored = %+v", rows.saved)
	}
}

func equalSeen(got, want []seen) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
