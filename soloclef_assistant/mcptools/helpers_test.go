package mcptools

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// The two accounts of the tests. Each has its own connections and can
// reach nothing of the other's.
const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
)

// memoryRows is Rows held in memory, with the same rules the SQL has, and a
// count of how often each question was asked.
type memoryRows struct {
	mu sync.Mutex
	// connections by owning account, then by id.
	connections map[string]map[int64]Connection
	siteHosts   []byte
	lists       int
	reads       int
	fail        error
}

func newRows() *memoryRows {
	return &memoryRows{connections: map[string]map[int64]Connection{}}
}

// owner is an account id as the database compares it: a uuid has one value
// whatever case it was written in.
func owner(userID string) string { return strings.ToLower(userID) }

// add gives an account a connection.
func (m *memoryRows) add(userID string, id int64, name, config string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.connections[userID] == nil {
		m.connections[userID] = map[int64]Connection{}
	}
	m.connections[userID][id] = Connection{ID: id, Name: name, Config: []byte(config)}
}

func (m *memoryRows) remove(userID string, id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.connections[userID], id)
}

func (m *memoryRows) config(userID string, id int64) map[string]json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return keys(m.connections[userID][id].Config)
}

// set replaces one top-level key of a connection's config, as jsonb_set
// does.
func (m *memoryRows) set(userID string, id int64, key string, value []byte) bool {
	row, found := m.connections[userID][id]
	if !found {
		return false
	}
	top := keys(row.Config)
	if top == nil {
		return false
	}
	top[key] = value
	row.Config, _ = json.Marshal(top)
	m.connections[userID][id] = row
	return true
}

func (m *memoryRows) Connections(_ context.Context, userID string) ([]Connection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lists++
	if m.fail != nil {
		return nil, m.fail
	}
	// Oldest first, as the statement's ORDER BY id.
	out := make([]Connection, 0, len(m.connections[owner(userID)]))
	for _, row := range m.connections[owner(userID)] {
		row.SiteHosts = m.siteHosts
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b Connection) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (m *memoryRows) Connection(_ context.Context, userID string, connectorID int64) (*Connection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	if m.fail != nil {
		return nil, m.fail
	}
	row, found := m.connections[owner(userID)][connectorID]
	if !found {
		return nil, nil
	}
	row.SiteHosts = m.siteHosts
	return &row, nil
}

func (m *memoryRows) AddTrusted(_ context.Context, userID string, connectorID int64, toolName string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, found := m.connections[owner(userID)][connectorID]
	if !found {
		return false, nil
	}
	trusted := readTrusted(row.Config)
	for _, name := range trusted {
		if name == toolName {
			return true, nil
		}
	}
	list, _ := json.Marshal(append(trusted, toolName))
	return m.set(owner(userID), connectorID, keyTrusted, list), nil
}

func (m *memoryRows) StoreDefinitions(_ context.Context, userID string, connectorID int64, cached []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.set(owner(userID), connectorID, keyCached, cached)
	return nil
}

func (m *memoryRows) MarkAuthExpired(_ context.Context, userID string, connectorID int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if string(keys(m.connections[owner(userID)][connectorID].Config)["auth_expired"]) == "true" {
		return false, nil
	}
	return m.set(owner(userID), connectorID, "auth_expired", []byte("true")), nil
}

func (m *memoryRows) counts() (lists, reads int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lists, m.reads
}

// counter is Generations in memory. Two Sources given the same counter are
// two processes sharing one Valkey.
type counter struct {
	mu      sync.Mutex
	numbers map[string]int64
	down    bool
	gets    int
}

func newCounter() *counter { return &counter{numbers: map[string]int64{}} }

var errDown = errors.New("valkey: connection refused")

func (c *counter) Current(_ context.Context, userID string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets++
	if c.down {
		return 0, errDown
	}
	return c.numbers[GenerationKey(userID)], nil
}

func (c *counter) Bump(_ context.Context, userID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.down {
		return errDown
	}
	c.numbers[GenerationKey(userID)]++
	return nil
}

func (c *counter) at(userID string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.numbers[GenerationKey(userID)]
}

func (c *counter) setDown(down bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.down = down
}

// loopback is the Network of the tests: it lets a connection go to the
// test's own servers on 127.0.0.1, which PublicInternet exists to refuse.
// It builds its client with the same newClient, so the no-redirect rule
// under test is the real one.
type loopback struct{}

func (loopback) Client(string, []string) (*http.Client, error) {
	return newClient((&net.Dialer{}).DialContext, time.Second), nil
}

// clock is a time the test moves.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// logs collects what was logged, for the tests that check a key never
// reaches the log.
type logs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// world is one process's Source over rows and a counter that other
// "processes" (other worlds made with the same two) share.
type world struct {
	source  *Source
	rows    *memoryRows
	counter *counter
	clock   *clock
	logs    *logs
}

func testLimits() Limits {
	return Limits{
		ToolsPerServer: 200, Discovery: 5 * time.Second, Call: 5 * time.Second,
		Lifetime: 300 * time.Second, RetryIncomplete: 30 * time.Second, Accounts: 50, ResultBytes: 4000,
	}
}

func newWorld(t testing.TB, rows *memoryRows, shared *counter, change func(*Settings)) *world {
	t.Helper()
	w := &world{rows: rows, counter: shared, clock: newClock(), logs: &logs{}}
	settings := Settings{
		Rows: rows, Generations: shared, Network: loopback{},
		Logger: slog.New(slog.NewTextHandler(w.logs, nil)), Now: w.clock.Now,
		ClientName: "chat-under-test", SiteHostsWhenUnset: []string{}, ReservedNames: []string{"search_knowledge_base"},
		Limits: testLimits(),
	}
	if change != nil {
		change(&settings)
	}
	source, err := NewSource(settings)
	if err != nil {
		t.Fatal(err)
	}
	w.source = source
	return w
}

// turn builds a turn's Set for an account and closes it with the
// test.
func (w *world) turn(t testing.TB, userID string) *Set {
	t.Helper()
	set, err := w.source.ForTurn(context.Background(), userID)
	if err != nil {
		t.Fatalf("ForTurn: %v", err)
	}
	t.Cleanup(set.Close)
	return set
}

// names is the model-facing names of a Set's tools, in order.
func names(set *Set) []string {
	var out []string
	for _, tl := range set.Tools() {
		out = append(out, tl.Name)
	}
	return out
}

// call runs the tool the model knows as name.
func call(t testing.TB, set *Set, name, args string) (string, error) {
	t.Helper()
	for _, tl := range set.Tools() {
		if tl.Name == name {
			if err := tl.Validate(); err != nil {
				t.Fatalf("tool %s is not one the loop accepts: %v", name, err)
			}
			return tl.Run(context.Background(), tool.Call{ID: "call-1", Name: name, Args: json.RawMessage(args)})
		}
	}
	t.Fatalf("no tool named %s among %v", name, names(set))
	return "", nil
}

// remote is a real MCP server (the SDK's server side) on loopback that
// records what reached it.
type remote struct {
	*httptest.Server
	mcp *mcp.Server

	mu       sync.Mutex
	headers  []http.Header
	methods  map[string]int
	deletes  int
	requests atomic.Int64
	// status, when set, is answered to every request instead of serving.
	status atomic.Int64
}

// newRemote starts a server whose tools/list hands out pageSize tools a
// page.
func newRemote(t testing.TB, pageSize int) *remote {
	t.Helper()
	r := &remote{methods: map[string]int{}}
	r.mcp = mcp.NewServer(&mcp.Implementation{Name: "remote-under-test", Version: "9.9"}, &mcp.ServerOptions{PageSize: pageSize})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return r.mcp }, nil)
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		body, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
		var message struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &message)
		r.mu.Lock()
		r.headers = append(r.headers, req.Header.Clone())
		if message.Method != "" {
			r.methods[message.Method]++
		}
		if req.Method == http.MethodDelete {
			r.deletes++
		}
		r.mu.Unlock()
		if status := r.status.Load(); status != 0 {
			w.WriteHeader(int(status))
			return
		}
		handler.ServeHTTP(w, req)
	}))
	t.Cleanup(r.Close)
	return r
}

// add gives the server a tool that answers with reply.
func (r *remote) add(name string, reply func(args json.RawMessage) *mcp.CallToolResult) {
	r.mcp.AddTool(&mcp.Tool{
		Name: name, Description: "Does " + name + ".",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
	}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return reply(request.Params.Arguments), nil
	})
}

func says(text string) func(json.RawMessage) *mcp.CallToolResult {
	return func(json.RawMessage) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
	}
}

// handshakes is how many requests were spent opening sessions. One session
// is one: a client that probed for a newer protocol first would count two.
func (r *remote) handshakes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.methods["initialize"] + r.methods["server/discover"]
}

func (r *remote) method(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.methods[name]
}

func (r *remote) closed() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deletes
}

// sent reports whether any request carried the header with that value.
func (r *remote) sent(name, value string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, header := range r.headers {
		if header.Get(name) == value {
			return true
		}
	}
	return false
}

// serverConfig is a connection's config pointing at address.
func serverConfig(address, key string, more ...string) string {
	extra := ""
	for _, piece := range more {
		extra += ", " + piece
	}
	return `{"server_config": {"transport": "streamable-http", "url": "` + address + `", "headers": {"x-api-key": "` + key + `"}}, "server_origin": "member"` + extra + `}`
}

// cachedList is a config.cached_tools value listing the named tools.
func cachedList(toolNames ...string) string {
	var items []string
	for _, name := range toolNames {
		quoted, _ := json.Marshal(name)
		items = append(items, `{"name": `+string(quoted)+`, "description": "Does `+strings.Trim(string(quoted), `"`)+`.", "input_schema": {"type": "object", "properties": {"q": {"type": "string"}}}}`)
	}
	return `"cached_tools": {"discovered_at": "2026-01-02T03:04:05Z", "server_name": "x", "server_version": null, "transport": "streamable-http", "tools": [` + strings.Join(items, ", ") + `]}`
}

// slogDiscard is a logger that keeps nothing, for the benchmarks whose
// subject is not the log.
func slogDiscard() *slog.Logger { return slog.New(slog.DiscardHandler) }
