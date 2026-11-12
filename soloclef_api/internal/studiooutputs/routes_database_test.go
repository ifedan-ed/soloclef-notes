package studiooutputs

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_STUDIOOUTPUTS_DATABASE"

// removals records the documents a delete removed.
type removals struct{ removed []int64 }

func (r *removals) Remove(_ context.Context, documentID int64) error {
	r.removed = append(r.removed, documentID)
	return nil
}

func TestANotebooksOutputsAreItsOwnersAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title) VALUES (10, '`+alice+`', 'Tides'), (20, '`+bob+`', 'His');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash) VALUES
 (500, '`+alice+`', 'Deck', 'ARTIFACT', '', 'd'), (501, '`+alice+`', 'Audio', 'ARTIFACT', '', 'e');
INSERT INTO artifacts (id, document_id, user_id, format, metadata) VALUES
 (70, 500, '`+alice+`', 'slides', '{"duration_seconds": 61.6}'), (71, 501, '`+alice+`', 'podcast', NULL);
INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, status, phase, progress, artifact_id) VALUES
 (80, '`+alice+`', 'slides', 'Deck', 't1', 'ready', 'done', 100, 70),
 (81, '`+alice+`', 'report', 'Draft', 't2', 'running', 'writing', 40, NULL),
 (82, '`+alice+`', 'report', 'Queued', 't3', 'queued', NULL, 0, NULL);
INSERT INTO podcast_runs (id, user_id, title, status, duration_seconds, artifact_id) VALUES
 (90, '`+alice+`', 'Talk', 'rendering', NULL, 71);
INSERT INTO notebook_outputs (id, notebook_id, user_id, kind, title, deliverable_job_id, podcast_id, source_document_ids, created_at) VALUES
 (1001, 10, '`+alice+`', 'slides', 'Deck', 80, NULL, '[1,2]', '2026-01-01'),
 (1002, 10, '`+alice+`', 'report', 'Draft', 81, NULL, '[]', '2026-01-02'),
 (1003, 10, '`+alice+`', 'report', 'Queued', 82, NULL, '[]', '2026-01-03'),
 (1004, 10, '`+alice+`', 'audio', 'Talk', NULL, 90, '[1]', '2026-01-04'),
 (1005, 20, '`+bob+`',   'report', 'His', NULL, NULL, '[]', '2026-01-05');`)

	removed := &removals{}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Pool: pool, Removal: removed, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) }}.Register(api)
	as := func(userID, method, path, body string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: principal.MethodSession}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	count := func(query string) (n int) {
		t.Helper()
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
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

	status, body := as(alice, "GET", "/api/v1/notebooks/10/studio/outputs", "")
	expect("her outputs", status, body, 200,
		`[{"id":1004,"kind":"audio","title":"Talk","status":"running","phase":"rendering"`,
		`"id":1001,"kind":"slides","title":"Deck","status":"ready","phase":"done","progress":100,"failure_code":null,"artifact_id":70,"artifact_format":"slides","duration_seconds":62,"source_count":2,"options":{},"generated_by":null`)
	if strings.Contains(body, "His") {
		t.Error("her list shows his output")
	}
	status, body = as(bob, "GET", "/api/v1/notebooks/10/studio/outputs", "")
	expect("her outputs asked by him", status, body, 404, notebookAbsent)

	status, body = as(alice, "PATCH", "/api/v1/notebooks/10/studio/outputs/1001", `{"title":"  Final deck "}`)
	expect("her rename", status, body, 200, `"id":1001`, `"title":"Final deck"`)
	status, body = as(bob, "PATCH", "/api/v1/notebooks/20/studio/outputs/1001", `{"title":"Mine"}`)
	expect("her output through his notebook", status, body, 404, outputAbsent)
	status, body = as(alice, "PATCH", "/api/v1/notebooks/10/studio/outputs/1001", `{"title":"   "}`)
	expect("a blank title", status, body, 422, "title")

	// Deleting stops each kind of run and removes what was made.
	status, body = as(bob, "DELETE", "/api/v1/notebooks/10/studio/outputs/1002", "")
	expect("his delete of hers", status, body, 404, outputAbsent)
	for _, id := range []string{"1001", "1002", "1003", "1004"} {
		status, body = as(alice, "DELETE", "/api/v1/notebooks/10/studio/outputs/"+id, "")
		expect("her delete of "+id, status, body, 204)
	}
	if count("notebook_outputs WHERE notebook_id = 10") != 0 || count("notebook_outputs WHERE id = 1005") != 1 {
		t.Error("the deletes missed one of hers or took his")
	}
	if count("deliverable_jobs WHERE id = 81 AND status = 'cancelling' AND cancel_requested_at IS NOT NULL") != 1 ||
		count("deliverable_jobs WHERE id = 82 AND status = 'cancelled' AND finished_at IS NOT NULL") != 1 ||
		count("deliverable_jobs WHERE id = 80 AND status = 'ready'") != 1 ||
		count("podcast_runs WHERE id = 90 AND status = 'cancelled'") != 1 {
		t.Error("a run was not stopped as its state asks, or a finished one was changed")
	}
	if len(removed.removed) != 2 || removed.removed[0] != 500 || removed.removed[1] != 501 ||
		count("documents WHERE id IN (500, 501) AND status ->> 'state' = 'deleting'") != 2 {
		t.Errorf("removed %v: each made artifact's document should be claimed and removed", removed.removed)
	}
	status, body = as(alice, "DELETE", "/api/v1/notebooks/10/studio/outputs/1001", "")
	expect("a second delete", status, body, 404, outputAbsent)
}
