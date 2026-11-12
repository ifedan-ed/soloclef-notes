package mcptools

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOnlyAPlainHTTPSAddressOnTheListPasses(t *testing.T) {
	hosts := []string{"mcp.example.com", "Tools.Example.org."}
	for _, address := range []string{
		"https://mcp.example.com/mcp", "https://MCP.example.com:443/mcp?key=k", " https://tools.example.org/ ", "https://mcp.example.com./x",
	} {
		if host, err := checkAddress(address, hosts); err != nil || host == "" {
			t.Errorf("%q is refused: %v", address, err)
		}
	}
	for label, address := range map[string]string{
		"plain http":            "http://mcp.example.com/mcp",
		"another scheme":        "file:///etc/passwd",
		"credentials":           "https://user:pass@mcp.example.com/mcp",
		"another port":          "https://mcp.example.com:8443/mcp",
		"a host off the list":   "https://evil.example.net/mcp",
		"a lookalike host":      "https://mcp.example.com.evil.net/mcp",
		"an IPv4 address":       "https://93.184.216.34/mcp",
		"an IPv6 address":       "https://[2606:4700::1]/mcp",
		"loopback by number":    "https://127.0.0.1/mcp",
		"no host":               "https:///mcp",
		"nothing":               "  ",
		"not a URL":             "https://mcp.example.com/%zz",
		"a host in credentials": "https://mcp.example.com@evil.example.net/",
	} {
		_, err := checkAddress(address, hosts)
		var refusal *Refusal
		if !errors.As(err, &refusal) {
			t.Errorf("%s (%q) passes: %v", label, address, err)
		}
	}
	if _, err := checkAddress("https://mcp.example.com/mcp", nil); err == nil {
		t.Error("an empty list lets a host through")
	}
}

func TestWhichAddressesArePublic(t *testing.T) {
	for _, address := range []string{"93.184.216.34", "8.8.8.8", "2606:4700:4700::1111", "::ffff:8.8.8.8"} {
		if !publicAddress(netip.MustParseAddr(address)) {
			t.Errorf("%s is refused", address)
		}
	}
	for label, address := range map[string]string{
		"loopback":                 "127.0.0.1",
		"loopback, high":           "127.255.255.254",
		"loopback written as IPv6": "::ffff:127.0.0.1",
		"private 10":               "10.1.2.3",
		"private 172":              "172.20.0.5",
		"private 192":              "192.168.1.1",
		"private written as IPv6":  "::ffff:10.0.0.1",
		"link-local, metadata":     "169.254.169.254",
		"carrier-grade NAT":        "100.64.0.1",
		"unspecified":              "0.0.0.0",
		"this network":             "0.1.2.3",
		"documentation":            "203.0.113.9",
		"benchmarking":             "198.18.0.1",
		"reserved":                 "240.0.0.1",
		"broadcast":                "255.255.255.255",
		"multicast":                "224.0.0.1",
		"IPv6 loopback":            "::1",
		"IPv6 unspecified":         "::",
		"IPv6 unique local":        "fd12:3456::1",
		"IPv6 link-local":          "fe80::1",
		"IPv6 link-local, scoped":  "fe80::1%eth0",
		"IPv6 multicast":           "ff02::1",
		"IPv6 documentation":       "2001:db8::1",
		"NAT64 of a private host":  "64:ff9b::a00:1",
		"6to4 of a private host":   "2002:a00:1::1",
	} {
		if publicAddress(netip.MustParseAddr(address)) {
			t.Errorf("%s (%s) counts as public", label, address)
		}
	}
	if publicAddress(netip.Addr{}) {
		t.Error("no address at all counts as public")
	}
}

// answers is a resolver that knows a few names.
func answers(table map[string][]string) func(context.Context, string) ([]netip.Addr, error) {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		listed, found := table[host]
		if !found {
			return nil, errors.New("no such host")
		}
		out := make([]netip.Addr, len(listed))
		for i, text := range listed {
			out[i] = netip.MustParseAddr(text)
		}
		return out, nil
	}
}

// The dialer is asked to connect to a name. What it actually connects to is
// recorded here instead of being reached.
func recordingInternet(table map[string][]string, fail map[string]bool) (PublicInternet, *[]string) {
	var dialled []string
	return PublicInternet{
		Resolve: answers(table), ConnectTimeout: time.Second,
		dial: func(_ context.Context, network, address string) (net.Conn, error) {
			dialled = append(dialled, network+" "+address)
			if fail[address] {
				return nil, errors.New("connection refused")
			}
			client, server := net.Pipe()
			_ = server.Close()
			return client, nil
		},
	}, &dialled
}

func TestTheDialerConnectsToTheAddressItCheckedAndToNoOther(t *testing.T) {
	table := map[string][]string{
		"mcp.example.com":      {"2606:4700::1", "93.184.216.34", "::ffff:8.8.4.4"},
		"rebound.example.com":  {"93.184.216.34", "10.0.0.7"},
		"inside.example.com":   {"127.0.0.1"},
		"metadata.example.com": {"169.254.169.254"},
		"mapped.example.com":   {"::ffff:192.168.0.10"},
		"empty.example.com":    {},
	}
	allowed := map[string]bool{}
	for name := range table {
		allowed[name] = true
	}
	allowed["unknown.example.com"] = true
	ctx := context.Background()

	t.Run("a public name is reached by number, IPv4 first", func(t *testing.T) {
		internet, dialled := recordingInternet(table, map[string]bool{"93.184.216.34:443": true})
		conn, err := internet.pinnedDial(allowed)(ctx, "tcp", "MCP.example.com.:443")
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
		// The first address failed to connect, so the next was tried; both
		// IPv4 addresses came before the IPv6 one, which was never needed.
		want := []string{"tcp 93.184.216.34:443", "tcp 8.8.4.4:443"}
		if !reflect.DeepEqual(*dialled, want) {
			t.Errorf("dialled %v, want %v", *dialled, want)
		}
	})

	for label, address := range map[string]string{
		"a name with one private answer among public ones": "rebound.example.com:443",
		"a name that resolves to loopback":                 "inside.example.com:443",
		"a name that resolves to the metadata address":     "metadata.example.com:443",
		"a private address written as IPv6":                "mapped.example.com:443",
		"a name with no addresses":                         "empty.example.com:443",
		"a name that does not resolve":                     "unknown.example.com:443",
		"a host that is not on the list":                   "elsewhere.example.com:443",
		"another port":                                     "mcp.example.com:8443",
		"an address by number":                             "93.184.216.34:443",
		"loopback by number":                               "127.0.0.1:443",
	} {
		t.Run(label, func(t *testing.T) {
			internet, dialled := recordingInternet(table, nil)
			conn, err := internet.pinnedDial(allowed)(ctx, "tcp", address)
			var refusal *Refusal
			if !errors.As(err, &refusal) {
				if conn != nil {
					_ = conn.Close()
				}
				t.Fatalf("not refused: %v", err)
			}
			if len(*dialled) != 0 {
				t.Errorf("a connection was attempted to %v before the refusal", *dialled)
			}
		})
	}
}

// The whole client, not only its dialer: a request through PublicInternet
// to a name that resolves to this machine never arrives.
func TestAServerOnLoopbackIsNeverReachedThroughThePublicInternet(t *testing.T) {
	var hits atomic.Int64
	inside := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer inside.Close()

	internet := PublicInternet{Resolve: answers(map[string][]string{"inside.example.com": {"127.0.0.1"}}), ConnectTimeout: time.Second}
	client, err := internet.Client("https://inside.example.com/mcp", []string{"inside.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Get("https://inside.example.com/mcp")
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("the request was not refused: %v", err)
	}
	// Nor by its number, which Client refuses before there is a client.
	if _, err := internet.Client(inside.URL, []string{"127.0.0.1"}); !errors.As(err, &refusal) {
		t.Errorf("an address by number was given a client: %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("the server on loopback was reached %d times", hits.Load())
	}
}

func TestProxySettingsOfTheEnvironmentAreIgnored(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	internet, dialled := recordingInternet(map[string][]string{"mcp.example.com": {"93.184.216.34"}}, nil)
	client, err := internet.Client("https://mcp.example.com/mcp", []string{"mcp.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	// The connection is a closed pipe, so the request fails; what matters
	// is where it was dialled.
	_, _ = client.Get("https://mcp.example.com/mcp")
	if want := []string{"tcp 93.184.216.34:443"}; !reflect.DeepEqual(*dialled, want) {
		t.Errorf("dialled %v, want %v: a proxy was used", *dialled, want)
	}
}

func TestARedirectIsNotFollowed(t *testing.T) {
	var elsewhere atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere.Add(1) }))
	defer target.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/stolen", http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()

	// The client itself.
	client, _ := loopback{}.Client("", nil)
	response, err := client.Post(redirecting.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect {
		t.Errorf("status = %d, want the redirect itself handed back", response.StatusCode)
	}

	// And a whole turn against a server that redirects: its tools are not
	// listed, a call fails, and the key in its headers went nowhere else.
	rows := newRows()
	rows.add(ada, 1, "Redirects", serverConfig(redirecting.URL, "secret-key-12345678"))
	rows.add(ada, 2, "Redirects, with a saved list", serverConfig(redirecting.URL, "secret-key-12345678", cachedList("lookup")))
	w := newWorld(t, rows, newCounter(), nil)
	set := w.turn(t, ada)
	if got := names(set); !reflect.DeepEqual(got, []string{"lookup"}) {
		t.Fatalf("tools = %v", got)
	}
	if _, err := call(t, set, "lookup", `{}`); err == nil || err.Error() != unreachable {
		t.Errorf("a call through a redirect: %v", err)
	}
	if elsewhere.Load() != 0 {
		t.Errorf("the redirect was followed %d times", elsewhere.Load())
	}
}

func TestWhichHostsAServerMayReach(t *testing.T) {
	site := []string{"mcp.exa.ai", "mcp.tavily.com"}
	if got := reachableHosts(originSite, "https://anything.example.com/mcp", site); !reflect.DeepEqual(got, site) {
		t.Errorf("a site server may reach %v", got)
	}
	if got := reachableHosts(originMember, "https://Tools.Example.com./mcp", site); !reflect.DeepEqual(got, []string{"tools.example.com"}) {
		t.Errorf("a member's server may reach %v", got)
	}
	// Names that only mean something inside a network are refused before
	// any lookup: the name of the container next door has no dot.
	for _, address := range []string{"https://litellm/mcp", "https://db.internal/mcp", "https://printer.local/", "https://x.localhost/", "https://nas.lan/", "https://a.home.arpa/", "::"} {
		if got := reachableHosts(originMember, address, site); len(got) != 0 {
			t.Errorf("%s: a member's server may reach %v", address, got)
		}
	}

	// The administrators' list is the stored one when there is one, and
	// the deployment's stated list only when none was ever saved.
	source := &Source{siteHosts: []string{"unset.example.com"}}
	where := target{origin: originSite, address: "https://x.example.com/"}
	for stored, want := range map[string][]string{
		`["mcp.exa.ai", 5, "", "mcp.tavily.com"]`: {"mcp.exa.ai", "mcp.tavily.com"},
		`[]`:                           {},
		``:                             {"unset.example.com"},
		`null`:                         {"unset.example.com"},
		`{"hosts": ["a.example.com"]}`: {"unset.example.com"},
	} {
		if got := source.hosts(where, []byte(stored)); !reflect.DeepEqual(got, want) {
			t.Errorf("stored %q: hosts = %v, want %v", stored, got, want)
		}
	}
}

func TestTheAddressAndTheKeyNeverReachTheLog(t *testing.T) {
	where := target{address: "https://mcp.example.com/mcp?exaApiKey=sk-live-123456", headers: map[string]string{"Authorization": "Bearer tok-abcdefgh"}}
	client, _ := loopback{}.Client("", nil)
	// A real network error, of the kind that quotes the whole address.
	_, err := client.Get("http://127.0.0.1:1/mcp?exaApiKey=sk-live-123456")
	if err == nil || !strings.Contains(err.Error(), "sk-live-123456") {
		t.Fatalf("the test's premise is wrong: %v", err)
	}
	for _, failure := range []error{err, errors.New(`sending "initialize": Post "https://mcp.example.com/mcp?exaApiKey=sk-live-123456": refused, sent Bearer tok-abcdefgh`)} {
		if text := redact(failure, where); strings.Contains(text, "sk-live") || strings.Contains(text, "tok-abcdefgh") {
			t.Errorf("the log line holds a key: %s", text)
		}
	}
}
