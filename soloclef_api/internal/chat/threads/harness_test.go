package threads

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	agentpostgres "git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint/postgres"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/turns"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/posthogcapture"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/routeanalytics"
)

// databaseVariable names a throwaway Postgres at this tier's schema (a
// pgvector/pgvector:pg17 brought up to date by soloclef_schema's
// `migrate up`). The tests **truncate the tables they seed**, so it must
// never name a database anyone uses. With it unset every test of the routes
// is skipped, and a skip is not a pass: the routes are only proved by
// running with it set.
//
//	SOLOCLEF_API_TEST_THREADS_DATABASE=postgres://check:check@127.0.0.1:39941/check \
//	  go test ./internal/chat/threads/ -count=1 -race -v
const databaseVariable = "SOLOCLEF_API_TEST_THREADS_DATABASE"

// seed is a small world of three accounts, each with threads of its own
// and no way to anybody else's.
//
// Ada's threads: 1 with three messages, 2 with one, 4 archived, 5 and 10
// empty (10's id begins like 1's), 6 one the artifact API made. Thread 3 is
// Ben's, who has set no name. Thread 90 is Dee's.
const seed = `
TRUNCATE "user", soloclef_agent_checkpoints CASCADE;
INSERT INTO "user" (id, email, display_name, avatar_url) VALUES
  ('aaaaaaaa-0000-4000-8000-000000000001', 'ada@example.org', 'Ada Lovelace', 'https://img.example/ada.png'),
  ('bbbbbbbb-0000-4000-8000-000000000002', 'ben@example.org', NULL, NULL),
  ('dddddddd-0000-4000-8000-000000000004', 'dee@example.org', 'Dee', NULL);
INSERT INTO new_chat_threads (id, title, archived, updated_at, user_id, source, created_at) VALUES
  (1, 'Cell Biology', false, '2026-01-05T00:00:00Z', 'aaaaaaaa-0000-4000-8000-000000000001', 'surfsense', '2026-01-01T10:00:00.5Z'),
  (2, 'Plans 50% done', false, '2026-01-06T00:00:00Z', 'aaaaaaaa-0000-4000-8000-000000000001', 'surfsense', '2026-01-02T00:00:00Z'),
  (3, 'Ben''s cell notes', false, '2026-01-07T00:00:00Z', 'bbbbbbbb-0000-4000-8000-000000000002', 'surfsense', '2026-01-03T00:00:00Z'),
  (4, 'Old cells', true, '2026-01-04T00:00:00Z', 'aaaaaaaa-0000-4000-8000-000000000001', 'surfsense', '2026-01-01T00:00:00Z'),
  (5, 'Left behind', false, '2026-01-03T00:00:00Z', 'aaaaaaaa-0000-4000-8000-000000000001', 'surfsense', '2026-01-01T00:00:00Z'),
  (6, 'Made by the artifact API', false, '2026-01-08T00:00:00Z', 'aaaaaaaa-0000-4000-8000-000000000001', 'artifact_api', '2026-01-01T00:00:00Z'),
  (10, 'Ten', false, '2026-01-02T00:00:00Z', 'aaaaaaaa-0000-4000-8000-000000000001', 'surfsense', '2026-01-01T00:00:00Z'),
  (90, 'Elsewhere cells', false, '2026-01-09T00:00:00Z', 'dddddddd-0000-4000-8000-000000000004', 'surfsense', '2026-01-01T00:00:00Z');
SELECT setval('new_chat_threads_id_seq', 1000);
-- Thread 1's two messages share created_at to the microsecond, as a turn's
-- question and answer do, and the answer has the LOWER id only in order of
-- insertion below: 101 is the question.
INSERT INTO new_chat_messages (id, role, content, thread_id, author_id, turn_id, created_at) VALUES
  (101, 'USER', '[{"type":"text","text":"What is <mitosis> & why?"}]', 1, 'aaaaaaaa-0000-4000-8000-000000000001', '1:1000', '2026-01-01T10:00:01Z'),
  (102, 'ASSISTANT', '[{"type":"reasoning","text":"thinking","id":"r","status":"completed","startedAt":"s"},{"type":"text","text":"Four phases [citation:481]."},{"type":"tool-call","toolCallId":"c0","toolName":"search_knowledge_base","args":{"query":"mitosis"},"result":"passages"}]', 1, NULL, '1:1000', '2026-01-01T10:00:01Z'),
  (103, 'USER', '[{"type":"text","text":"Thanks"}]', 1, 'aaaaaaaa-0000-4000-8000-000000000001', '1:2000', '2026-01-01T10:05:00.25Z'),
  (201, 'USER', '[{"type":"text","text":"Plan?"}]', 2, 'aaaaaaaa-0000-4000-8000-000000000001', '2:1', '2026-01-02T00:00:01Z'),
  (301, 'USER', '[{"type":"text","text":"Ben only"}]', 3, 'bbbbbbbb-0000-4000-8000-000000000002', '3:1', '2026-01-03T00:00:01Z');
SELECT setval('new_chat_messages_id_seq', 1000);
INSERT INTO token_usage (prompt_tokens, completion_tokens, total_tokens, cost_micros, model_breakdown, usage_type, thread_id, message_id, user_id) VALUES
  (120, 30, 150, 420, '{"gpt": {"total_tokens": 150}}', 'chat', 1, 102, 'aaaaaaaa-0000-4000-8000-000000000001');
`

// world is the routes over the seeded database.
type world struct {
	t        testing.TB
	pool     *pgxpool.Pool
	server   *httptest.Server
	service  *Service
	registry *turns.Registry
	// forgetErr, when set, is what Forget answers instead of forgetting.
	forgetErr atomic.Pointer[error]
	events    *captured
	now       time.Time
}

// captured records analytics events.
type captured struct {
	mu     sync.Mutex
	events []posthogcapture.Captured
}

func (c *captured) Capture(event posthogcapture.Captured) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

var testTools = []Tool{
	{Name: "search_knowledge_base", Description: "Search the documents", EnabledByDefault: true},
	{Name: "generate_podcast", Description: "Make a podcast", EnabledByDefault: true},
}

// newWorld seeds the database and serves the routes over it. The tests of
// one world share its rows, so each test function makes its own.
func newWorld(t *testing.T) *world {
	t.Helper()
	return newWorldFor(t)
}

// newWorldFor is newWorld for a test or a benchmark.
func newWorldFor(tb testing.TB) *world {
	tb.Helper()
	address := os.Getenv(databaseVariable)
	if address == "" {
		tb.Skipf("%s is not set: the thread routes were NOT tested", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		tb.Fatalf("opening the database: %v", err)
	}
	// The checkpoint table is the migrations' own; nothing is created here.
	checkpoints, err := agentpostgres.New(pool)
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := pool.Exec(ctx, seed); err != nil {
		tb.Fatalf("seeding: %v", err)
	}
	var owners atomic.Int64
	registry, err := turns.New(turns.Config{
		Store: turns.NewMemory(time.Now), Lifetime: time.Minute, Heartbeat: 10 * time.Millisecond,
		NewOwner: func() string { return "owner-" + strconv.FormatInt(owners.Add(1), 10) },
	})
	if err != nil {
		tb.Fatal(err)
	}
	lifetime, shutdown := context.WithCancel(ctx)
	go func() { _ = registry.Listen(lifetime) }()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := &world{t: tb, pool: pool, registry: registry, events: &captured{}, now: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}
	w.service = &Service{
		Store: Store{Pool: pool}, Turns: registry,
		// The production wiring: the engine's saved conversation for a
		// thread is the checkpoint tree named "chat-<id>".
		Forget: func(ctx context.Context, threadID int64) error {
			if failure := w.forgetErr.Load(); failure != nil {
				return *failure
			}
			return checkpoints.DeleteTree(ctx, "chat-"+itoa(threadID))
		},
		Tools: testTools, FrontendURL: "https://notes.example/",
		Events:   routeanalytics.Capture{Sink: w.events},
		StopWait: 2 * time.Second, Logger: logger,
		Now: func() time.Time { return w.now },
	}
	if err := w.service.Check(); err != nil {
		tb.Fatal(err)
	}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	Routes{Service: w.service, Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"}}.Register(api)
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		// The caller is whoever the tier's gate resolved; here, a header.
		// Whether an API key may act for an account at all is that gate's
		// question (internal/principal) and is tested there.
		if caller := request.Header.Get("X-Test-Caller"); caller != "" {
			request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: caller, Method: principal.MethodSession}))
		}
		mux.ServeHTTP(response, request)
	})
	// The body reader covers exactly the routes the package says read one,
	// as cmd/api wires it.
	bodies := requestbody.Middleware{
		Covers: requestbody.CoveringRoutes(BodyRoutes()...),
	}
	w.server = httptest.NewServer(requestcontext.Middleware(logger, bodies.Wrap(withCaller)))
	tb.Cleanup(func() {
		shutdown()
		w.server.Close()
		pool.Close()
	})
	return w
}

// answered is one answer.
type answered struct {
	status int
	header http.Header
	raw    string
	body   map[string]any
	list   []any
}

// call sends one request as caller.
func (w *world) call(caller, method, path, body string) answered {
	w.t.Helper()
	return w.send(caller, method, path, body, nil)
}

func (w *world) send(caller, method, path, body string, header map[string]string) answered {
	w.t.Helper()
	var sent io.Reader
	if body != "" {
		sent = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, w.server.URL+path, sent)
	if err != nil {
		w.t.Fatal(err)
	}
	request.Header.Set("X-Request-ID", "req_test")
	if caller != "" {
		request.Header.Set("X-Test-Caller", caller)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range header {
		request.Header.Set(name, value)
	}
	response, err := w.server.Client().Do(request)
	if err != nil {
		w.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	got := answered{status: response.StatusCode, header: response.Header, raw: string(raw)}
	_ = json.Unmarshal(raw, &got.body)
	_ = json.Unmarshal(raw, &got.list)
	return got
}

// message is the refusal's sentence, wherever the envelope put it.
func (a answered) message() string {
	failure, _ := a.body["error"].(map[string]any)
	text, _ := failure["message"].(string)
	return text
}

// refusedWith checks a refusal's status and sentence.
func (w *world) refusedWith(what string, got answered, status int, message string) {
	w.t.Helper()
	if got.status != status || got.message() != message {
		w.t.Errorf("%s: %d %q, want %d %q (%s)", what, got.status, got.message(), status, message, got.raw)
	}
}

// exec runs SQL the test needs.
func (w *world) exec(sql string, args ...any) {
	w.t.Helper()
	if _, err := w.pool.Exec(context.Background(), sql, args...); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
}

// count answers a query that returns one number.
func (w *world) count(sql string, args ...any) int64 {
	w.t.Helper()
	var n int64
	if err := w.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// text answers a query that returns one string.
func (w *world) text(sql string, args ...any) string {
	w.t.Helper()
	var s string
	if err := w.pool.QueryRow(context.Background(), sql, args...).Scan(&s); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return s
}

// ids is the "id" of each entry of a list, as a string like "3 2 1".
func ids(list any) string {
	entries, _ := list.([]any)
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		object, _ := entry.(map[string]any)
		id, _ := object["id"].(float64)
		out = append(out, strconv.FormatInt(int64(id), 10))
	}
	return strings.Join(out, " ")
}

var errForget = errors.New("the checkpoint store is away")

// plain is value as JSON with nothing escaped that JSON does not need
// escaped, so an expectation can be written as it reads.
func plain(t testing.TB, value any) string {
	t.Helper()
	raw, err := encode(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
