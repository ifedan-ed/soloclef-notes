package mcptools

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"testing"
	"time"
)

// The form's test button against a real MCP server: it carries the key,
// follows the tool list past its first page, stops at the ceiling, hangs
// up, and fails where the guard refuses.
func TestProbeListsAServersTools(t *testing.T) {
	server := newRemote(t, 2)
	for _, name := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		server.add(name, says("ok"))
	}
	probe := Probe{Network: loopback{}, ClientName: "probe-under-test", Timeout: 5 * time.Second, MostTools: 4}
	if err := probe.Check(); err != nil {
		t.Fatal(err)
	}
	tools, err := probe.Tools(context.Background(), server.URL, map[string]string{"x-api-key": "fake-key"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Five tools, two a page, a ceiling of four: the second page was asked
	// for and the third was cut.
	if len(tools) != 4 {
		t.Fatalf("listed %d tools, want the ceiling of 4: %v", len(tools), tools)
	}
	if tools[0].Description == "" || string(tools[0].InputSchema) == "" || tools[0].InputSchema[0] != '{' {
		t.Errorf("a tool lost its description or schema: %+v", tools[0])
	}
	if !server.sent("x-api-key", "fake-key") {
		t.Error("the key did not reach the server")
	}
	if server.closed() != 1 {
		t.Errorf("the session was closed %d times, want once", server.closed())
	}

	// A wrong key is an error, and nothing more is said of it here.
	server.status.Store(http.StatusUnauthorized)
	if _, err := probe.Tools(context.Background(), server.URL, nil, nil); !errors.Is(err, errUnauthorized) {
		t.Errorf("a 401 was reported as %v", err)
	}

	// The production guard refuses the same loopback address before any
	// connection is made.
	guarded := Probe{Network: PublicInternet{Resolve: SystemResolve, ConnectTimeout: time.Second},
		ClientName: "probe-under-test", Timeout: time.Second, MostTools: 4}
	before := server.requests.Load()
	var refused *Refusal
	if _, err := guarded.Tools(context.Background(), server.URL, nil, []string{"127.0.0.1"}); !errors.As(err, &refused) {
		t.Errorf("a loopback address was not refused: %v", err)
	}
	if server.requests.Load() != before {
		t.Error("a refused test still reached the server")
	}
}

// Verify is the save-time answer: the shape of the address, the list, and
// what the name resolves to now.
func TestVerifyRefusesWhatACallWouldRefuse(t *testing.T) {
	answers := map[string][]netip.Addr{
		"public.example.test": {netip.MustParseAddr("93.184.216.34")},
		"inside.example.test": {netip.MustParseAddr("10.1.2.3")},
		"mixed.example.test":  {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")},
	}
	guard := PublicInternet{ConnectTimeout: time.Second, Resolve: func(_ context.Context, host string) ([]netip.Addr, error) {
		if found, known := answers[host]; known {
			return found, nil
		}
		return nil, errors.New("no such host")
	}}
	all := []string{"public.example.test", "inside.example.test", "mixed.example.test", "unknown.example.test"}
	for address, admitted := range map[string]bool{
		"https://public.example.test/mcp":      true,
		"https://PUBLIC.example.test./mcp":     true,
		"http://public.example.test/mcp":       false,
		"https://public.example.test:8443/mcp": false,
		"https://user:pw@public.example.test/": false,
		"https://inside.example.test/mcp":      false,
		"https://mixed.example.test/mcp":       false,
		"https://unknown.example.test/mcp":     false,
		"https://elsewhere.example.test/mcp":   false,
	} {
		err := guard.Verify(context.Background(), address, all)
		var refused *Refusal
		if admitted && err != nil {
			t.Errorf("%s was refused: %v", address, err)
		}
		if !admitted && !errors.As(err, &refused) {
			t.Errorf("%s was not refused (%v)", address, err)
		}
	}
	if got := ReachableHosts(OriginMember, "https://litellm/mcp", nil); len(got) != 0 {
		t.Errorf("a container's name may reach %v", got)
	}
	if got := SiteHosts(nil, []string{"configured.example"}); len(got) != 1 || got[0] != "configured.example" {
		t.Errorf("with no saved list the hosts are %v", got)
	}
	if got := SiteHosts([]byte(`[]`), []string{"configured.example"}); len(got) != 0 {
		t.Errorf("a saved empty list was read as %v", got)
	}
}
