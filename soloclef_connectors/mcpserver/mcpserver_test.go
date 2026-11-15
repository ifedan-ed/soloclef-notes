package mcpserver

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

const owner = "11111111-1111-4111-8111-111111111111"

func testSeal(t *testing.T) sealedsecret.Seal {
	t.Helper()
	seal, err := sealedsecret.New("fake-deployment-secret", sealedsecret.MCPServerSecret)
	if err != nil {
		t.Fatal(err)
	}
	return seal
}

func TestOnlyKeyBearingQueryValuesAreMasked(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"https://mcp.example.com/mcp", "https://mcp.example.com/mcp"},
		{"https://mcp.example.com/mcp?region=eu&a%20b=c+d", "https://mcp.example.com/mcp?region=eu&a%20b=c+d"},
		{"https://mcp.exa.ai/mcp?exaApiKey=abcdefgh-1234-wxyz", "https://mcp.exa.ai/mcp?exaApiKey=••••wxyz"},
		{"https://x.example/m?token=short&keep=1#frag", "https://x.example/m?token=••••&keep=1#frag"},
		{"https://x.example/m?Access_TOKEN=", "https://x.example/m?Access_TOKEN="},
	} {
		got, _ := MaskAddress(c.in)
		if got != c.want {
			t.Errorf("MaskAddress(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// What is stored holds no secret in the clear, and opens back to exactly
// what was typed.
func TestBuildSealsEverySecretAndOpenGivesThemBack(t *testing.T) {
	seal := testSeal(t)
	settings := Settings{
		Address: "https://mcp.example.com/mcp?api_key=fake-url-key-123456", Transport: StreamableHTTP,
		Auth:    Auth{Kind: AuthHeader, HeaderName: "X-API-Key", Secret: "fake-header-key-abcd"},
		Headers: map[string]string{"X-Org": "org-7"},
	}
	stored, err := Build(seal, owner, settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(stored)
	for _, secret := range []string{"fake-url-key-123456", "fake-header-key-abcd", "org-7"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("%q is stored in the clear: %s", secret, raw)
		}
	}
	if stored.URL != "https://mcp.example.com/mcp?api_key=••••3456" || stored.Auth.Secret.Mask != "••••abcd" {
		t.Errorf("masks = %q, %q", stored.URL, stored.Auth.Secret.Mask)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	target, err := Open(seal, owner, parsed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if target.Address != settings.Address || target.Headers["X-API-Key"] != "fake-header-key-abcd" || target.Headers["X-Org"] != "org-7" {
		t.Errorf("target = %+v", target)
	}
	if _, err := Open(seal, "22222222-2222-4222-8222-222222222222", parsed, nil); !errors.Is(err, sealedsecret.ErrUnreadable) {
		t.Errorf("another owner opened the settings: %v", err)
	}
}

func TestABearerTokenIsSentAsAuthorization(t *testing.T) {
	seal := testSeal(t)
	stored, err := Build(seal, owner, Settings{Address: "https://a.example/mcp", Transport: SSE,
		Auth: Auth{Kind: AuthBearer, Secret: "tok"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	target, err := Open(seal, owner, stored, nil)
	if err != nil || target.Headers["Authorization"] != "Bearer tok" {
		t.Fatalf("target = %+v, %v", target, err)
	}
}

// A mask sent back keeps the stored secret, sealed as it was, while the
// host is the same; for another host it must be typed again.
func TestAMaskKeepsTheStoredSecretOnTheSameHostOnly(t *testing.T) {
	seal := testSeal(t)
	first, err := Build(seal, owner, Settings{
		Address: "https://mcp.example.com/mcp?api_key=fake-url-key-123456", Transport: StreamableHTTP,
		Auth:    Auth{Kind: AuthBearer, Secret: "fake-bearer-token-9999"},
		Headers: map[string]string{"X-Org": "org-7"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	edit := Settings{Address: first.URL, Transport: SSE,
		Auth:    Auth{Kind: AuthBearer, Secret: first.Auth.Secret.Mask},
		Headers: map[string]string{"x-org": "••••", "X-New": "plain"}}
	second, err := Build(seal, owner, edit, &first)
	if err != nil {
		t.Fatal(err)
	}
	if second.URLSealed != first.URLSealed || second.Auth.Secret.Sealed != first.Auth.Secret.Sealed ||
		second.Headers["x-org"].Sealed != first.Headers["X-Org"].Sealed || second.Transport != SSE {
		t.Errorf("the stored secrets were not kept: %+v", second)
	}

	for name, c := range map[string]struct {
		settings Settings
		part     string
	}{
		"a header on another host": {Settings{Address: "https://evil.example/mcp", Transport: SSE,
			Auth: Auth{Kind: AuthNone}, Headers: map[string]string{"X-Org": "••••"}}, "header"},
		"the token on another host": {Settings{Address: "https://evil.example/mcp", Transport: SSE,
			Auth: Auth{Kind: AuthBearer, Secret: "••••9999"}}, "auth"},
		"the token moved to a header": {Settings{Address: first.URL, Transport: SSE,
			Auth: Auth{Kind: AuthHeader, HeaderName: "X-Key", Secret: "••••9999"}}, "auth"},
		"an edited address around a mask": {Settings{Address: "https://mcp.example.com/v2?api_key=••••3456", Transport: SSE,
			Auth: Auth{Kind: AuthNone}}, "url"},
		"a header never stored": {Settings{Address: first.URL, Transport: SSE,
			Auth: Auth{Kind: AuthNone}, Headers: map[string]string{"X-Other": "••••"}}, "header"},
	} {
		_, err := Build(seal, owner, c.settings, &first)
		var refused *Refusal
		if !errors.As(err, &refused) || refused.Part != c.part {
			t.Errorf("%s: err = %v, want a %s refusal", name, err, c.part)
		}
	}
	if _, err := Build(seal, owner, Settings{Address: "https://a.example/m", Transport: SSE,
		Auth: Auth{Kind: AuthBearer, Secret: "••••"}}, nil); err == nil {
		t.Error("a mask was accepted for a server not saved yet")
	}
}

func TestAnOAuthSignInSurvivesAnEditOnlyForTheSameHostAndClient(t *testing.T) {
	seal := testSeal(t)
	tokens, _ := SealTokens(seal, owner, Tokens{AccessToken: "at", RefreshToken: "rt"})
	signedIn := Stored{URL: "https://mcp.example.com/mcp", Transport: StreamableHTTP, Auth: StoredAuth{Type: AuthOAuth,
		OAuth: &StoredOAuth{ClientID: "dyn-1", DynamicRegistration: true, Issuer: "https://auth.example.com",
			TokenEndpoint: "https://auth.example.com/token", Tokens: tokens, ExpiresAt: 99}}}

	kept, err := Build(seal, owner, Settings{Address: signedIn.URL, Transport: StreamableHTTP, Auth: Auth{Kind: AuthOAuth}}, &signedIn)
	if err != nil || !kept.Auth.OAuth.Authorized() || kept.Auth.OAuth.ClientID != "dyn-1" || kept.Auth.OAuth.TokenEndpoint == "" {
		t.Fatalf("a rename lost the sign-in: %+v, %v", kept.Auth.OAuth, err)
	}
	opened, err := OpenTokens(seal, owner, kept.Auth.OAuth)
	if err != nil || opened.AccessToken != "at" {
		t.Fatalf("tokens = %+v, %v", opened, err)
	}
	target, err := Open(seal, owner, kept, opened)
	if err != nil || target.Headers["Authorization"] != "Bearer at" {
		t.Fatalf("target = %+v, %v", target, err)
	}

	for name, settings := range map[string]Settings{
		"another host":   {Address: "https://other.example/mcp", Transport: StreamableHTTP, Auth: Auth{Kind: AuthOAuth}},
		"another client": {Address: signedIn.URL, Transport: StreamableHTTP, Auth: Auth{Kind: AuthOAuth, ClientID: "typed"}},
	} {
		dropped, err := Build(seal, owner, settings, &signedIn)
		if err != nil || dropped.Auth.OAuth.Authorized() || dropped.Auth.OAuth.TokenEndpoint != "" {
			t.Errorf("%s: the old sign-in was kept: %+v, %v", name, dropped.Auth.OAuth, err)
		}
	}
	if _, err := Open(seal, owner, Stored{URL: "https://a.example", Transport: SSE,
		Auth: StoredAuth{Type: AuthOAuth, OAuth: &StoredOAuth{}}}, nil); !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("an OAuth server with no sign-in opened: %v", err)
	}
}

func TestParseRefusesAnythingNotThisFormat(t *testing.T) {
	for _, raw := range []string{
		`[]`,
		`{"url":"https://a.example","headers":{"X":"in the clear"},"transport":"streamable-http"}`,
		`{"url":"https://a.example","transport":"stdio","auth":{"type":"none"}}`,
		`{"url":"https://a.example","transport":"http","auth":{"type":"none"}}`,
		`{"url":"https://a.example","transport":"sse","auth":{"type":"header"}}`,
		`{"url":"https://a.example","transport":"sse","auth":{"type":"magic"}}`,
		`{"transport":"sse","auth":{"type":"none"}}`,
	} {
		if _, err := Parse([]byte(raw)); !errors.Is(err, ErrUnreadable) {
			t.Errorf("Parse(%s) = %v", raw, err)
		}
	}
}

func TestHeaderNames(t *testing.T) {
	for name, want := range map[string]bool{"X-API-Key": true, "Authorization": true, "": false, "X Key": false, "X:Key": false, "Ключ": false} {
		if HeaderNameValid(name) != want {
			t.Errorf("HeaderNameValid(%q) != %v", name, want)
		}
	}
}
