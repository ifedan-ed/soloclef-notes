package searchconnectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/posthogcapture"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/routeanalytics"
)

// These tests drive the write routes over an in-memory estate, to hold the
// order of a removal's steps and when the MCP counter moves. Whether the
// statements themselves keep one account out of another's rows is asked of
// a real database in isolation_test.go; the estate here applies the same
// rule (owner in every lookup) so the routes can be told apart from the SQL.

// bumps records which accounts' MCP counters were moved.
type bumps struct {
	mu       sync.Mutex
	accounts []string
	// down makes Bump fail, as Valkey being unreachable does.
	down bool
	// before runs at each Bump, so a test can see what had happened by then.
	before func()
}

func (b *bumps) Bump(_ context.Context, userID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.before != nil {
		b.before()
	}
	if b.down {
		return errors.New("valkey did not answer")
	}
	b.accounts = append(b.accounts, userID)
	return nil
}

const (
	owner    = "11111111-1111-4111-8111-111111111111" // owns every connection of the estate
	stranger = "22222222-2222-4222-8222-222222222222" // another account, who owns none
)

// estate is the rows the routes act on: connections, and the documents that
// came through them.
type estate struct {
	mu          sync.Mutex
	connections map[int64]*held
	// documents is connection id -> document id -> state.
	documents map[int64]map[int64]string
	// order is what happened, in order.
	order []string
	// failOn makes the removal of that document fail.
	failOn int64
}

type held struct {
	connection Connection
	config     string
}

func newEstate() *estate {
	e := &estate{connections: map[int64]*held{}, documents: map[int64]map[int64]string{}}
	add := func(id int64, name, kind, config string) {
		e.connections[id] = &held{connection: Connection{ID: id, Name: name, Type: kind, UserID: owner, Enabled: true}, config: config}
	}
	add(810, "Tools", serverTypeTag, `{"server_config": {"url": "https://mcp.example/mcp"}, "trusted_tools": ["search"]}`)
	add(800, "Drive", "GOOGLE_DRIVE_CONNECTOR", `{"refresh_token": "fake-refresh"}`)
	e.documents[800] = map[int64]string{41: "ready", 42: "processing", 43: "failed"}
	return e
}

// row is the estate's one lookup, and it takes the owner: a connection of
// another account is not found, as it is not by the statements.
func (e *estate) row(ownerID string, id int64) *held {
	row, found := e.connections[id]
	if !found || row.connection.UserID != ownerID {
		return nil
	}
	return row
}

func (e *estate) Connection(_ context.Context, ownerID string, id int64) (*Connection, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	row := e.row(ownerID, id)
	if row == nil {
		return nil, nil
	}
	connection := row.connection
	config, err := readColumn([]byte(row.config))
	if err != nil {
		return nil, err
	}
	connection.Config = config
	return &connection, nil
}

func (e *estate) SetEnabled(ctx context.Context, ownerID string, id int64, enabled bool) (*Connection, error) {
	e.mu.Lock()
	if row := e.row(ownerID, id); row != nil {
		row.connection.Enabled = enabled
		e.order = append(e.order, "committed")
	}
	e.mu.Unlock()
	return e.Connection(ctx, ownerID, id)
}

func (e *estate) DeleteServer(_ context.Context, ownerID string, id int64) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	row := e.row(ownerID, id)
	if row == nil || !row.connection.IsServer() {
		return false, nil
	}
	delete(e.connections, id)
	e.order = append(e.order, "committed")
	return true, nil
}

func (e *estate) EditTrustedTools(_ context.Context, ownerID string, id int64,
	edit func([]string) ([]string, bool)) ([]string, bool, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	row := e.row(ownerID, id)
	if row == nil || !row.connection.IsServer() {
		return nil, false, false, nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal([]byte(row.config), &config); err != nil {
		return nil, false, false, err
	}
	next, changed := edit(storedToolNames(config["trusted_tools"]))
	if !changed {
		return next, false, true, nil
	}
	config["trusted_tools"], _ = json.Marshal(next)
	written, _ := json.Marshal(config)
	row.config = string(written)
	e.order = append(e.order, "committed")
	return next, true, true, nil
}

func (e *estate) MarkDocumentsDeleting(_ context.Context, _ string, id int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for document := range e.documents[id] {
		e.documents[id][document] = "deleting"
	}
	e.order = append(e.order, "marked")
	return nil
}

func (e *estate) DocumentIDs(_ context.Context, _ string, id int64, batch int) ([]int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := []int64{}
	for document := range e.documents[id] {
		ids = append(ids, document)
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	return ids[:min(batch, len(ids))], nil
}

func (e *estate) DeleteConnection(_ context.Context, ownerID string, id int64) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	found := e.row(ownerID, id) != nil
	if found {
		delete(e.connections, id)
	}
	e.order = append(e.order, "row deleted")
	return found, nil
}

// Remove is the document remover: it refuses a document that was not marked
// first, which is the order the route owes.
func (e *estate) Remove(_ context.Context, documentID int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if documentID == e.failOn {
		return errors.New("the vector store is unreachable")
	}
	for _, documents := range e.documents {
		if state, found := documents[documentID]; found {
			if state != "deleting" {
				return errors.New("a document was removed before it was taken out of search")
			}
			delete(documents, documentID)
		}
	}
	e.order = append(e.order, "document removed")
	return nil
}

func (e *estate) Revoke(_ context.Context, _ int64, kind string, config []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.order = append(e.order, "revoked "+kind+" "+string(config))
}

type events struct{ captured []posthogcapture.Captured }

func (s *events) Capture(event posthogcapture.Captured) { s.captured = append(s.captured, event) }

// writes is the write routes mounted over an estate, with a caller.
type writes struct {
	t      *testing.T
	estate *estate
	tools  *bumps
	events *events
	server *httptest.Server
	as     principal.Principal
}

func newWrites(t *testing.T) *writes {
	t.Helper()
	w := &writes{t: t, estate: newEstate(), tools: &bumps{}, events: &events{},
		as: principal.Principal{UserID: owner, Method: principal.MethodSession}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	e := w.estate
	ConnectionDeleteRoute{Connections: e, Removals: e, Documents: e, Revocation: e, Tools: w.tools,
		Analytics: routeanalytics.Capture{Sink: w.events}, Errors: envelope, Logger: logger}.Register(api)
	EnabledRoute{Changes: e, Tools: w.tools, Errors: envelope, Logger: logger}.Register(api)
	ServerDeleteRoute{Changes: e, Tools: w.tools, Errors: envelope, Logger: logger}.Register(api)
	TrustRoute{Changes: e, Tools: w.tools, Errors: envelope, Logger: logger}.Register(api)
	TrustRoute{Remove: true, Changes: e, Tools: w.tools, Errors: envelope, Logger: logger}.Register(api)
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		api.Adapter().ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(), w.as)))
	})
	bodies := requestbody.Middleware{
		Covers: requestbody.CoveringRoutes("POST "+TrustPath, "POST "+UntrustPath, "PUT "+EnabledPath),
	}
	w.server = httptest.NewServer(requestcontext.Middleware(logger, bodies.Wrap(withCaller)))
	t.Cleanup(w.server.Close)
	return w
}

func (w *writes) call(method, path, body string) (int, string) {
	w.t.Helper()
	request, err := http.NewRequest(method, w.server.URL+path, strings.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		w.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		w.t.Fatal(err)
	}
	return response.StatusCode, string(raw)
}

func TestRemovingAConnectionRemovesItsDocumentsThenItsCredentialThenItsRow(t *testing.T) {
	w := newWrites(t)
	status, raw := w.call(http.MethodDelete, "/api/v1/search-source-connectors/800", "")
	if status != http.StatusOK {
		t.Fatalf("answered %d %s", status, raw)
	}
	var answer connectionDeleted
	if err := json.Unmarshal([]byte(raw), &answer); err != nil {
		t.Fatal(err)
	}
	want := connectionDeleted{Message: "Connector 'Drive' deleted. 3 documents removed.", Status: "completed",
		ConnectorID: 800, ConnectorName: "Drive", DocumentsDeleted: 3}
	if answer != want {
		t.Errorf("answered %+v, want %+v", answer, want)
	}
	// Marked first, so nothing is searchable while it is being removed; the
	// credential ended only once every document is gone; the row last.
	order := w.estate.order
	if len(order) != 6 || order[0] != "marked" || order[1] != "document removed" || order[3] != "document removed" ||
		order[4] != `revoked GOOGLE_DRIVE_CONNECTOR {"refresh_token":"fake-refresh"}` || order[5] != "row deleted" {
		t.Errorf("order %q", order)
	}
	if len(w.estate.documents[800]) != 0 || w.estate.connections[800] != nil {
		t.Errorf("left: %v, %v", w.estate.documents[800], w.estate.connections[800])
	}
	if len(w.events.captured) != 1 || w.events.captured[0].Name != DeletedEvent {
		t.Errorf("events %+v", w.events.captured)
	}
	// A Drive offers the chat no MCP tools, so no counter moves.
	if len(w.tools.accounts) != 0 {
		t.Errorf("the MCP counter was moved for %v", w.tools.accounts)
	}
}

func TestAConnectionRemovalThatStopsPartwayIsFinishedByDeletingAgain(t *testing.T) {
	w := newWrites(t)
	w.estate.failOn = 42
	status, raw := w.call(http.MethodDelete, "/api/v1/search-source-connectors/800", "")
	if status != http.StatusServiceUnavailable || !strings.Contains(raw, removalUnfinished) {
		t.Fatalf("answered %d %s", status, raw)
	}
	if w.estate.connections[800] == nil || w.estate.documents[800][42] != "deleting" || len(w.events.captured) != 0 {
		t.Fatalf("after the failure: connection %v, documents %v, events %d", w.estate.connections[800], w.estate.documents[800], len(w.events.captured))
	}
	for _, step := range w.estate.order {
		if strings.HasPrefix(step, "revoked") || step == "row deleted" {
			t.Errorf("a removal that had not finished went on to %q", step)
		}
	}

	w.estate.failOn = 0
	status, raw = w.call(http.MethodDelete, "/api/v1/search-source-connectors/800", "")
	if status != http.StatusOK || w.estate.connections[800] != nil || len(w.estate.documents[800]) != 0 {
		t.Errorf("the second delete answered %d %s", status, raw)
	}
}

// Another account's connection is answered exactly as one that does not
// exist, on every write, and nothing at all happens: no document marked, no
// credential ended, no counter moved, no event recorded.
func TestAnotherAccountsConnectionIsNotFoundOnEveryWrite(t *testing.T) {
	for _, test := range []struct {
		name               string
		as                 string
		method, path, body string
		status             int
		says               string
	}{
		{"remove a connection", stranger, http.MethodDelete, "/api/v1/search-source-connectors/800", "", 404, connectorAbsent},
		{"remove a server by the connection route", stranger, http.MethodDelete, "/api/v1/search-source-connectors/810", "", 404, connectorAbsent},
		{"remove a server", stranger, http.MethodDelete, "/api/v1/connectors/mcp/810", "", 404, serverAbsent},
		{"switch a connection off", stranger, http.MethodPut, "/api/v1/search-source-connectors/800/enabled", `{"enabled": false}`, 404, connectorAbsent},
		{"trust a tool", stranger, http.MethodPost, "/api/v1/connectors/mcp/810/trust-tool", `{"tool_name": "summarise"}`, 404, serverAbsent},
		{"untrust a tool", stranger, http.MethodPost, "/api/v1/connectors/mcp/810/untrust-tool", `{"tool_name": "search"}`, 404, serverAbsent},
		// The same answers for ids nobody has, so the two cannot be told apart.
		{"remove a connection nobody has", owner, http.MethodDelete, "/api/v1/search-source-connectors/999", "", 404, connectorAbsent},
		{"an id no connection can have", owner, http.MethodDelete, "/api/v1/search-source-connectors/99999999999999999999", "", 404, connectorAbsent},
		{"a server id no connection can have", owner, http.MethodDelete, "/api/v1/connectors/mcp/99999999999999999999", "", 404, serverAbsent},
		{"the MCP routes on a connection that is not a server", owner, http.MethodDelete, "/api/v1/connectors/mcp/800", "", 404, serverAbsent},
		{"an id that is not a number", owner, http.MethodDelete, "/api/v1/search-source-connectors/abc", "", 422, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := newWrites(t)
			w.as.UserID = test.as
			status, raw := w.call(test.method, test.path, test.body)
			if status != test.status || !strings.Contains(raw, test.says) {
				t.Fatalf("answered %d %s", status, raw)
			}
			if len(w.estate.connections) != 2 || len(w.estate.documents[800]) != 3 || len(w.estate.order) != 0 ||
				len(w.tools.accounts) != 0 || len(w.events.captured) != 0 ||
				!w.estate.connections[800].connection.Enabled ||
				!strings.Contains(w.estate.connections[810].config, `["search"]`) {
				t.Errorf("a refused write left order %q, bumps %v, %d events", w.estate.order, w.tools.accounts, len(w.events.captured))
			}
		})
	}
}

// Every change to which MCP tools the chat may offer moves the owner's
// counter, once, and only after the change is stored: a process that rebuilt
// its tool list from a row about to change would cache the old answer under
// the new count.
func TestEveryChangeToAnAccountsMCPServersMovesItsCounterAfterTheCommit(t *testing.T) {
	for _, test := range []struct {
		name               string
		method, path, body string
		status             int
	}{
		{"a server removed by the MCP route", http.MethodDelete, "/api/v1/connectors/mcp/810", "", 204},
		{"a server removed by the connection route", http.MethodDelete, "/api/v1/search-source-connectors/810", "", 200},
		{"a server switched off", http.MethodPut, "/api/v1/search-source-connectors/810/enabled", `{"enabled": false}`, 200},
		{"a tool trusted", http.MethodPost, "/api/v1/connectors/mcp/810/trust-tool", `{"tool_name": "summarise"}`, 200},
		{"a tool untrusted", http.MethodPost, "/api/v1/connectors/mcp/810/untrust-tool", `{"tool_name": "search"}`, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := newWrites(t)
			var committedFirst bool
			w.tools.before = func() {
				w.estate.mu.Lock()
				defer w.estate.mu.Unlock()
				last := ""
				if len(w.estate.order) > 0 {
					last = w.estate.order[len(w.estate.order)-1]
				}
				committedFirst = last == "committed" || last == "row deleted"
			}
			status, raw := w.call(test.method, test.path, test.body)
			if status != test.status {
				t.Fatalf("answered %d %s", status, raw)
			}
			if !reflect.DeepEqual(w.tools.accounts, []string{owner}) {
				t.Errorf("the counter was moved for %v, want the owner's once", w.tools.accounts)
			}
			if !committedFirst {
				t.Errorf("the counter moved before the change was committed: %q", w.estate.order)
			}
		})
	}
}

// Switching a connection that is not an MCP server changes no tool, so it
// moves no counter; and the answer is the connection as it then stands.
func TestTheSwitchAnswersTheConnectionAndLeavesOtherTypesCountersAlone(t *testing.T) {
	w := newWrites(t)
	status, raw := w.call(http.MethodPut, "/api/v1/search-source-connectors/800/enabled", `{"enabled": false}`)
	if status != http.StatusOK || !strings.Contains(raw, `"id":800`) || !strings.Contains(raw, `"enabled":false`) {
		t.Fatalf("answered %d %s", status, raw)
	}
	if strings.Contains(raw, "fake-refresh") {
		t.Errorf("the switch answered a stored credential: %s", raw)
	}
	if w.estate.connections[800].connection.Enabled || len(w.tools.accounts) != 0 {
		t.Errorf("enabled %v, bumps %v", w.estate.connections[800].connection.Enabled, w.tools.accounts)
	}
	// Its documents are kept: off is not removal.
	if len(w.estate.documents[800]) != 3 {
		t.Errorf("switching off touched the documents: %v", w.estate.documents[800])
	}
	for body, want := range map[string]int{
		``:                             http.StatusUnprocessableEntity,
		`{}`:                           http.StatusUnprocessableEntity,
		`{"enabled": null}`:            http.StatusUnprocessableEntity,
		`{"enabled": true, "name": 1}`: http.StatusUnprocessableEntity,
		`{"enabled": true}`:            http.StatusOK,
	} {
		if status, raw := w.call(http.MethodPut, "/api/v1/search-source-connectors/800/enabled", body); status != want {
			t.Errorf("body %q answered %d %s, want %d", body, status, raw, want)
		}
	}
}

func TestATrustEditThatChangesNothingMovesNoCounter(t *testing.T) {
	for path, body := range map[string]string{
		"/api/v1/connectors/mcp/810/trust-tool":   `{"tool_name": "search"}`,    // already trusted
		"/api/v1/connectors/mcp/810/untrust-tool": `{"tool_name": "summarise"}`, // never trusted
	} {
		w := newWrites(t)
		status, raw := w.call(http.MethodPost, path, body)
		if status != http.StatusOK || !strings.Contains(raw, `"trusted_tools":["search"]`) {
			t.Fatalf("%s answered %d %s", path, status, raw)
		}
		if len(w.tools.accounts) != 0 || len(w.estate.order) != 0 {
			t.Errorf("%s wrote %q and moved the counter for %v", path, w.estate.order, w.tools.accounts)
		}
	}
}

// "Run this without asking me" is the person's decision: a key or an agent
// acting for the account may not make it, and is told so before any row is
// read. A tool that opens web addresses can never be trusted.
func TestWhoMayTrustATool(t *testing.T) {
	w := newWrites(t)
	if status, raw := w.call(http.MethodPost, "/api/v1/connectors/mcp/810/trust-tool", `{"tool_name": "summarise"}`); status != 200 || !strings.Contains(raw, `"trusted_tools":["search","summarise"]`) {
		t.Fatalf("answered %d %s", status, raw)
	}
	if !strings.Contains(w.estate.connections[810].config, `"trusted_tools":["search","summarise"]`) ||
		!strings.Contains(w.estate.connections[810].config, "https://mcp.example/mcp") {
		t.Errorf("the row holds %s", w.estate.connections[810].config)
	}
	if status, raw := w.call(http.MethodPost, "/api/v1/connectors/mcp/810/trust-tool", `{"tool_name": "web_fetch"}`); status != http.StatusBadRequest || !strings.Contains(raw, toolAlwaysAsk) {
		t.Errorf("a tool that opens addresses answered %d %s", status, raw)
	}
	for _, method := range []principal.Method{principal.MethodPersonalAccessToken, principal.MethodAgent} {
		w.as.Method = method
		for _, path := range []string{"/api/v1/connectors/mcp/810/trust-tool", "/api/v1/connectors/mcp/810/untrust-tool"} {
			if status, raw := w.call(http.MethodPost, path, `{"tool_name": "search"}`); status != http.StatusForbidden || !strings.Contains(raw, sessionRequired) {
				t.Errorf("%s by %s answered %d %s", path, method, status, raw)
			}
		}
	}
}

func TestACounterThatCannotBeMovedDoesNotFailTheChange(t *testing.T) {
	w := newWrites(t)
	w.tools.down = true
	if status, raw := w.call(http.MethodDelete, "/api/v1/connectors/mcp/810", ""); status != http.StatusNoContent {
		t.Fatalf("answered %d %s", status, raw)
	}
	if w.estate.connections[810] != nil {
		t.Error("the server is still there")
	}
}

// The decision of one edit, on its own.
func TestEditTrusted(t *testing.T) {
	for _, test := range []struct {
		held    []string
		tool    string
		remove  bool
		want    []string
		changed bool
	}{
		{[]string{"a"}, "b", false, []string{"a", "b"}, true},
		{[]string{"a"}, "a", false, []string{"a"}, false},
		{[]string{"a", "b", "a"}, "a", true, []string{"b"}, true},
		{[]string{"a"}, "b", true, []string{"a"}, false},
		{[]string{}, "a", true, []string{}, false},
	} {
		got, changed := editTrusted(test.held, test.tool, test.remove)
		if !reflect.DeepEqual(got, test.want) || changed != test.changed {
			t.Errorf("editTrusted(%v, %q, %v) = %v, %v", test.held, test.tool, test.remove, got, changed)
		}
	}
	// A stored list that is not a list, or holds what is not a name, reads
	// as the names it does hold.
	for raw, want := range map[string][]string{
		`null`: {}, `"text"`: {}, `{"a": 1}`: {}, `["a", 7, null, "b"]`: {"a", "b"},
	} {
		if got := storedToolNames([]byte(raw)); !reflect.DeepEqual(got, want) {
			t.Errorf("storedToolNames(%s) = %v, want %v", raw, got, want)
		}
	}
}
