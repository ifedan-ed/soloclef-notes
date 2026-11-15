package searchconnectors

import (
	"encoding/json"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
)

// sealedServerJSON is a server_config as the API stores one: sealed for its
// owner, with a bearer token when token is not empty. t may be nil for a
// fixture built outside a test; a failure then panics.
func sealedServerJSON(t testing.TB, owner, url, token string) string {
	if t == nil {
		t = panicking{}
	}
	t.Helper()
	auth := mcpserver.Auth{Kind: mcpserver.AuthNone}
	if token != "" {
		auth = mcpserver.Auth{Kind: mcpserver.AuthBearer, Secret: token}
	}
	stored, err := mcpserver.Build(testServerSeal, owner, mcpserver.Settings{Address: url, Transport: mcpserver.StreamableHTTP, Auth: auth}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// sealedServerColumn is the same, read as a config column is read.
func sealedServerColumn(t testing.TB, owner, url string) any {
	t.Helper()
	value, err := readColumn([]byte(sealedServerJSON(t, owner, url, "")))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// panicking is a testing.TB for fixtures made outside a test.
type panicking struct{ testing.TB }

func (panicking) Helper()           {}
func (panicking) Fatal(args ...any) { panic(args) }
