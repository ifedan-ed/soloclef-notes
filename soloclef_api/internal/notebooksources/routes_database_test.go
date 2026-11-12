package notebooksources

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_NOTEBOOKSOURCES_DATABASE"

var moment = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

// Each route, asked by the owner and by the other account. The other
// account is answered as if the notebook, the source or the document did
// not exist, and changes nothing.
func TestANotebooksSourcesAreItsOwnersAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title, updated_at) VALUES
 (10, '`+alice+`', 'Tides', '2026-01-01'), (20, '`+bob+`', 'His', '2026-01-01');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status) VALUES
 (100, '`+alice+`', 'Tide tables', 'FILE', 'x', 'a', '{"state":"ready"}'),
 (101, '`+alice+`', 'Moon notes', 'NOTE', 'x', 'b', '{"state":"processing"}'),
 (200, '`+bob+`',   'His file', 'FILE', 'x', 'c', '{"state":"ready"}');
INSERT INTO folders (id, user_id, name, position) VALUES
 (300, '`+alice+`', 'Charts', 'a0'), (400, '`+bob+`', 'His folder', 'a0');
INSERT INTO notebook_sources (id, notebook_id, user_id, document_id, added_at) VALUES
 (51, 10, '`+alice+`', 100, '2026-01-02'), (52, 20, '`+bob+`', 200, '2026-01-02');`)

	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Pool: pool, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return moment }}.Register(api)
	as := func(userID, method, path, body string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: principal.MethodSession}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	count := func(query string, arguments ...any) (n int) {
		t.Helper()
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+query, arguments...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	expect := func(name string, status int, body string, wantStatus int, says ...string) {
		t.Helper()
		if status != wantStatus {
			t.Errorf("%s: %d %s", name, status, body)
			return
		}
		for _, piece := range says {
			if !strings.Contains(body, piece) {
				t.Errorf("%s: %s lacks %s", name, body, piece)
			}
		}
	}

	status, body := as(alice, "GET", "/api/v1/notebooks/10/sources", "")
	expect("her list", status, body, 200, `"id":51,"kind":"document","target_id":100,"title":"Tide tables","document_type":"FILE","status":"ready","enabled":true`)
	status, body = as(bob, "GET", "/api/v1/notebooks/10/sources", "")
	expect("her list asked by him", status, body, 404, notebookAbsent)

	// He cannot add her document to his notebook, nor anything to hers.
	status, body = as(bob, "POST", "/api/v1/notebooks/20/sources", `{"targets":[{"kind":"document","id":100}]}`)
	expect("her document into his notebook", status, body, 404, sourceAbsent)
	status, body = as(bob, "POST", "/api/v1/notebooks/10/sources", `{"targets":[{"kind":"document","id":200}]}`)
	expect("his document into her notebook", status, body, 404, notebookAbsent)
	// One target that is not hers stops the whole add.
	status, body = as(alice, "POST", "/api/v1/notebooks/10/sources",
		`{"targets":[{"kind":"document","id":101},{"kind":"folder","id":400}]}`)
	expect("a mixed add with his folder", status, body, 404, sourceAbsent)
	if count("notebook_sources WHERE notebook_id = 10") != 1 {
		t.Fatal("a refused add left a link behind")
	}

	status, body = as(alice, "POST", "/api/v1/notebooks/10/sources",
		`{"targets":[{"kind":"document","id":101},{"kind":"folder","id":300},{"kind":"document","id":100}]}`)
	expect("her add", status, body, 200, `"target_id":101,"title":"Moon notes","document_type":"NOTE","status":"processing"`,
		`"kind":"folder","target_id":300,"title":"Charts","document_type":null,"status":null`)
	if count("notebook_sources WHERE notebook_id = 10") != 3 || count("notebooks WHERE id = 10 AND updated_at = $1", moment) != 1 {
		t.Error("the add did not link each target once, or did not mark the notebook used")
	}

	status, body = as(alice, "PATCH", "/api/v1/notebooks/10/sources/51", `{"enabled":false}`)
	expect("her untick", status, body, 200, `"id":51,`, `"enabled":false`)
	status, body = as(bob, "PATCH", "/api/v1/notebooks/20/sources/51", `{"enabled":false}`)
	expect("her source through his notebook", status, body, 404, sourceAbsent)
	status, body = as(alice, "PATCH", "/api/v1/notebooks/10/sources/51", `{}`)
	expect("an untick that says nothing", status, body, 422, "enabled")

	// Untick all, listing his source's id beside hers: his stays ticked.
	status, body = as(alice, "POST", "/api/v1/notebooks/10/sources/toggle", `{"enabled":false,"source_ids":[52]}`)
	expect("a listed toggle naming his source", status, body, 200)
	if count("notebook_sources WHERE id = 52 AND enabled") != 1 {
		t.Error("her toggle changed his source")
	}
	status, body = as(alice, "POST", "/api/v1/notebooks/10/sources/toggle", `{"enabled":false}`)
	expect("untick all", status, body, 200)
	if count("notebook_sources WHERE notebook_id = 10 AND enabled") != 0 || count("notebook_sources WHERE id = 52 AND enabled") != 1 {
		t.Error("untick all missed one of hers or reached his")
	}

	status, body = as(bob, "DELETE", "/api/v1/notebooks/10/sources/51", "")
	expect("his delete of her source", status, body, 404, notebookAbsent)
	status, body = as(alice, "DELETE", "/api/v1/notebooks/10/sources/51", "")
	expect("her delete", status, body, 204)
	if count("notebook_sources WHERE id = 51") != 0 || count("documents WHERE id = 100") != 1 {
		t.Error("the delete left the link or took the document")
	}
	status, body = as(alice, "DELETE", "/api/v1/notebooks/10/sources/51", "")
	expect("a second delete", status, body, 404, sourceAbsent)
	status, body = as(alice, "GET", "/api/v1/notebooks/x/sources", "")
	expect("an id that is not a number", status, body, 422, "notebook_id")
}

type queued struct{ messages []produce.Message }

func (q *queued) Enqueue(_ context.Context, message produce.Message) (string, error) {
	q.messages = append(q.messages, message)
	return "id", nil
}

// A notebook still named by the app is asked to name itself when a ready
// document becomes its source, and only then: not for a document still
// being read (the indexer asks for that one when it is ready), not for a
// notebook its owner named, and not after it has used its two names.
func TestAddingAReadySourceAsksForTheNotebooksName(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice = testpostgres.Alice
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title, title_source, auto_title, updated_at) VALUES
 (10, '`+alice+`', 'Untitled notebook', 'auto', NULL, '2026-01-01'),
 (11, '`+alice+`', 'Untitled notebook', 'auto', NULL, '2026-01-01'),
 (12, '`+alice+`', 'Her own name', 'user', NULL, '2026-01-01'),
 (13, '`+alice+`', 'Named twice', 'auto', '{"runs": 2}', '2026-01-01');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status) VALUES
 (100, '`+alice+`', 'Tide tables', 'FILE', 'x', 'a', '{"state":"ready"}'),
 (101, '`+alice+`', 'Moon notes', 'NOTE', 'x', 'b', '{"state":"processing"}');`)
	naming := &queued{}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Pool: pool, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return moment }, Naming: naming}.Register(api)
	add := func(notebook, document string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/notebooks/"+notebook+"/sources",
			strings.NewReader(`{"targets":[{"kind":"document","id":`+document+`}]}`))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: alice, Method: principal.MethodSession}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("adding %s to %s: %d %s", document, notebook, recorder.Code, recorder.Body.String())
		}
	}
	add("10", "100")
	add("11", "101")
	add("12", "100")
	add("13", "100")
	if len(naming.messages) != 1 {
		t.Fatalf("asked %d times, want once (notebook 10): %+v", len(naming.messages), naming.messages)
	}
	message := naming.messages[0]
	if message.Task != "notebook_autoname" || !reflect.DeepEqual(message.Args, []any{int64(10), "source", nil}) || message.Countdown <= 0 {
		t.Errorf("message = %+v", message)
	}
}
