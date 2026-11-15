package agentrules

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailtools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres; the test empties it.
//
//	SOLOCLEF_API_TEST_AGENTRULES_DATABASE=postgres://check:check@127.0.0.1:39951/check \
//	  go test ./internal/agentrules/ -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_AGENTRULES_DATABASE"

const (
	alice = testpostgres.Alice
	bob   = testpostgres.Bob
)

// Alice has a connected app (30) that last listed two tools, and one whose
// saved list is broken (31); Bob has an app of his own (40) with a tool of
// the same name as one of Alice's.
const seed = `
INSERT INTO search_source_connectors (id, user_id, name, connector_type, config) VALUES
 (30, '` + alice + `', 'Team wiki', 'MCP_CONNECTOR', '{"cached_tools": {"tools": [{"name": "search_pages"}, {"name": "edit_page"}, {"name": "search_pages"}, {"description": "nameless"}]}}'),
 (31, '` + alice + `', 'Broken', 'MCP_CONNECTOR', '{"cached_tools": {"tools": "not a list"}}'),
 (40, '` + bob + `', 'His wiki', 'MCP_CONNECTOR', '{"cached_tools": {"tools": [{"name": "edit_page"}]}}'),
 (41, '` + alice + `', 'Drive', 'GOOGLE_DRIVE_CONNECTOR', '{"cached_tools": {"tools": [{"name": "edit_page"}]}}');
`

type world struct {
	t      *testing.T
	pool   *pgxpool.Pool
	server *httptest.Server
	method principal.Method
}

func newWorld(t *testing.T) *world {
	t.Helper()
	pool := testpostgres.Open(t, databaseVariable)
	testpostgres.Exec(t, pool, seed)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	Routes{Marks: Store{Pool: pool}, Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"}, Logger: logger}.Register(api)
	w := &world{t: t, pool: pool, method: principal.MethodSession}
	// The caller is whoever the tier's gate resolved; here, a header.
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if caller := request.Header.Get("X-Test-Caller"); caller != "" {
			request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: caller, Method: w.method}))
		}
		mux.ServeHTTP(response, request)
	})
	bodies := requestbody.Middleware{Covers: requestbody.CoveringRoutes(BodyRoutes()...)}
	w.server = httptest.NewServer(requestcontext.Middleware(logger, bodies.Wrap(withCaller)))
	t.Cleanup(w.server.Close)
	return w
}

func (w *world) call(caller, method, path, body string) (int, string) {
	w.t.Helper()
	request, err := http.NewRequest(method, w.server.URL+path, strings.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if caller != "" {
		request.Header.Set("X-Test-Caller", caller)
	}
	response, err := w.server.Client().Do(request)
	if err != nil {
		w.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(raw)
}

// list is the caller's tools, by "group/tool" (and the connection for an
// app's), and whether each asks first.
func (w *world) list(caller string) map[string]bool {
	w.t.Helper()
	status, raw := w.call(caller, http.MethodGet, ToolsPath, "")
	if status != http.StatusOK {
		w.t.Fatalf("the list answered %d %s", status, raw)
	}
	var tools []Tool
	if err := json.Unmarshal([]byte(raw), &tools); err != nil {
		w.t.Fatalf("the list: %v in %s", err, raw)
	}
	out := make(map[string]bool, len(tools))
	for _, listed := range tools {
		key := listed.Group + "/" + listed.Name
		if listed.ConnectorID != nil {
			key += "@" + strconv.FormatInt(*listed.ConnectorID, 10)
		}
		out[key] = listed.AskFirst
	}
	return out
}

func (w *world) marks() int {
	w.t.Helper()
	var n int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM agent_ask_first_tools`).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// Every tool is listed unmarked at first: the chat's own from the
// catalogue, and each of the caller's apps' tools once, under the app's
// name; never another account's, and nothing from a broken list or a
// connection that is not an app.
func TestTheListIsEveryToolAndNothingIsMarkedAtFirst(t *testing.T) {
	w := newWorld(t)
	listed := w.list(alice)
	if len(listed) != len(catalogue)+2 {
		t.Errorf("%d tools listed, want the catalogue's %d and the wiki's 2: %v", len(listed), len(catalogue), listed)
	}
	for key, asks := range listed {
		if asks {
			t.Errorf("%s asks first before anyone marked it", key)
		}
	}
	for _, key := range []string{"Mail/" + mailtools.SearchTool, "The web/" + pagereader.ToolName, "Team wiki/search_pages@30", "Team wiki/edit_page@30"} {
		if _, found := listed[key]; !found {
			t.Errorf("%s is not listed", key)
		}
	}
	if _, found := listed["His wiki/edit_page@40"]; found {
		t.Error("Bob's app is in Alice's list")
	}
}

// Marking and unmarking, of the chat's own tools and an app's, each by its
// own key, and only for the caller.
func TestMarkingATool(t *testing.T) {
	w := newWorld(t)
	put := func(caller, body string) (int, string) {
		return w.call(caller, http.MethodPut, AskFirstPath, body)
	}
	for _, body := range []string{
		`{"tool": "` + mailtools.ReadTool + `", "ask_first": true}`,
		`{"tool": "` + mailtools.ReadTool + `", "connector_id": null, "ask_first": true}`, // twice is once
		`{"tool": "edit_page", "connector_id": 30, "ask_first": true}`,
	} {
		if status, raw := put(alice, body); status != http.StatusNoContent || raw != "" {
			t.Fatalf("%s answered %d %s", body, status, raw)
		}
	}
	listed := w.list(alice)
	if !listed["Mail/"+mailtools.ReadTool] || !listed["Team wiki/edit_page@30"] || listed["Team wiki/search_pages@30"] || w.marks() != 2 {
		t.Errorf("after marking: %v, %d marks", listed, w.marks())
	}
	// Bob's list and his app's tool of the same name are his own.
	if bobs := w.list(bob); bobs["Mail/"+mailtools.ReadTool] || bobs["His wiki/edit_page@40"] {
		t.Errorf("Alice's marks reached Bob: %v", bobs)
	}

	// Unmarking takes the one mark off, and is never refused.
	for _, body := range []string{
		`{"tool": "` + mailtools.ReadTool + `", "ask_first": false}`,
		`{"tool": "` + mailtools.ReadTool + `", "ask_first": false}`,
		`{"tool": "a_tool_no_server_lists", "connector_id": 30, "ask_first": false}`,
	} {
		if status, raw := put(alice, body); status != http.StatusNoContent {
			t.Fatalf("%s answered %d %s", body, status, raw)
		}
	}
	if listed := w.list(alice); listed["Mail/"+mailtools.ReadTool] || !listed["Team wiki/edit_page@30"] {
		t.Errorf("after unmarking: %v", listed)
	}

	// The app removed: its marks go with it.
	testpostgres.Exec(t, w.pool, `DELETE FROM search_source_connectors WHERE id = 30`)
	if w.marks() != 0 {
		t.Errorf("%d marks left after the app was removed", w.marks())
	}
}

// A mark can only be set on a tool the list shows, so no mark can look like
// a safeguard and do nothing; and whatever is refused writes nothing.
func TestWhatCannotBeMarked(t *testing.T) {
	w := newWorld(t)
	for name, body := range map[string]string{
		"a tool the chat does not have":        `{"tool": "send_rocket", "ask_first": true}`,
		"the memory tool, left to its page":    `{"tool": "update_memory", "ask_first": true}`,
		"a tool the app did not list":          `{"tool": "delete_everything", "connector_id": 30, "ask_first": true}`,
		"another account's app":                `{"tool": "edit_page", "connector_id": 40, "ask_first": true}`,
		"a connection that is not an app":      `{"tool": "edit_page", "connector_id": 41, "ask_first": true}`,
		"an app whose list is broken":          `{"tool": "edit_page", "connector_id": 31, "ask_first": true}`,
		"an app nobody has":                    `{"tool": "edit_page", "connector_id": 999, "ask_first": true}`,
		"a connection id no connection has":    `{"tool": "edit_page", "connector_id": 0, "ask_first": true}`,
		"a connection id past the column":      `{"tool": "edit_page", "connector_id": 99999999999999999999, "ask_first": true}`,
		"a name no text column holds":          `{"tool": "edit\u0000page", "connector_id": 30, "ask_first": true}`,
		"a chat tool named under an app by id": `{"tool": "` + mailtools.ReadTool + `", "connector_id": 30, "ask_first": true}`,
	} {
		if status, raw := w.call(alice, http.MethodPut, AskFirstPath, body); status != http.StatusNotFound || !strings.Contains(raw, toolAbsent) {
			t.Errorf("%s: %d %s", name, status, raw)
		}
	}
	for name, body := range map[string]string{
		"no tool":          `{"ask_first": true}`,
		"no choice":        `{"tool": "` + mailtools.ReadTool + `"}`,
		"an empty name":    `{"tool": "", "ask_first": true}`,
		"a name too long":  `{"tool": "` + strings.Repeat("x", maxToolName+1) + `", "ask_first": true}`,
		"not an object":    `["` + mailtools.ReadTool + `"]`,
		"a choice of text": `{"tool": "` + mailtools.ReadTool + `", "ask_first": "perhaps"}`,
	} {
		if status, raw := w.call(alice, http.MethodPut, AskFirstPath, body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, status, raw)
		}
	}
	if w.marks() != 0 {
		t.Errorf("refused requests left %d marks", w.marks())
	}
}

// Which tools run unasked is the person's decision: a key or an agent acting
// for the account may read the list and may not change it. With no caller
// the wiring is wrong, which is a 500 and never a list.
func TestOnlyThePersonChangesAMark(t *testing.T) {
	w := newWorld(t)
	testpostgres.Exec(t, w.pool, `INSERT INTO agent_ask_first_tools (user_id, tool) VALUES ('`+alice+`', '`+mailtools.ReadTool+`')`)
	for _, method := range []principal.Method{principal.MethodPersonalAccessToken} {
		w.method = method
		if status, raw := w.call(alice, http.MethodPut, AskFirstPath, `{"tool": "`+mailtools.ReadTool+`", "ask_first": false}`); status != http.StatusForbidden || !strings.Contains(raw, sessionRequired) {
			t.Errorf("%s: %d %s", method, status, raw)
		}
		if !w.list(alice)["Mail/"+mailtools.ReadTool] {
			t.Errorf("%s took a mark off", method)
		}
	}
	if status, raw := w.call("", http.MethodGet, ToolsPath, ""); status != http.StatusInternalServerError || strings.Contains(raw, mailtools.ReadTool) {
		t.Errorf("no caller: %d %s", status, raw)
	}
}
