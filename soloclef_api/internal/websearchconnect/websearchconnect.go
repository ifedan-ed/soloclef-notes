// Package websearchconnect is an account's Exa and Tavily connections: the
// keys the chat's web search tools and the notebook research box search
// with, on the person's own provider account.
//
//	GET    /api/v1/connectors/web-search              both providers: connected or not, and the key's mask
//	PUT    /api/v1/connectors/web-search/{provider}   connect one (or replace its key), checked first
//	DELETE /api/v1/connectors/web-search/{provider}   disconnect one
//
// # What this does better than the MCP servers it replaces
//
// A provider used to be connected by adding its MCP server, and the chat dug
// the key out of whatever headers or address that server was saved with, in
// the clear. Now:
//
//   - the key is checked against the provider before it is saved, with the
//     very request the tools make (websearch.Verify), so a typo is said at
//     the form and not in the middle of a chat;
//   - it is stored sealed (sealedsecret, the web search purpose, bound to
//     the owner) and opened only for a search; no answer carries more of it
//     than its mask;
//   - connecting and disconnecting are a person's decision: a session only,
//     never an API key or an agent, as for an MCP server.
//
// Every statement names the owner (queries.sql).
package websearchconnect

import (
	"context"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
)

// The paths.
const (
	ListPath     = "/api/v1/connectors/web-search"
	ProviderPath = "/api/v1/connectors/web-search/{provider}"
)

// BodyRoutes is every route of this package that reads a small JSON body
// through the buffered reader, as "METHOD path" (cmd/api buffers them).
func BodyRoutes() []string { return []string{"PUT " + ProviderPath} }

// Served is every route of this package, as "METHOD path".
func Served() []string {
	return []string{"GET " + ListPath, "PUT " + ProviderPath, "DELETE " + ProviderPath}
}

// providers are the two, in the order the screen lists them.
var providers = []websearch.Provider{websearch.Exa, websearch.Tavily}

// providerNamed is the provider a path names, or false.
func providerNamed(name string) (websearch.Provider, bool) {
	for _, provider := range providers {
		if string(provider) == name {
			return provider, true
		}
	}
	return "", false
}

// Connected is one provider's connection as stored.
type Connected struct {
	Provider string
	KeyMask  string
	// ConnectedAt is when the key now stored was connected.
	ConnectedAt time.Time
}

// Rows is the package's statements (store.go over Postgres).
type Rows interface {
	Connections(ctx context.Context, ownerID string) ([]Connected, error)
	// Connect stores a provider's sealed key, replacing one stored before.
	Connect(ctx context.Context, ownerID, provider, sealedKey, keyMask string) (Connected, error)
	// Disconnect removes it; removed is false when there was none.
	Disconnect(ctx context.Context, ownerID, provider string) (removed bool, err error)
}

// Verifier asks a provider whether a key is one it accepts
// (websearch.Search.Verify).
type Verifier interface {
	Verify(ctx context.Context, provider websearch.Provider, key string) error
}
