package searchconnectors

import (
	"maps"
	"slices"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
)

// What any reader is shown of an MCP server's stored settings: the form's
// shape, with each secret as the mask computed when it was sealed. Nothing
// is opened to show it, and no ciphertext leaves: a sealed value copied out
// of an answer could be written into another row, and even bound to its
// owner it is nobody's business but the server's.
//
//	{
//	  "url": "https://mcp.example.com/mcp?api_key=••••abcd",
//	  "transport": "streamable-http",
//	  "auth": {"type": "header", "header_name": "X-API-Key", "value": "••••wxyz"},
//	  "headers": {"X-Org": "••••"}
//	}
//
// An OAuth server is shown as whether it is signed in, to which
// authorization server, and as which client, never its tokens:
//
//	"auth": {"type": "oauth", "client_id": "abc" | null, "client_secret": "••••" | null,
//	         "dynamic_registration": false, "authorization_server": "https://…" | null,
//	         "authorized": true}

// publicServer is that answer for one row's server_config. A server_config
// that is not the sealed form is errRowRefused: a row that cannot be shown
// truthfully is not shown.
func publicServer(connection Connection) (*pythonjson.Object, error) {
	if _, err := validated(connection); err != nil {
		return nil, err
	}
	stored, found := storedServerOf(connection)
	if !found {
		return nil, errRowRefused
	}
	return publicStored(stored), nil
}

// publicStored is the answer for settings as stored.
func publicStored(stored mcpserver.Stored) *pythonjson.Object {
	shown := pythonjson.NewObject()
	shown.Set("url", stored.URL)
	shown.Set("transport", stored.Transport)

	auth := pythonjson.NewObject()
	auth.Set("type", string(stored.Auth.Type))
	switch stored.Auth.Type {
	case mcpserver.AuthHeader:
		auth.Set("header_name", stored.Auth.HeaderName)
		auth.Set("value", stored.Auth.Secret.Mask)
	case mcpserver.AuthBearer:
		auth.Set("token", stored.Auth.Secret.Mask)
	case mcpserver.AuthOAuth:
		oauth := stored.Auth.OAuth
		auth.Set("client_id", textOrNull(oauth.ClientID))
		if oauth.ClientSecret != nil {
			auth.Set("client_secret", oauth.ClientSecret.Mask)
		} else {
			auth.Set("client_secret", nil)
		}
		auth.Set("dynamic_registration", oauth.DynamicRegistration)
		auth.Set("authorization_server", textOrNull(oauth.Issuer))
		auth.Set("authorized", oauth.Authorized())
	}
	shown.Set("auth", auth)

	headers := pythonjson.NewObject()
	for _, name := range sortedNames(stored.Headers) {
		headers.Set(name, stored.Headers[name].Mask)
	}
	shown.Set("headers", headers)
	return shown
}

func textOrNull(text string) any {
	if text == "" {
		return nil
	}
	return text
}

// sortedNames is a map's keys in order, so an answer is the same bytes
// each time it is asked.
func sortedNames(headers map[string]mcpserver.Secret) []string {
	return slices.Sorted(maps.Keys(headers))
}

// publicConnectorConfig is the public part of an MCP row's whole config, as
// the generic connection reads show it: its server, shown as above, beside
// the keys public_config.go lets through.
func publicConnectorConfig(connection Connection, config *pythonjson.Object) (*pythonjson.Object, error) {
	shown := pythonjson.NewObject()
	for _, key := range config.Keys() {
		value, _ := config.Get(key)
		if key != "server_config" {
			shown.Set(key, value)
			continue
		}
		server, err := publicServer(connection)
		if err != nil {
			return nil, err
		}
		shown.Set(key, server)
	}
	return shown, nil
}
