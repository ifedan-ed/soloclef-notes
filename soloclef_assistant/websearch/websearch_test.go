package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// fixedKeys is an account's connected providers.
type fixedKeys map[Provider]string

func (k fixedKeys) Key(_ context.Context, _ string, p Provider) (string, bool, error) {
	key, found := k[p]
	return key, found, nil
}

// provider stands in for Exa or Tavily and records what it was sent.
type provider struct {
	*httptest.Server
	header http.Header
	body   map[string]any
	status int
	reply  string
}

func newProvider(t *testing.T, reply string) *provider {
	p := &provider{status: 200, reply: reply}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.header = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &p.body)
		w.WriteHeader(p.status)
		_, _ = io.WriteString(w, p.reply)
	}))
	t.Cleanup(p.Close)
	return p
}

func run(t *testing.T, s Search, p Provider, args string, reg *citation.Registry) (string, error) {
	t.Helper()
	tl := s.Tool("11111111-1111-4111-8111-111111111111", p)
	if err := tl.Validate(); err != nil {
		t.Fatal(err)
	}
	return tl.Run(context.Background(), tool.Call{Args: json.RawMessage(args), Citations: reg})
}

func TestExaIsAskedWithItsKeyAndEachResultIsItsOwnSource(t *testing.T) {
	exa := newProvider(t, `{"results":[
		{"url":"https://www.example.org/a","title":"First","highlights":["one","two"],"author":"A. Writer","publishedDate":"2026-03-04T10:00:00Z"},
		{"url":"javascript:alert(1)","title":"Not a page"},
		{"url":"https://example.com/b","title":"","text":"body text"}]}`)
	s := Search{Keys: fixedKeys{Exa: "exa-key"}, HTTP: exa.Client(), endpoints: map[Provider]string{Exa: exa.URL}}
	reg := citation.NewRegistry()
	out, err := run(t, s, Exa, `{"query":" go generics ","max_results":50}`, reg)
	if err != nil {
		t.Fatal(err)
	}
	if exa.header.Get("x-api-key") != "exa-key" || exa.header.Get("Authorization") != "" {
		t.Fatalf("headers = %v", exa.header)
	}
	if exa.body["query"] != "go generics" || exa.body["numResults"] != 10.0 || exa.body["type"] != "auto" {
		t.Fatalf("body = %v", exa.body)
	}
	want := "[1] First\nhttps://www.example.org/a\nexample.org · A. Writer · 2026-03-04\none two\n\n" +
		"[2] https://example.com/b\nhttps://example.com/b\nexample.com\nbody text"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if src, _ := reg.Lookup(2); src.Kind != "web_result" || src.Ref != "https://example.com/b" {
		t.Fatalf("label 2 = %+v", src)
	}
}

func TestTavilyIsAskedWithABearerKey(t *testing.T) {
	tavily := newProvider(t, `{"results":[{"url":"https://t.example/x","title":"T","content":"snippet","published_date":"2025-01-02"}]}`)
	s := Search{Keys: fixedKeys{Tavily: "tv-key"}, HTTP: tavily.Client(), endpoints: map[Provider]string{Tavily: tavily.URL}}
	out, err := run(t, s, Tavily, `{"query":"x","max_results":3}`, citation.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if tavily.header.Get("Authorization") != "Bearer tv-key" || tavily.header.Get("x-api-key") != "" {
		t.Fatalf("headers = %v", tavily.header)
	}
	if tavily.body["max_results"] != 3.0 || tavily.body["search_depth"] != "basic" {
		t.Fatalf("body = %v", tavily.body)
	}
	if !strings.Contains(out, "[1] T\nhttps://t.example/x\nt.example · 2025-01-02\nsnippet") {
		t.Fatalf("out = %s", out)
	}
}

// The two are separate tools on separate accounts. One provider's key is
// never sent to the other, and one being down never becomes a search on the
// other.
func TestTheProvidersAreNeverMixed(t *testing.T) {
	exa := newProvider(t, `{"results":[]}`)
	tavily := newProvider(t, `{"results":[]}`)
	tavily.status = 500
	s := Search{Keys: fixedKeys{Exa: "exa-key", Tavily: "tv-key"}, HTTP: http.DefaultClient,
		endpoints: map[Provider]string{Exa: exa.URL, Tavily: tavily.URL}}
	if _, err := run(t, s, Tavily, `{"query":"x"}`, citation.NewRegistry()); err == nil || !strings.Contains(err.Error(), "Tavily is busy") {
		t.Fatalf("err = %v", err)
	}
	if exa.header != nil {
		t.Fatal("a failed Tavily search was sent to Exa")
	}
	if Exa.ToolName() != "exa_web_search" || Tavily.ToolName() != "tavily_web_search" {
		t.Fatal("tool names changed")
	}
}

func TestWhatTheModelIsToldWhenASearchCannotRun(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		keys   fixedKeys
		args   string
		want   string
	}{
		"a rejected key":       {401, fixedKeys{Exa: "k"}, `{"query":"x"}`, "rejected the user's key"},
		"out of quota":         {429, fixedKeys{Exa: "k"}, `{"query":"x"}`, "out of quota"},
		"disconnected since":   {200, fixedKeys{}, `{"query":"x"}`, "not connected any more"},
		"an empty query":       {200, fixedKeys{Exa: "k"}, `{"query":"   "}`, "1 to 400 characters"},
		"a query far too long": {200, fixedKeys{Exa: "k"}, `{"query":"` + strings.Repeat("q", 401) + `"}`, "1 to 400 characters"},
	} {
		exa := newProvider(t, `{"results":[]}`)
		exa.status = c.status
		s := Search{Keys: c.keys, HTTP: exa.Client(), endpoints: map[Provider]string{Exa: exa.URL}}
		_, err := run(t, s, Exa, c.args, citation.NewRegistry())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to say %q", name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "k\"") {
			t.Errorf("%s: the key is in the message", name)
		}
	}
}

func TestNoResultsIsAnAnswerNotAnError(t *testing.T) {
	exa := newProvider(t, `{"results":[]}`)
	s := Search{Keys: fixedKeys{Exa: "k"}, HTTP: exa.Client(), endpoints: map[Provider]string{Exa: exa.URL}}
	out, err := run(t, s, Exa, `{"query":"zebra"}`, citation.NewRegistry())
	if err != nil || out != `Exa found nothing for "zebra".` {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestAKeyIsTakenOnlyFromItsProvidersOwnServer(t *testing.T) {
	for _, c := range []struct {
		name, config string
		provider     Provider
		want         string
	}{
		{"exa header", `{"server_config":{"url":"https://mcp.exa.ai/mcp","headers":{"X-API-Key":" abc "}}}`, Exa, "abc"},
		{"exa query", `{"server_config":{"url":"https://mcp.exa.ai/mcp?exaApiKey=q1"}}`, Exa, "q1"},
		{"tavily bearer", `{"server_config":{"url":"https://mcp.tavily.com/mcp/","headers":{"authorization":"Bearer tv"}}}`, Tavily, "tv"},
		{"tavily query", `{"server_config":{"url":"https://mcp.tavily.com/mcp/?tavilyApiKey=q2"}}`, Tavily, "q2"},
		{"another server's x-api-key is not an Exa key", `{"server_config":{"url":"https://tools.example.com/mcp","headers":{"x-api-key":"other"}}}`, Exa, ""},
		{"a look-alike domain", `{"server_config":{"url":"https://notexa.ai/mcp","headers":{"x-api-key":"other"}}}`, Exa, ""},
		{"exa's server is not tavily's", `{"server_config":{"url":"https://mcp.exa.ai/mcp","headers":{"x-api-key":"abc"}}}`, Tavily, ""},
		{"a config that is not an object", `[]`, Exa, ""},
		{"no key at all", `{"server_config":{"url":"https://mcp.exa.ai/mcp"}}`, Exa, ""},
	} {
		if got := keyFrom([]byte(c.config), c.provider); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// Find is the tool's own path for a caller that words the outcome itself:
// the same request, and the reason readable by code.
func TestFindSearchesAsTheToolDoesAndSaysWhyItCouldNot(t *testing.T) {
	exa := newProvider(t, `{"results":[{"url":"https://a.example/x","title":"A","highlights":["h"]}]}`)
	s := Search{Keys: fixedKeys{Exa: "k"}, HTTP: exa.Client(), endpoints: map[Provider]string{Exa: exa.URL}}
	results, err := s.Find(context.Background(), "u", Exa, "tides", 25)
	if err != nil || len(results) != 1 || results[0].URL != "https://a.example/x" {
		t.Fatalf("results = %v, %v", results, err)
	}
	// Held to what one request returns, whatever was asked for.
	if exa.header.Get("x-api-key") != "k" || exa.body["numResults"] != float64(maxResults) {
		t.Errorf("sent %v with %v", exa.body, exa.header)
	}
	for name, c := range map[string]struct {
		status   int
		provider Provider
		want     error
	}{
		"not connected":  {200, Tavily, ErrNotConnected},
		"a rejected key": {401, Exa, ErrKeyRejected},
		"out of quota":   {429, Exa, ErrQuota},
	} {
		exa.status = c.status
		if _, err := s.Find(context.Background(), "u", c.provider, "tides", 5); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := s.Find(context.Background(), "u", Provider("bing"), "tides", 5); err == nil {
		t.Error("an unknown provider was searched")
	}
}
