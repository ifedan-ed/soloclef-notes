package settings_test

import (
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
