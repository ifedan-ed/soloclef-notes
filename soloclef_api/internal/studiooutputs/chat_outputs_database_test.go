package studiooutputs

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
	"git.danvics.com/danvics/soloclef/soloclef_schema/migrations"
	"git.danvics.com/danvics/soloclef/soloclef_studio/artifactsave"
)

// What a notebook's chat makes is listed in that notebook's Studio: an
// artifact, a video and a podcast, each written by the same code a chat
// turn runs (artifactsave, deliverables), then read, opened and deleted
// through the Studio's own routes.

// studio is the Studio's routes over a real database, asked as either
// account.
type studio struct {
	t       *testing.T
	pool    *pgxpool.Pool
	api     huma.API
	removed *removals
}

func newStudio(t *testing.T) *studio {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	// Her notebook and his; her notebook's chat (300), a Studio
	// generation's hidden thread in it (301), and a chat outside any
	// notebook (302).
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title) VALUES (10, '`+alice+`', 'Tides'), (20, '`+bob+`', 'His');
INSERT INTO new_chat_threads (id, user_id, title, notebook_id, source) VALUES
 (300, '`+alice+`', 'Notebook chat', 10, 'surfsense'),
 (301, '`+alice+`', 'A generation', 10, 'artifact_api'),
 (302, '`+alice+`', 'A plain chat', NULL, 'surfsense');`)
	s := &studio{t: t, pool: pool, removed: &removals{}}
	s.api = humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Pool: pool, Removal: s.removed, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC) }}.Register(s.api)
	return s
}

func (s *studio) as(userID, method, path string) (int, string) {
	s.t.Helper()
	request := httptest.NewRequest(method, path, nil)
	request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: userID, Method: principal.MethodSession}))
	recorder := httptest.NewRecorder()
	s.api.Adapter().ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

func (s *studio) count(query string) (n int) {
	s.t.Helper()
	if err := s.pool.QueryRow(context.Background(), "SELECT count(*) FROM "+query).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}

// save saves an artifact with no file (as the chat's specialist saves a
// mind map) for owner, through the chat's own saver.
func (s *studio) save(owner artifactsave.Owner, title, format string) artifactsave.Created {
	s.t.Helper()
	created, err := artifactsave.Store{Pool: s.pool}.Create(context.Background(), artifactsave.NewArtifact{
		Owner: owner, ToolCallID: "call-" + title, Title: title, Format: format,
		Markdown: "# " + title, Metadata: []byte(`{}`),
	}, nil, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return created
}

func TestWhatANotebooksChatMakesIsInItsStudio(t *testing.T) {
	s := newStudio(t)
	const alice = testpostgres.Alice
	inChat := artifactsave.Owner{UserID: alice, ThreadID: 300, NotebookID: 10}

	quiz := s.save(inChat, "Tide quiz", deliverables.FormatQuiz)
	report := s.save(inChat, "Tide report", deliverables.FormatDOCX)
	// A picture drawn in the chat is a picture in the chat. The image model
	// that made them is gone (migration 00026), but older chats still hold
	// artifacts saved with format "image", spelled here as it is stored.
	s.save(inChat, "A drawing", "image")
	// A chat outside any notebook, and a Studio generation (whose output the
	// Studio wrote itself), list nothing here.
	s.save(artifactsave.Owner{UserID: alice, ThreadID: 302}, "Elsewhere", deliverables.FormatQuiz)
	s.save(artifactsave.Owner{UserID: alice, ThreadID: 301}, "Generated", deliverables.FormatQuiz)

	video, err := deliverables.VideoStore{Pool: s.pool}.CreateVideo(context.Background(), inChat, "call-video", "Tide video", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	// A second run of the same call (a turn continued after a restart)
	// finds its job and lists nothing twice.
	if _, err := (deliverables.VideoStore{Pool: s.pool}).CreateVideo(context.Background(), inChat, "call-video", "Tide video", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	podcast, err := deliverables.PodcastStore{Pool: s.pool}.Create(context.Background(), inChat, "Tide talk", "the sources",
		func([]byte) ([]byte, error) { return []byte(`{}`), nil })
	if err != nil {
		t.Fatal(err)
	}

	status, body := s.as(alice, "GET", "/api/v1/notebooks/10/studio/outputs")
	if status != 200 {
		t.Fatalf("%d %s", status, body)
	}
	for _, want := range []string{
		`"kind":"quiz","title":"Tide quiz","status":"ready","phase":null,"progress":null,"failure_code":null,"artifact_id":` + strconv.FormatInt(quiz.ArtifactID, 10) + `,"artifact_format":"quiz","duration_seconds":null,"source_count":0,"options":{"kind":"quiz"}`,
		`"kind":"report","title":"Tide report","status":"ready"`,
		// jsonb keeps an object's keys in its own order.
		`"options":{"kind":"report","format":"document","file_type":"docx"}`,
		`"kind":"video","title":"Tide video","status":"queued"`,
		`"kind":"audio","title":"Tide talk","status":"queued"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the Studio lacks %s:\n%s", want, body)
		}
	}
	for _, absent := range []string{"A drawing", "Elsewhere", "Generated"} {
		if strings.Contains(body, absent) {
			t.Errorf("the Studio lists %q", absent)
		}
	}
	if s.count("notebook_outputs") != 4 ||
		s.count("notebook_outputs WHERE deliverable_job_id = "+strconv.FormatInt(video.JobID, 10)) != 1 ||
		s.count("notebook_outputs WHERE podcast_id = "+strconv.FormatInt(podcast, 10)+" AND thread_id = 300") != 1 {
		t.Error("the outputs are not one per thing made, each remembering its chat")
	}

	// Deleting it from the Studio removes the artifact, as for one the
	// Studio made: its document is claimed for removal and removed.
	var outputID int64
	if err := s.pool.QueryRow(context.Background(), `SELECT id FROM notebook_outputs WHERE artifact_id = $1`, quiz.ArtifactID).Scan(&outputID); err != nil {
		t.Fatal(err)
	}
	if status, body := s.as(testpostgres.Bob, "DELETE", "/api/v1/notebooks/10/studio/outputs/"+strconv.FormatInt(outputID, 10)); status != 404 {
		t.Errorf("his delete of hers: %d %s", status, body)
	}
	if status, body := s.as(alice, "DELETE", "/api/v1/notebooks/10/studio/outputs/"+strconv.FormatInt(outputID, 10)); status != 204 {
		t.Fatalf("her delete: %d %s", status, body)
	}
	if len(s.removed.removed) != 1 || s.removed.removed[0] != quiz.DocumentID ||
		s.count("documents WHERE id = "+strconv.FormatInt(quiz.DocumentID, 10)+" AND status ->> 'state' = 'deleting'") != 1 {
		t.Errorf("the quiz was not removed with its output: %v", s.removed.removed)
	}
	// An artifact removed elsewhere (its document deleted from the chat's
	// list) takes its output with it.
	testpostgres.Exec(t, s.pool, `DELETE FROM documents WHERE id = `+strconv.FormatInt(report.DocumentID, 10))
	if s.count("notebook_outputs WHERE kind = 'report'") != 0 {
		t.Error("a report whose artifact is gone is still listed")
	}
}

// A chat names a notebook that is not its account's: the artifact is saved
// in the chat, and no account's Studio lists it. The database refuses the
// pairing outright too, whatever a statement asked.
func TestAChatCannotListInAnotherAccountsNotebook(t *testing.T) {
	s := newStudio(t)
	const alice = testpostgres.Alice
	created := s.save(artifactsave.Owner{UserID: alice, ThreadID: 300, NotebookID: 20}, "Hers", deliverables.FormatQuiz)
	if created.ArtifactID == 0 || s.count("notebook_outputs") != 0 {
		t.Fatalf("saved %+v; %d outputs", created, s.count("notebook_outputs"))
	}
	if status, body := s.as(testpostgres.Bob, "GET", "/api/v1/notebooks/20/studio/outputs"); status != 200 || body != "[]\n" && body != "[]" {
		t.Errorf("his Studio: %d %s", status, body)
	}
	_, err := s.pool.Exec(context.Background(), `INSERT INTO notebook_outputs (notebook_id, user_id, kind, title, artifact_id)
VALUES (20, '`+testpostgres.Bob+`', 'quiz', 'Taken', $1)`, created.ArtifactID)
	if err == nil {
		t.Error("an output of his was allowed to name her artifact")
	}
}

// The migration's backfill lists what notebook chats made before it, for the
// account that made it, and nothing twice when it runs again.
func TestTheBackfillListsOldChatOutputsOnce(t *testing.T) {
	s := newStudio(t)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, s.pool, `
INSERT INTO new_chat_threads (id, user_id, title, notebook_id) VALUES (310, '`+bob+`', 'His chat', 20);
INSERT INTO documents (id, user_id, title, document_type, content, content_hash) VALUES
 (600, '`+alice+`', 'Old deck', 'ARTIFACT', '', 'a'), (601, '`+alice+`', 'Old picture', 'ARTIFACT', '', 'b'),
 (602, '`+alice+`', 'Generated', 'ARTIFACT', '', 'c'), (603, '`+alice+`', 'Video file', 'ARTIFACT', '', 'd'),
 (604, '`+alice+`', 'Plain chat page', 'ARTIFACT', '', 'e'), (605, '`+bob+`', 'His map', 'ARTIFACT', '', 'f');
INSERT INTO artifacts (id, document_id, user_id, thread_id, format) VALUES
 (700, 600, '`+alice+`', 300, 'pptx'), (701, 601, '`+alice+`', 300, 'image'),
 (702, 602, '`+alice+`', 301, 'quiz'), (703, 603, '`+alice+`', 300, 'mp4'),
 (704, 604, '`+alice+`', 302, 'html'), (705, 605, '`+bob+`', 310, 'mindmap');
INSERT INTO deliverable_jobs (id, user_id, kind, title, thread_id, tool_call_id, status, artifact_id) VALUES
 (800, '`+alice+`', 'video', 'Old video', 300, 'v1', 'ready', 703),
 (801, '`+alice+`', 'artifact', 'Generated', 301, 'g1', 'ready', 702);
INSERT INTO podcast_runs (id, user_id, title, status, thread_id) VALUES (900, '`+alice+`', 'Old talk', 'ready', 300);
-- The generation's output is listed already, by the Studio.
INSERT INTO notebook_outputs (notebook_id, user_id, kind, title, deliverable_job_id, thread_id) VALUES
 (10, '`+alice+`', 'quiz', 'Generated', 801, 301);`)

	content, err := fs.ReadFile(migrations.Files, "00027_chat_outputs_in_studio.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(content), "-- +goose Down")
	_, rest, found := strings.Cut(up, "-- The backfill")
	if !found {
		t.Fatal("the migration has no backfill section")
	}
	backfill := "-- The backfill" + rest
	for run := 1; run <= 2; run++ {
		if _, err := s.pool.Exec(context.Background(), backfill); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if got := s.count("notebook_outputs"); got != 5 {
			t.Fatalf("run %d: %d outputs, want the generation's and four listed", run, got)
		}
	}
	for what, query := range map[string]string{
		"her deck, as slides":        "notebook_outputs WHERE artifact_id = 700 AND kind = 'slides' AND notebook_id = 10 AND user_id = '" + alice + "' AND thread_id = 300 AND title = 'Old deck'",
		"her video, by its job":      "notebook_outputs WHERE deliverable_job_id = 800 AND kind = 'video' AND artifact_id IS NULL",
		"her podcast, by its run":    "notebook_outputs WHERE podcast_id = 900 AND kind = 'audio'",
		"his map, in his notebook":   "notebook_outputs WHERE artifact_id = 705 AND notebook_id = 20 AND user_id = '" + bob + "'",
		"no picture":                 "notebook_outputs WHERE artifact_id = 701",
		"no plain chat's page":       "notebook_outputs WHERE artifact_id = 704",
		"the video's file not twice": "notebook_outputs WHERE artifact_id = 703",
	} {
		want := 1
		if strings.HasPrefix(what, "no ") || strings.HasPrefix(what, "the video's file") {
			want = 0
		}
		if got := s.count(query); got != want {
			t.Errorf("%s: %d rows, want %d", what, got, want)
		}
	}
	status, body := s.as(alice, "GET", "/api/v1/notebooks/10/studio/outputs")
	if status != 200 || !strings.Contains(body, `"title":"Old deck","status":"ready"`) || !strings.Contains(body, `"artifact_format":"pptx"`) {
		t.Errorf("her Studio after the backfill: %d %s", status, body)
	}
}
