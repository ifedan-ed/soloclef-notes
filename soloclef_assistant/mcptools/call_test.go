package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// notes is a server with the tools the call tests use, and an account
// connected to it with its list saved.
func notes(t *testing.T) (*remote, *world) {
	t.Helper()
	server := newRemote(t, 0)
	server.add("echo", func(args json.RawMessage) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "you sent " + string(args)}}}
	})
	server.add("fails", func(json.RawMessage) *mcp.CallToolResult {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "no note has that id"}}}
	})
	server.add("fails_silently", func(json.RawMessage) *mcp.CallToolResult {
		return &mcp.CallToolResult{IsError: true}
	})
	server.add("picture", func(json.RawMessage) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "the chart:"},
			&mcp.ImageContent{MIMEType: "image/png", Data: []byte(strings.Repeat("\x89PNG", 500))},
			&mcp.AudioContent{MIMEType: "audio/wav", Data: []byte("RIFF")},
			&mcp.ResourceLink{Name: "report", URI: "https://files.example.com/report.pdf"},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "note://1", Text: "the note's text"}},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "note://2", MIMEType: "application/pdf", Blob: []byte("%PDF")}},
		}}
	})
	server.add("structured", func(json.RawMessage) *mcp.CallToolResult {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"count": 3}}
	})
	server.add("long", says(strings.Repeat("é", 5000)))
	server.add("slow", func(json.RawMessage) *mcp.CallToolResult {
		time.Sleep(700 * time.Millisecond)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "late"}}}
	})
	rows := newRows()
	rows.add(ada, 1, "Notes", serverConfig(server.URL, "first-key-12345678",
		cachedList("echo", "fails", "fails_silently", "picture", "structured", "long", "slow", "not_on_the_server")))
	return server, newWorld(t, rows, newCounter(), nil)
}

func TestATurnOpensOneConnectionPerServerAndClosesIt(t *testing.T) {
	server, w := notes(t)
	set := w.turn(t, ada)
	if server.requests.Load() != 0 {
		t.Fatal("a connection was opened before any call")
	}
	for _, args := range []string{`{"q":"one"}`, `{"q":"two"}`, ``} {
		got, err := call(t, set, "echo", args)
		want := args
		if want == "" {
			want = "{}"
		}
		if err != nil || got != "you sent "+want {
			t.Fatalf("echo(%s) = %q, %v", args, got, err)
		}
	}
	if opened := server.handshakes(); opened != 1 {
		t.Errorf("%d connections were opened for three calls, want 1", opened)
	}
	if !server.sent("x-api-key", "first-key-12345678") {
		t.Error("the connection's key was not sent")
	}
	if server.closed() != 0 {
		t.Fatal("the session was ended before the turn was")
	}
	set.Close()
	set.Close() // twice is fine
	if server.closed() != 1 {
		t.Errorf("the server was told the session ended %d times, want 1", server.closed())
	}
	if _, err := call(t, set, "echo", `{}`); err == nil || err.Error() != ended {
		t.Errorf("a call after Close: %v", err)
	}
	// The next turn is a new connection.
	if _, err := call(t, w.turn(t, ada), "echo", `{}`); err != nil {
		t.Fatal(err)
	}
	if opened := server.handshakes(); opened != 2 {
		t.Errorf("%d connections after two turns, want 2", opened)
	}
}

// The address, the key and whether the server is still there are read from
// its row at each call. A turn can wait hours for an approval; what it
// believed when it was built must not be what it acts on.
func TestACallUsesTheRowAsItIsNow(t *testing.T) {
	server, w := notes(t)
	set := w.turn(t, ada)
	if _, err := call(t, set, "echo", `{}`); err != nil {
		t.Fatal(err)
	}

	// The key is changed in settings while the turn is open.
	w.rows.add(ada, 1, "Notes", serverConfig(server.URL, "second-key-12345678"))
	if _, err := call(t, set, "echo", `{}`); err != nil {
		t.Fatal(err)
	}
	if !server.sent("x-api-key", "second-key-12345678") {
		t.Error("the call after the key changed did not carry the new key")
	}
	if server.handshakes() != 2 || server.closed() != 1 {
		t.Errorf("the old connection was not replaced: %d opened, %d closed", server.handshakes(), server.closed())
	}

	// The server is moved somewhere this chat may not go.
	other := newRemote(t, 0)
	other.add("echo", says("from the other server"))
	w.rows.add(ada, 1, "Notes", serverConfig(other.URL, "second-key-12345678"))
	if got, err := call(t, set, "echo", `{}`); err != nil || got != "from the other server" {
		t.Errorf("after the address changed: %q, %v", got, err)
	}

	// It is made a local command, which is never started.
	w.rows.add(ada, 1, "Notes", `{"server_config": {"transport": "stdio", "command": "rm"}}`)
	if _, err := call(t, set, "echo", `{}`); err == nil || err.Error() != unavailable {
		t.Errorf("after it became a local command: %v", err)
	}

	// It is removed. The tool is still in this turn's list, and its call
	// is refused with nothing sent anywhere.
	w.rows.remove(ada, 1)
	before := server.requests.Load() + other.requests.Load()
	if _, err := call(t, set, "echo", `{}`); err == nil || err.Error() != gone {
		t.Errorf("after the server was removed: %v", err)
	}
	if server.requests.Load()+other.requests.Load() != before {
		t.Error("a removed server was contacted")
	}
}

func TestWhatTheModelReadsFromAResult(t *testing.T) {
	_, w := notes(t)
	set := w.turn(t, ada)

	// A tool that says it failed is a failed result, with its own words.
	if _, err := call(t, set, "fails", `{}`); err == nil || err.Error() != "no note has that id" {
		t.Errorf("an isError result: %v", err)
	}
	if _, err := call(t, set, "fails_silently", `{}`); err == nil {
		t.Error("an isError result with no text was a success")
	}
	// A tool the server does not have is the server's refusal, and the
	// connection survives it.
	if _, err := call(t, set, "not_on_the_server", `{}`); err == nil || !strings.HasPrefix(err.Error(), "the MCP server refused the call: ") {
		t.Errorf("an unknown tool: %v", err)
	}

	got, err := call(t, set, "picture", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	want := "the chart:\n[an image, image/png, 2000 bytes, not shown]\n[audio, audio/wav, 4 bytes, not shown]\n" +
		"[a link to a resource: report https://files.example.com/report.pdf]\nthe note's text\n" +
		"[a resource, note://2, application/pdf, 4 bytes, not shown]"
	if got != want {
		t.Errorf("mixed content:\n got %q\nwant %q", got, want)
	}

	if got, err := call(t, set, "structured", `{}`); err != nil || got != `{"count":3}` {
		t.Errorf("structured content only: %q, %v", got, err)
	}

	// A long result is cut at the limit, on a character, and says so.
	got, err = call(t, set, "long", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	body, note, cut := strings.Cut(got, "\n")
	if !cut || len(body) != 4000 || strings.Trim(body, "é") != "" || note != "[the result was cut at 4000 bytes of 10000]" {
		t.Errorf("a long result: %d bytes, note %q", len(body), note)
	}
}

func TestACallHasATimeLimitAndAStoppedRunIsNotAToolError(t *testing.T) {
	server, _ := notes(t)
	rows := newRows()
	rows.add(ada, 1, "Notes", serverConfig(server.URL, "k", cachedList("slow", "echo")))
	w := newWorld(t, rows, newCounter(), func(s *Settings) { s.Limits.Call = 150 * time.Millisecond })
	set := w.turn(t, ada)

	started := time.Now()
	_, err := call(t, set, "slow", `{}`)
	if err == nil || err.Error() != "slow did not answer within 150ms" {
		t.Errorf("a slow call: %v", err)
	}
	if took := time.Since(started); took > 600*time.Millisecond {
		t.Errorf("the call was held for %s", took)
	}
	// The turn goes on: the next call gets a fresh connection.
	if _, err := call(t, set, "echo", `{}`); err != nil {
		t.Errorf("the call after a timeout: %v", err)
	}

	// The run being stopped is the caller's context ending. That is not
	// something to tell the model; the loop has to see it as it is.
	stopping, stop := context.WithCancel(context.Background())
	stop()
	_, err = set.Tools()[0].Run(stopping, tool.Call{Name: "slow", Args: json.RawMessage(`{}`)})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped run: %v", err)
	}
}

func TestARefusedKeyIsFlaggedOnceAndSaidPlainly(t *testing.T) {
	server := newRemote(t, 0)
	server.add("lookup", says("ok"))
	server.status.Store(401)
	rows, shared := newRows(), newCounter()
	rows.add(ada, 1, "Expired", serverConfig(server.URL, "old-key-12345678", cachedList("lookup")))
	rows.add(ada, 2, "Expired, never listed", serverConfig(server.URL, "old-key-12345678"))
	w := newWorld(t, rows, shared, nil)

	set := w.turn(t, ada)
	// Server 2 had to be asked for its tools and answered 401.
	if string(rows.config(ada, 2)["auth_expired"]) != "true" {
		t.Error("a 401 while listing tools did not flag the connection")
	}
	if string(rows.config(ada, 1)["auth_expired"]) == "true" {
		t.Fatal("a server nobody contacted was flagged")
	}
	for range 2 {
		if _, err := call(t, set, "lookup", `{}`); err == nil || err.Error() != keyRefused {
			t.Fatalf("a call with a refused key: %v", err)
		}
	}
	if string(rows.config(ada, 1)["auth_expired"]) != "true" {
		t.Error("a 401 on a call did not flag the connection")
	}
	// One bump per connection flagged, not one per failed call.
	if shared.at(ada) != 2 {
		t.Errorf("the counter is at %d, want 2", shared.at(ada))
	}
	if logged := w.logs.String(); strings.Contains(logged, "old-key") || strings.Contains(logged, server.URL) {
		t.Errorf("the log holds the key or the address:\n%s", logged)
	}
}

func TestAnUnreachableServerCostsOneResultAndNamesNoAddress(t *testing.T) {
	server := newRemote(t, 0)
	address := server.URL
	server.Close()
	rows := newRows()
	rows.add(ada, 1, "Gone away", `{"server_config": {"transport": "http", "url": "`+address+`/mcp?apiKey=sk-live-12345678"}, `+cachedList("lookup")+`}`)
	w := newWorld(t, rows, newCounter(), nil)
	_, err := call(t, w.turn(t, ada), "lookup", `{}`)
	if err == nil || err.Error() != unreachable {
		t.Errorf("a call to a dead server: %v", err)
	}
	if logged := w.logs.String(); !strings.Contains(logged, "a call failed") || strings.Contains(logged, "sk-live") || strings.Contains(logged, address) {
		t.Errorf("the log:\n%s", logged)
	}
}

// With the production Network, the same rows that work on loopback in every
// other test offer nothing and reach nothing: the guard is what the tests
// replace, by name, and not something they switch off.
func TestThePublicInternetRefusesTheTestsOwnServers(t *testing.T) {
	server := newRemote(t, 0)
	server.add("lookup", says("ok"))
	rows := newRows()
	rows.add(ada, 1, "Saved", serverConfig(server.URL, "k", cachedList("lookup")))
	rows.add(ada, 2, "Not saved", serverConfig(server.URL, "k"))
	w := newWorld(t, rows, newCounter(), func(s *Settings) {
		s.Network = PublicInternet{Resolve: SystemResolve, ConnectTimeout: time.Second}
	})
	if got := names(w.turn(t, ada)); len(got) != 0 {
		t.Errorf("tools on loopback are offered: %v", got)
	}
	if server.requests.Load() != 0 {
		t.Errorf("a server on loopback was contacted %d times", server.requests.Load())
	}

	// A site server off the administrators' list, with a saved tool list:
	// not offered. On the list: offered, and its call refused only because
	// the name resolves to this machine.
	rows = newRows()
	rows.add(ada, 1, "Site", `{"server_config": {"transport": "http", "url": "https://mcp.example.com/mcp"}, `+cachedList("lookup")+`}`)
	internet := PublicInternet{Resolve: answers(map[string][]string{"mcp.example.com": {"127.0.0.1"}}), ConnectTimeout: time.Second}
	w = newWorld(t, rows, newCounter(), func(s *Settings) { s.Network = internet })
	if got := names(w.turn(t, ada)); len(got) != 0 {
		t.Errorf("a site server off the list is offered: %v", got)
	}
	rows.siteHosts = []byte(`["mcp.example.com"]`)
	_ = w.source.Bump(context.Background(), ada)
	set := w.turn(t, ada)
	if got := names(set); len(got) != 1 {
		t.Fatalf("a site server on the list is not offered: %v", got)
	}
	if _, err := call(t, set, "lookup", `{}`); err == nil || err.Error() != unavailable {
		t.Errorf("a call to a name that resolves to loopback: %v", err)
	}
	if !strings.Contains(w.logs.String(), "not public") {
		t.Error("the log does not say why the call was refused")
	}
	// The list is read with the row at each call: taken off it, the same
	// open turn's next call is refused before any lookup.
	rows.siteHosts = []byte(`[]`)
	if _, err := call(t, set, "lookup", `{}`); err == nil || err.Error() != unavailable {
		t.Errorf("after the host left the list: %v", err)
	}
}
