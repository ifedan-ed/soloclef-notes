package studioviews

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/studiogenerate"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_STUDIOVIEWS_DATABASE"

// quickModel answers the suggestions call with a fixed reply, or fails.
type quickModel struct {
	reply string
	err   error
	asked []model.Request
}

func (m *quickModel) Generate(_ context.Context, request model.Request, _ func(model.Chunk)) (model.Response, error) {
	m.asked = append(m.asked, request)
	if m.err != nil {
		return model.Response{}, m.err
	}
	return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: m.reply}, FinishReason: "stop"}, nil
}

// studio stands where studiogenerate.Routes is: it records what it was
// asked to start and writes the output and its running job, as the real one
// does, so the view can find them.
type studio struct {
	pool    *pgxpool.Pool
	started []studiogenerate.FromReport
	callers []studiogenerate.Caller
	nextID  int64
	refuse  bool
}

func (s *studio) CanMake(kind string) bool { return kind != "data_table" }

func (s *studio) StartFromReport(ctx context.Context, who studiogenerate.Caller, notebookID int64, asked studiogenerate.FromReport) (int64, error) {
	if s.refuse {
		// The real refusal type is unexported; the real routes are asked for
		// one, which is a refusal by construction: a kind outside the list.
		_, err := studiogenerate.Routes{}.StartFromReport(ctx, who, notebookID, studiogenerate.FromReport{Kind: "nonsense"})
		return 0, err
	}
	s.started, s.callers = append(s.started, asked), append(s.callers, who)
	s.nextID++
	id := 3000 + s.nextID
	_, err := s.pool.Exec(ctx, `
WITH job AS (
  INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, status, phase, progress)
  VALUES ($1, $2, 'artifact', $3, $4, 'running', 'generating', 10) RETURNING id)
INSERT INTO notebook_outputs (id, notebook_id, user_id, kind, title, deliverable_job_id)
SELECT $1, $5, $2, $6, $3, job.id FROM job`, id, who.UserID, asked.Title, asked.RequestID, notebookID, asked.Kind)
	return id, err
}

func TestTheStudioViewsAreTheOwnersAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title) VALUES (10, '`+alice+`', 'PICU'), (20, '`+bob+`', 'His');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, source_markdown) VALUES
 (500, '`+alice+`', 'Orientation', 'ARTIFACT', '', 'a', $md$`+sampleReport+`$md$),
 (501, '`+alice+`', 'Deck', 'ARTIFACT', 'slides', 'b', NULL),
 (502, '`+alice+`', 'Page', 'ARTIFACT', '<html>', 'c', E'## A\n\nx\n\n## B\n\ny'),
 (600, '`+alice+`', 'Handbook', 'FILE', 'text', 'd', NULL),
 (700, '`+bob+`',   'His notes', 'NOTE', 'text', 'e', NULL),
 (800, '`+bob+`',   'His report', 'ARTIFACT', E'## One\n\nx\n\n## Two\n\ny', 'f', NULL);
INSERT INTO artifacts (id, document_id, user_id, format, generation, metadata, updated_at) VALUES
 (70, 500, '`+alice+`', 'pdf', 2, '{"pages": 4, "report_view": {"generation": 1, "ready": true, "suggestions": [{"id": "g1s1", "section": 0, "kind": "quiz", "title": "Old version"}]}}', '2026-01-01'),
 (71, 501, '`+alice+`', 'pptx', 1, NULL, '2026-01-01'),
 (72, 502, '`+alice+`', 'html', 1, NULL, '2026-01-01'),
 (90, 800, '`+bob+`',   'pdf', 1, NULL, '2026-01-01');
INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, status, phase, progress, artifact_id) VALUES
 (80, '`+alice+`', 'artifact', 'Orientation', 't1', 'ready', 'done', 100, 70),
 (81, '`+alice+`', 'artifact', 'Draft', 't2', 'running', 'generating', 40, NULL),
 (82, '`+alice+`', 'artifact', 'Deck', 't3', 'ready', 'done', 100, 71),
 (83, '`+alice+`', 'artifact', 'Page', 't4', 'ready', 'done', 100, 72),
 (95, '`+bob+`',   'artifact', 'His report', 't5', 'ready', 'done', 100, 90);
INSERT INTO notebook_outputs (id, notebook_id, user_id, kind, title, options, source_document_ids, deliverable_job_id) VALUES
 (1001, 10, '`+alice+`', 'report', 'Orientation', '{"kind": "report", "focus": "  Rounds and handover ", "language": "English"}', '[600, 700, "x", 99999]', 80),
 (1002, 10, '`+alice+`', 'report', 'Draft', '{}', '[]', 81),
 (1003, 10, '`+alice+`', 'slides', 'Deck', '{"focus": 7}', '[600]', 82),
 (1004, 10, '`+alice+`', 'report', 'Page', '{"format": "interactive"}', '[]', 83),
 (2001, 20, '`+bob+`',   'report', 'His report', '{}', '[700]', 95);`)

	quick := &quickModel{reply: `{"report_suggestions": [
 {"section": 3, "kind": "flashcards", "title": "Escalation triggers", "focus": "Who to call and when."},
 {"section": 2, "kind": "audio", "title": "Rounds, talked through", "focus": "Day rounds and night handover."},
 {"section": 1, "kind": "data_table", "title": "Not makeable here", "focus": ""}]}`}
	made := &studio{pool: pool}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes := Routes{
		Pool:         pool,
		QuickModel:   func(context.Context, string) (string, error) { return "quick", nil },
		Open:         func(context.Context, string, string) (model.Model, error) { return quick, nil },
		ModelTimeout: 5 * time.Second,
		Studio:       made,
		Errors:       errorenvelope.Writer{},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	routes.Register(api)
	as := func(userID, method, path string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(method, path, nil)
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: principal.MethodSession}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		if recorder.Code == 200 && recorder.Header().Get("Cache-Control") != "private, no-store" {
			t.Errorf("%s %s may be kept by a cache", method, path)
		}
		return recorder.Code, recorder.Body.String()
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
	text := func(query string, args ...any) (value string) {
		t.Helper()
		if err := pool.QueryRow(context.Background(), query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	const base = "/api/v1/notebooks/10/studio/outputs/"

	// The viewer's chip: the focus and the sources, the caller's own only.
	status, body := as(alice, "GET", base+"1001/view")
	expect("her report's view", status, body, 200,
		`{"output_id":1001,"artifact_id":70,"kind":"report","prompt":"Rounds and handover","sources":[{"id":600,"title":"Handbook","document_type":"FILE"}]}`)
	status, body = as(alice, "GET", base+"1003/view")
	expect("her deck's view, a focus that is not text", status, body, 200, `"kind":"slides","prompt":""`)
	for name, c := range map[string]struct{ user, path string }{
		"hers asked by him":              {bob, base + "1001/view"},
		"hers through his notebook":      {bob, "/api/v1/notebooks/20/studio/outputs/1001/view"},
		"his through her notebook":       {alice, base + "2001/view"},
		"one still being made":           {alice, base + "1002/view"},
		"one never made":                 {alice, base + "424242/view"},
		"an id wider than a bigint":      {alice, base + "99999999999999999999999/view"},
		"his report's view asked by her": {alice, "/api/v1/notebooks/20/studio/outputs/2001/view"},
	} {
		status, body = as(c.user, "GET", c.path)
		expect(name, status, body, 404, outputAbsent)
	}
	status, body = as(alice, "GET", base+"abc/view")
	expect("an id that is not a number", status, body, 422, "output_id")

	// The reading view. The first open asks the model; the entry stored for
	// the report's earlier version is not this version's.
	status, body = as(alice, "GET", base+"1001/report-view")
	expect("her reading view", status, body, 200,
		`{"output_id":1001,"artifact_id":70,"generation":2,"format":"document","language":"en","suggestions_status":"ready","suggestions":[`+
			`{"id":"g2s1","section_index":1,"section_heading":"Navigating the PICU: Rounds","kind":"audio","title":"Rounds, talked through","focus":"Day rounds and night handover.","state":"open","output_id":null},`+
			`{"id":"g2s2","section_index":2,"section_heading":"Communication & Escalation","kind":"flashcards","title":"Escalation triggers","focus":"Who to call and when.","state":"open","output_id":null}],`+
			`"sources":[{"id":600,"title":"Handbook","document_type":"FILE"}]}`)
	if len(quick.asked) != 1 {
		t.Fatalf("the model was asked %d times", len(quick.asked))
	}
	if system := quick.asked[0].Messages[0].Content; strings.Contains(system, "data_table") || !strings.Contains(system, "in English") {
		t.Errorf("the prompt offers a kind the studio cannot make, or names no language: %s", system)
	}
	status, _ = as(alice, "GET", base+"1001/report-view")
	if status != 200 || len(quick.asked) != 1 {
		t.Errorf("a second open: %d, the model asked %d times; suggestions are written once a version", status, len(quick.asked))
	}
	if kept := text(`SELECT metadata ->> 'pages' FROM artifacts WHERE id = 70`); kept != "4" {
		t.Errorf("the artifact's other metadata: pages = %s", kept)
	}
	if moved := text(`SELECT (updated_at <> '2026-01-01')::text FROM artifacts WHERE id = 70`); moved != "false" {
		t.Error("storing suggestions moved the artifact's updated_at, which means its content changed")
	}

	for name, c := range map[string]struct{ user, method, path string }{
		"hers asked by him":            {bob, "GET", base + "1001/report-view"},
		"hers through his notebook":    {bob, "GET", "/api/v1/notebooks/20/studio/outputs/1001/report-view"},
		"an output that is no report":  {alice, "GET", base + "1003/report-view"},
		"a report still being made":    {alice, "GET", base + "1002/report-view"},
		"his dismissal of hers":        {bob, "POST", base + "1001/report-suggestions/g2s1/dismiss"},
		"his add of hers":              {bob, "POST", base + "1001/report-suggestions/g2s1/add"},
		"his add through his notebook": {bob, "POST", "/api/v1/notebooks/20/studio/outputs/1001/report-suggestions/g2s1/add"},
	} {
		status, body = as(c.user, c.method, c.path)
		expect(name, status, body, 404, reportAbsent)
	}
	for _, path := range []string{"g1s1/add", "g1s1/dismiss", "nope/add", strings.Repeat("x", 80) + "/dismiss"} {
		status, body = as(alice, "POST", base+"1001/report-suggestions/"+path)
		expect("a suggestion this version does not have: "+path, status, body, 404, suggestionAbsent)
	}
	if len(made.started) != 0 {
		t.Fatalf("a refused request started %d outputs", len(made.started))
	}

	// An interactive report has no sections to put a card under.
	status, body = as(alice, "GET", base+"1004/report-view")
	expect("an interactive report", status, body, 200, `"format":"interactive"`, `"suggestions_status":"unavailable","suggestions":[]`)
	if len(quick.asked) != 1 {
		t.Error("the model was asked about an interactive report")
	}

	// Dismiss.
	status, body = as(alice, "POST", base+"1001/report-suggestions/g2s2/dismiss")
	expect("her dismissal", status, body, 200, `"id":"g2s2"`, `"state":"dismissed"`, `"id":"g2s1","section_index":1`)

	// Add starts one output, from the report's own sources, about the section.
	status, body = as(alice, "POST", base+"1001/report-suggestions/g2s1/add")
	expect("her add", status, body, 200, `"id":"g2s1"`, `"state":"added","output_id":3001`)
	if len(made.started) != 1 {
		t.Fatalf("started %d outputs", len(made.started))
	}
	asked := made.started[0]
	if asked.Kind != "audio" || asked.Title != "Rounds, talked through" || asked.Language != "en" || asked.RequestID != "report1001g2s1a0" ||
		len(asked.DocumentIDs) != 3 || asked.DocumentIDs[0] != 600 || made.callers[0].UserID != alice {
		t.Errorf("asked %+v as %+v", asked, made.callers[0])
	}
	for _, piece := range []string{"Rounds, talked through: Day rounds and night handover.",
		`Base it on the section "Navigating the PICU: Rounds" of the report "CHAM PICU Resident Orientation"`,
		"The section reads:\nDay rounds start at 08:00"} {
		if !strings.Contains(asked.Focus, piece) {
			t.Errorf("the focus lacks %q: %s", piece, asked.Focus)
		}
	}
	status, body = as(alice, "POST", base+"1001/report-suggestions/g2s1/add")
	expect("a second click", status, body, 200, `"state":"added","output_id":3001`)
	if len(made.started) != 1 {
		t.Error("a second click started a second output")
	}
	// The output failed: adding again is a new attempt.
	testpostgres.Exec(t, pool, `UPDATE deliverable_jobs SET status = 'failed' WHERE id = 3001`)
	status, body = as(alice, "POST", base+"1001/report-suggestions/g2s1/add")
	expect("an add after a failure", status, body, 200, `"state":"added","output_id":3002`)
	if len(made.started) != 2 || made.started[1].RequestID != "report1001g2s1a1" {
		t.Errorf("started %+v", made.started)
	}
	// The person deletes that output from the Studio: the idea is offered again.
	testpostgres.Exec(t, pool, `DELETE FROM notebook_outputs WHERE id = 3002`)
	status, body = as(alice, "GET", base+"1001/report-view")
	expect("after the output is deleted", status, body, 200, `"id":"g2s1"`, `"focus":"Day rounds and night handover.","state":"open","output_id":null`)

	// A generation the studio refuses is refused as the studio says, and
	// the suggestion is left as it was.
	made.refuse = true
	status, body = as(alice, "POST", base+"1001/report-suggestions/g2s1/add")
	expect("a refused generation", status, body, 422, "Input should be one of")
	made.refuse = false
	if state := text(`SELECT metadata #>> '{report_view,suggestions,0,attempts}' FROM artifacts WHERE id = 70`); state != "2" {
		t.Errorf("attempts after a refused add: %s", state)
	}

	// A model that cannot be reached: the report opens without suggestions,
	// nothing is stored, and the next open asks again.
	quick.err = errors.New("the proxy is down")
	status, body = as(bob, "GET", "/api/v1/notebooks/20/studio/outputs/2001/report-view")
	expect("his view, the model down", status, body, 200, `"suggestions_status":"unavailable","suggestions":[]`, `"title":"His notes"`)
	if stored := text(`SELECT COALESCE(metadata ->> 'report_view', 'none') FROM artifacts WHERE id = 90`); stored != "none" {
		t.Errorf("a failed call stored %s", stored)
	}
	// A reply in the wrong language is asked for once more, then given up.
	quick.err, quick.reply = nil, `{"report_suggestions": [{"section": 1, "kind": "quiz", "title": "升级触发条件以及联系人", "focus": "何时呼叫以及呼叫谁"}]}`
	before := len(quick.asked)
	status, body = as(bob, "GET", "/api/v1/notebooks/20/studio/outputs/2001/report-view")
	expect("his view, the wrong language twice", status, body, 200, `"suggestions_status":"unavailable"`)
	if len(quick.asked)-before != 2 || !strings.Contains(quick.asked[len(quick.asked)-1].Messages[0].Content, "was not in English") {
		t.Errorf("asked %d more times; the second must say what was wrong", len(quick.asked)-before)
	}
	quick.reply = `{"report_suggestions": [{"section": 2, "kind": "quiz", "title": "Check yourself", "focus": "Section two."}]}`
	status, body = as(bob, "GET", "/api/v1/notebooks/20/studio/outputs/2001/report-view")
	expect("his view, the model back", status, body, 200, `"suggestions_status":"ready"`, `"id":"g1s1","section_index":1,"section_heading":"Two","kind":"quiz"`)
	// And hers were untouched by any of his.
	if hers := text(`SELECT metadata #>> '{report_view,suggestions,1,state}' FROM artifacts WHERE id = 70`); hers != "dismissed" {
		t.Errorf("her second suggestion is %s", hers)
	}
}
