package agentactions

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_AGENTACTIONS_DATABASE"

func serve(t *testing.T, route Route, userID, path string) (int, string) {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	route.Errors, route.Logger = errorenvelope.Writer{}, slog.New(slog.NewTextHandler(io.Discard, nil))
	route.Register(api)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if userID != "" {
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: principal.MethodSession}))
	}
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// The checks that need no database: what is refused before a row is read.
func TestABadRequestIsRefusedBeforeTheLogIsRead(t *testing.T) {
	// A nil pool: reaching the database would panic, which is the point.
	for _, c := range []struct {
		name, path string
		want       int
		names      string
	}{
		{"a thread id that is not a number", "/api/v1/threads/abc/actions", 422, "thread_id"},
		{"a negative page", "/api/v1/threads/1/actions?page=-1", 422, "page"},
		{"a page size of nothing", "/api/v1/threads/1/actions?page_size=0", 422, "page_size"},
		{"a page size past the limit", "/api/v1/threads/1/actions?page_size=201", 422, "page_size"},
	} {
		if status, body := serve(t, Route{}, testpostgres.Alice, c.path); status != c.want || !strings.Contains(body, c.names) {
			t.Errorf("%s: %d %s", c.name, status, body)
		}
	}
	if status, _ := serve(t, Route{}, "", "/api/v1/threads/1/actions"); status != 500 {
		t.Errorf("a request with no caller answered %d", status)
	}
}

func TestAThreadsActionsAreItsOwnersAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO new_chat_threads (id, user_id, title) VALUES (10, '`+alice+`', 'Hers'), (11, '`+alice+`', 'Empty'), (20, '`+bob+`', 'His');
INSERT INTO agent_action_log (id, thread_id, user_id, tool_name, args, result_id, reversible, reverse_descriptor, error, reverse_of, tool_call_id, chat_turn_id, created_at) VALUES
 (1, 10, '`+alice+`', 'update_memory', '{"text":"a <b>"}', 'doc:5', true, '{"kind":"restore"}', NULL, NULL, 'call_1', 'turn_1', '2026-11-12T08:00:00Z'),
 (2, 10, '`+alice+`', 'update_memory', '[1,2]', NULL, false, 'null', '{"message":"refused"}', NULL, 'call_2', 'turn_1', '2026-11-12T08:01:00Z'),
 (3, 10, '`+alice+`', 'revert', NULL, NULL, false, NULL, NULL, 1, NULL, 'turn_2', '2026-11-12T08:02:00Z'),
 -- A row in her thread recorded for him: never hers to read.
 (4, 10, '`+bob+`', 'update_memory', '{"secret":"his"}', NULL, false, NULL, NULL, NULL, 'call_4', 'turn_9', '2026-11-12T08:03:00Z'),
 (5, 20, '`+bob+`', 'update_memory', '{"secret":"his"}', NULL, true, NULL, NULL, NULL, 'call_5', 'turn_9', '2026-11-12T08:04:00Z');
`)
	expect := func(name string, status int, body string, want int, says ...string) {
		t.Helper()
		if status != want {
			t.Errorf("%s: %d %s", name, status, body)
			return
		}
		for _, piece := range says {
			if !strings.Contains(body, piece) {
				t.Errorf("%s: %s lacks %s", name, body, piece)
			}
		}
	}
	route := Route{Pool: pool}

	status, body := serve(t, route, alice, "/api/v1/threads/10/actions")
	expect("her log", status, body, 200,
		`{"items":[{"id":3,"thread_id":10,"user_id":"`+alice+`","tool_name":"revert","args":null,"result_id":null,"reversible":false,"reverse_descriptor":null,"error":null,"reverse_of":1,"reverted_by_action_id":null,"is_revert_action":true,"tool_call_id":null,"chat_turn_id":"turn_2","created_at":"2026-11-12T08:02:00Z"}`,
		// Arguments that are not an object are null, not a parse failure.
		`{"id":2,"thread_id":10,"user_id":"`+alice+`","tool_name":"update_memory","args":null,"result_id":null,"reversible":false,"reverse_descriptor":null,"error":{"message":"refused"}`,
		// The undone action names the action that undid it.
		`{"id":1,`, `"args":{"text":"a <b>"},"result_id":"doc:5","reversible":true,"reverse_descriptor":{"kind":"restore"},"error":null,"reverse_of":null,"reverted_by_action_id":3,"is_revert_action":false,"tool_call_id":"call_1"`,
		`"total":3,"page":0,"page_size":50,"has_more":false}`)
	if strings.Contains(body, "secret") {
		t.Errorf("her log shows a row recorded for him: %s", body)
	}
	status, body = serve(t, route, alice, "/api/v1/threads/10/actions?page=1&page_size=2")
	expect("her second page", status, body, 200, `{"items":[{"id":1,`, `"total":3,"page":1,"page_size":2,"has_more":false}`)
	status, body = serve(t, route, alice, "/api/v1/threads/10/actions?page=0&page_size=2")
	expect("her first page of two", status, body, 200, `"total":3,"page":0,"page_size":2,"has_more":true}`)
	status, body = serve(t, route, alice, "/api/v1/threads/10/actions?page=7")
	expect("a page past the end", status, body, 200, `{"items":[],"total":3,"page":7,"page_size":50,"has_more":false}`)
	status, body = serve(t, route, alice, "/api/v1/threads/11/actions")
	expect("her thread with no actions", status, body, 200, `{"items":[],"total":0,"page":0,"page_size":50,"has_more":false}`)

	status, body = serve(t, route, bob, "/api/v1/threads/10/actions")
	expect("her log asked by him", status, body, 404, threadAbsent)
	status, body = serve(t, route, alice, "/api/v1/threads/20/actions")
	expect("his log asked by her", status, body, 404, threadAbsent)
	status, body = serve(t, route, alice, "/api/v1/threads/999/actions")
	expect("a thread nobody has", status, body, 404, threadAbsent)
	status, body = serve(t, route, alice, "/api/v1/threads/99999999999999999999999/actions")
	expect("an id wider than any thread's", status, body, 404, threadAbsent)
}
