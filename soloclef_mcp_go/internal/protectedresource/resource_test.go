package protectedresource_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/protectedresource"
)

func resource() protectedresource.Resource {
	return protectedresource.Resource{
		Address:             "https://notes.example.invalid/mcp",
		AuthorizationServer: "https://sso.example.invalid/application/o/mcp/",
		Scopes:              []string{"openid", "email", "profile", "offline_access"},
	}
}

// RFC 9728's path for this resource, and the bare one some clients probe first.
func TestTheDocumentIsServedAtBothPaths(t *testing.T) {
	want := []string{
		"/.well-known/oauth-protected-resource/mcp",
		"/.well-known/oauth-protected-resource",
	}
	if got := resource().MetadataPaths(); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAResourceAtTheRootHasOnlyTheBarePath(t *testing.T) {
	bare := resource()
	bare.Address = "https://notes.example.invalid"
	want := []string{"/.well-known/oauth-protected-resource"}
	if got := bare.MetadataPaths(); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTheDocumentNamesOneAuthorizationServer(t *testing.T) {
	// Claude reads only the first entry, so there is exactly one.
	encoded, err := json.Marshal(resource().Metadata())
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Resource               string   `json:"resource"`
		AuthorizationServers   []string `json:"authorization_servers"`
		ScopesSupported        []string `json:"scopes_supported"`
		BearerMethodsSupported []string `json:"bearer_methods_supported"`
		ResourceName           string   `json:"resource_name"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.AuthorizationServers) != 1 ||
		decoded.AuthorizationServers[0] != "https://sso.example.invalid/application/o/mcp/" {
		t.Errorf("authorization_servers is %v", decoded.AuthorizationServers)
	}
	if decoded.Resource != "https://notes.example.invalid/mcp" {
		t.Errorf("resource is %q", decoded.Resource)
	}
	if !reflect.DeepEqual(decoded.BearerMethodsSupported, []string{"header"}) {
		t.Errorf("bearer_methods_supported is %v", decoded.BearerMethodsSupported)
	}
	if decoded.ResourceName != "Soloclef Notes" {
		t.Errorf("resource_name is %q", decoded.ResourceName)
	}
	if len(decoded.ScopesSupported) != 4 {
		t.Errorf("scopes_supported is %v", decoded.ScopesSupported)
	}
}

// The challenge is what tells claude.ai and ChatGPT where to sign in, so its
// exact text matters.
func TestTheChallengeNamesTheDocumentAndTheScopes(t *testing.T) {
	const want = `Bearer resource_metadata="https://notes.example.invalid/` +
		`.well-known/oauth-protected-resource/mcp", ` +
		`scope="openid email profile offline_access"`
	if got := resource().Challenge("", ""); got != want {
		t.Errorf("got\n %s\nwant\n %s", got, want)
	}
}

func TestAnInvalidTokenChallengeCarriesTheReason(t *testing.T) {
	const want = `Bearer error="invalid_token", ` +
		`error_description="The sign-in is not valid or has expired", ` +
		`resource_metadata="https://notes.example.invalid/` +
		`.well-known/oauth-protected-resource/mcp", ` +
		`scope="openid email profile offline_access"`
	got := resource().Challenge("invalid_token", "The sign-in is not valid or has expired")
	if got != want {
		t.Errorf("got\n %s\nwant\n %s", got, want)
	}
}

// A description with a quote in it would otherwise end the parameter early and
// leave the client reading a malformed header.
func TestAQuotedValueIsEscaped(t *testing.T) {
	got := resource().Challenge("invalid_token", `he said "no" \ here`)
	const want = `error_description="he said \"no\" \\ here"`
	if !strings.Contains(got, want) {
		t.Errorf("challenge does not escape the description:\n %s", got)
	}
}
