package settings_test

import (
	"reflect"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/settings"
)

// The variable names are the Python server's, so one deployment's environment
// serves either process.
func TestDefaultsWhenNothingIsSet(t *testing.T) {
	resolved := settings.FromEnvironment()
	if resolved.BaseURL != settings.DefaultBaseURL {
		t.Errorf("base URL is %q", resolved.BaseURL)
	}
	if resolved.APIPrefix != settings.DefaultAPIPrefix {
		t.Errorf("API prefix is %q", resolved.APIPrefix)
	}
	if resolved.TimeoutSeconds != settings.DefaultTimeoutSeconds {
		t.Errorf("timeout is %v", resolved.TimeoutSeconds)
	}
	if resolved.Host != settings.DefaultHTTPHost || resolved.Port != settings.DefaultHTTPPort {
		t.Errorf("listening on %s:%d", resolved.Host, resolved.Port)
	}
	if resolved.OAuthEnabled() {
		t.Error("sign-in is on with nothing configured")
	}
	if !reflect.DeepEqual(resolved.OAuthScopes, settings.DefaultOAuthScopes) {
		t.Errorf("scopes are %v", resolved.OAuthScopes)
	}
}

func TestATrailingSlashIsTrimmedAndThePrefixIsNormalised(t *testing.T) {
	t.Setenv("SURFSENSE_BASE_URL", "http://backend:8000/")
	t.Setenv("SURFSENSE_API_PREFIX", "api/v2/")
	resolved := settings.FromEnvironment()
	if resolved.BaseURL != "http://backend:8000" {
		t.Errorf("base URL is %q", resolved.BaseURL)
	}
	if resolved.APIPrefix != "/api/v2" {
		t.Errorf("API prefix is %q", resolved.APIPrefix)
	}
	if resolved.APIBase() != "http://backend:8000/api/v2" {
		t.Errorf("API base is %q", resolved.APIBase())
	}
}

// An unreadable number falls back rather than failing, as the Python original
// does: a typo in a timeout must not stop a server a client depends on.
func TestAnUnreadableNumberFallsBack(t *testing.T) {
	t.Setenv("SURFSENSE_TIMEOUT", "soon")
	t.Setenv("SURFSENSE_MCP_PORT", "later")
	resolved := settings.FromEnvironment()
	if resolved.TimeoutSeconds != settings.DefaultTimeoutSeconds {
		t.Errorf("timeout is %v", resolved.TimeoutSeconds)
	}
	if resolved.Port != settings.DefaultHTTPPort {
		t.Errorf("port is %d", resolved.Port)
	}
}

// One line in the deployment's environment turns sign-in on for this server and
// the backend, with the public address defaulting to the site's own /mcp.
func TestThePublicAddressDefaultsToTheSitesOwnMCPPath(t *testing.T) {
	t.Setenv("SURFSENSE_PUBLIC_URL", "https://notes.example.invalid/")
	t.Setenv("MCP_OAUTH_ISSUER", "https://sso.example.invalid/application/o/mcp/")
	resolved := settings.FromEnvironment()
	if resolved.PublicURL != "https://notes.example.invalid/mcp" {
		t.Errorf("public URL is %q", resolved.PublicURL)
	}
	if !resolved.OAuthEnabled() {
		t.Error("sign-in is off with both values set")
	}
}

func TestAnExplicitPublicAddressWins(t *testing.T) {
	t.Setenv("SURFSENSE_PUBLIC_URL", "https://notes.example.invalid")
	t.Setenv("SURFSENSE_MCP_PUBLIC_URL", "https://elsewhere.example.invalid/mcp/")
	resolved := settings.FromEnvironment()
	if resolved.PublicURL != "https://elsewhere.example.invalid/mcp" {
		t.Errorf("public URL is %q", resolved.PublicURL)
	}
}

// An issuer with no public address leaves sign-in off, because a client cannot
// be told where to sign in without an address it can compare.
func TestAnIssuerAloneDoesNotTurnSignInOn(t *testing.T) {
	t.Setenv("MCP_OAUTH_ISSUER", "https://sso.example.invalid/application/o/mcp/")
	if settings.FromEnvironment().OAuthEnabled() {
		t.Error("sign-in is on with no public address")
	}
}

func TestScopesAreReadAsAWhitespaceSeparatedList(t *testing.T) {
	t.Setenv("MCP_OAUTH_SCOPES", "  openid   email  ")
	resolved := settings.FromEnvironment()
	if !reflect.DeepEqual(resolved.OAuthScopes, []string{"openid", "email"}) {
		t.Errorf("scopes are %v", resolved.OAuthScopes)
	}
}
