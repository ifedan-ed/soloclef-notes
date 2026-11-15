package zerosync

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

const caller = "11111111-2222-3333-4444-555555555555"

// ask posts body as zero-cache would, for a caller the gate resolved
// (userID empty: the gate was left out).
func ask(t *testing.T, userID, body string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(body))
	if userID != "" {
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: principal.MethodSession}))
	}
	recorder := httptest.NewRecorder()
	Handler{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.ServeHTTP(recorder, request)
	var answer map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
		t.Fatalf("the answer is not JSON: %v %s", err, recorder.Body)
	}
	return recorder.Code, answer
}

// Every query's AST is the one Zero's own TypeScript handler built for the
// same name, arguments and caller (testdata/handler_asts.json, captured from
// soloclef_web's app/api/zero/query route before it was removed). Compared
// as parsed JSON, so key order is not part of it.
func TestEveryQueryIsTheASTZerosOwnHandlerBuilt(t *testing.T) {
	raw, err := os.ReadFile("testdata/handler_asts.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	args := map[string]string{
		"messages.byThread":           `{"threadId":7}`,
		"chatSession.byThread":        `{"threadId":7}`,
		"automationRuns.byAutomation": `{"automationId":9}`,
		"podcastRuns.byId":            `{"podcastId":3}`,
		"deliverableJobs.byId":        `{"jobId":4}`,
	}
	var batch []string
	for _, query := range want["queries"].([]any) {
		q := query.(map[string]any)
		name := q["name"].(string)
		argList := "[]"
		if a, ok := args[name]; ok {
			argList = "[" + a + "]"
		}
		batch = append(batch, `{"id":"`+q["id"].(string)+`","name":"`+name+`","args":`+argList+`}`)
	}
	if len(batch) != len(shapes) {
		t.Fatalf("the fixture has %d queries and the package %d", len(batch), len(shapes))
	}
	status, got := ask(t, caller, `["transform",[`+strings.Join(batch, ",")+`]]`)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	wantQueries, gotQueries := want["queries"].([]any), got["queries"].([]any)
	for i := range wantQueries {
		if !reflect.DeepEqual(gotQueries[i], wantQueries[i]) {
			g, _ := json.Marshal(gotQueries[i])
			w, _ := json.Marshal(wantQueries[i])
			t.Errorf("query %d:\n got %s\nwant %s", i, g, w)
		}
	}
	if got["kind"] != "QueryResponse" || got["userID"] != caller {
		t.Errorf("kind %v, userID %v", got["kind"], got["userID"])
	}
}

// The account in an AST is the gate's caller, whatever the request says: a
// client cannot name another account, only an id inside its own rows.
func TestTheAccountIsAlwaysTheCallersOwn(t *testing.T) {
	_, got := ask(t, "someone-else",
		`["transform",[{"id":"a","name":"podcastRuns.byId","args":[{"podcastId":3,"userId":"`+caller+`"}]}]]`)
	body, _ := json.Marshal(got)
	if strings.Contains(string(body), caller) || !strings.Contains(string(body), "someone-else") {
		t.Errorf("the AST names the wrong account: %s", body)
	}
}

// A query that cannot be built is refused alone, as Zero's handler does:
// the batch's other queries are still answered.
func TestABadQueryIsRefusedAndTheRestAnswered(t *testing.T) {
	for _, c := range []struct {
		name, query, message string
	}{
		{"an unknown name", `{"id":"b","name":"nope.nope","args":[]}`, "Query not found: nope.nope"},
		{"an id that is text", `{"id":"b","name":"messages.byThread","args":[{"threadId":"7"}]}`,
			"Validation failed for query messages.byThread: threadId: expected number"},
		{"an id with a fraction", `{"id":"b","name":"podcastRuns.byId","args":[{"podcastId":3.5}]}`,
			"Validation failed for query podcastRuns.byId: podcastId: expected an integer"},
		{"no id", `{"id":"b","name":"deliverableJobs.byId","args":[{}]}`,
			"Validation failed for query deliverableJobs.byId: jobId: expected number, received undefined"},
		{"no argument at all", `{"id":"b","name":"deliverableJobs.byId","args":[]}`,
			`Validation failed for query deliverableJobs.byId: expected an object with "jobId"`},
	} {
		status, got := ask(t, caller,
			`["transform",[`+c.query+`,{"id":"ok","name":"folders.mine","args":[]}]]`)
		if status != http.StatusOK {
			t.Fatalf("%s: status %d", c.name, status)
		}
		queries := got["queries"].([]any)
		bad, good := queries[0].(map[string]any), queries[1].(map[string]any)
		if bad["error"] != "app" || bad["id"] != "b" || bad["message"] != c.message || bad["ast"] != nil {
			t.Errorf("%s: refused as %v", c.name, bad)
		}
		if good["ast"] == nil {
			t.Errorf("%s: the good query was not answered: %v", c.name, good)
		}
	}
}

// A body that is not a transform request is TransformFailed, in the body
// and with a 200, which is where zero-cache reads it.
func TestABodyThatIsNotATransformRequestFailsAsAWhole(t *testing.T) {
	for _, body := range []string{
		`not json`, `{}`, `["push",[]]`, `["transform"]`, `["transform",{}]`,
		`["transform",[{"id":"a","name":"folders.mine"}]]`,
	} {
		status, got := ask(t, caller, body)
		if status != http.StatusOK || got["kind"] != "TransformFailed" || got["origin"] != "server" ||
			got["reason"] != "parse" || !strings.HasPrefix(got["message"].(string), "Failed to parse query request: ") {
			t.Errorf("%s: %d %v", body, status, got)
		}
	}
}

// Without a caller nothing is built: the gate answers 401 before this
// route, and a wiring that left the gate out is a 500, never a query.
func TestWithoutACallerNothingIsBuilt(t *testing.T) {
	status, got := ask(t, "", `["transform",[{"id":"a","name":"folders.mine","args":[]}]]`)
	if status != http.StatusInternalServerError || got["queries"] != nil {
		t.Errorf("%d %v", status, got)
	}
}

func TestAnOversizedBatchIsRefused(t *testing.T) {
	status, _ := ask(t, caller, `["transform",[`+strings.Repeat(" ", bodyMax)+`]]`)
	if status != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d", status)
	}
}
