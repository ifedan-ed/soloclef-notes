// Package protectedresource presents this server as an OAuth protected resource
// (RFC 9728, the MCP authorization spec).
//
// claude.ai and ChatGPT find where to sign in from the document built here: a
// 401 names it in WWW-Authenticate: Bearer resource_metadata="...", and it lists
// the identity provider as the one authorization server. Claude uses only the
// first entry, so there is exactly one.
package protectedresource

import (
	"net/url"
	"strings"
)

// WellKnown is RFC 9728's path prefix.
const WellKnown = "/.well-known/oauth-protected-resource"

// DefaultName is the resource name a client shows while signing in.
const DefaultName = "Soloclef Notes"

// Resource is this server as a protected resource.
type Resource struct {
	// Address is exactly the URL people paste into their client, e.g.
	// https://notes.example.com/mcp: clients compare it with what they asked.
	Address string
	// AuthorizationServer is the issuer, character for character as its
	// discovery document prints it, trailing slash included, or discovery fails.
	AuthorizationServer string
	Scopes              []string
	Name                string
}

// MetadataPaths is where the document is served: RFC 9728's path for this
// resource (/.well-known/oauth-protected-resource/mcp) and the bare one some
// clients probe first.
func (r Resource) MetadataPaths() []string {
	parsed, err := url.Parse(r.Address)
	if err != nil {
		return []string{WellKnown}
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		return []string{WellKnown}
	}
	return []string{WellKnown + path, WellKnown}
}

// MetadataURL is the absolute address of the document, which a challenge names.
func (r Resource) MetadataURL() string {
	parsed, err := url.Parse(r.Address)
	if err != nil {
		return r.MetadataPaths()[0]
	}
	return (&url.URL{
		Scheme: parsed.Scheme,
		Host:   parsed.Host,
		Path:   r.MetadataPaths()[0],
	}).String()
}

// Metadata is the document itself, with its members in the order the Python
// server writes them.
func (r Resource) Metadata() map[string]any {
	name := r.Name
	if name == "" {
		name = DefaultName
	}
	scopes := r.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return map[string]any{
		"resource":                 r.Address,
		"authorization_servers":    []string{r.AuthorizationServer},
		"scopes_supported":         scopes,
		"bearer_methods_supported": []string{"header"},
		"resource_name":            name,
	}
}

// Challenge is the WWW-Authenticate value for a 401 (RFC 6750 section 3).
func (r Resource) Challenge(failure, description string) string {
	var parameters []string
	if failure != "" {
		parameters = append(parameters, "error="+quoted(failure))
	}
	if description != "" {
		parameters = append(parameters, "error_description="+quoted(description))
	}
	parameters = append(parameters, "resource_metadata="+quoted(r.MetadataURL()))
	if len(r.Scopes) > 0 {
		parameters = append(parameters, "scope="+quoted(strings.Join(r.Scopes, " ")))
	}
	return "Bearer " + strings.Join(parameters, ", ")
}

func quoted(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}
