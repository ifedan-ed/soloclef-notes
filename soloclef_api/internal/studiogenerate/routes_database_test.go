package studiogenerate

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_STUDIOGENERATE_DATABASE"

const alice, bob = testpostgres.Alice, testpostgres.Bob

// scripted is a model that plays the specialist's part for a mind map: it
// loads the instructions, saves the map, and answers. It also answers a
// suggestions request, which is the one call that carries a system message
// of this package's own. Every request is kept for the test to read.
type scripted struct {
	mu       sync.Mutex
	requests []model.Request
	// block makes the first specialist call wait for its context to end:
	// a run that is still going when the person stops it.
	block bool
	// fail makes every call fail.
	fail error
	pool string
}

func (s *scripted) Generate(ctx context.Context, request model.Request, _ func(model.Chunk)) (model.Response, error) {
	s.mu.Lock()
	s.requests = append(s.requests, request)
	block, fail, pool := s.block, s.fail, s.pool
	s.mu.Unlock()
	if fail != nil {
		return model.Response{}, fail
	}
	if strings.Contains(request.Messages[0].Content, "Offer ideas they can pick") {
		return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: pool}, FinishReason: "stop"}, nil
	}
	if block {
		<-ctx.Done()
		return model.Response{}, ctx.Err()
	}
	results := 0
	for _, message := range request.Messages {
		if message.Role == model.RoleTool {
			results++
		}
	}
	call := func(id, name, args string) (model.Response, error) {
		return model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: id, Name: name, Args: args}}}, FinishReason: "tool_calls"}, nil
	}
	infographic := false
	for _, message := range request.Messages {
		infographic = infographic || (message.Role == model.RoleUser && strings.Contains(message.Content, "Kind of artifact: infographic."))
	}
	if infographic {
		switch results {
		case 0:
			return call("c1", deliverables.InstructionsTool, `{"artifact_type":"infographic"}`)
		case 1:
			return call("c2", deliverables.SaveTool, `{"format":"infographic","title":"Tides","markdown":`+infographicPlan+`}`)
		}
		return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Made an infographic called Tides."}, FinishReason: "stop"}, nil
	}
	switch results {
	case 0:
		return call("c1", deliverables.InstructionsTool, `{"artifact_type":"mindmap"}`)
	case 1:
		return call("c2", deliverables.SaveTool, `{"format":"mindmap","title":"Tides","markdown":"# Tides\n\n- Moon\n  - Pull\n- Sun\n"}`)
	}
	return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Made a mind map called Tides."}, FinishReason: "stop"}, nil
}

func (s *scripted) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// models hands every account the scripted model.
type models struct{ model *scripted }

func (m models) For(context.Context, string) (model.Model, string, error) {
	return m.model, "test-model", nil
}

// noPodcasts and videoRows stand where the specialist's podcast opener and
// video queuer are; the studio's own audio and video go through the two
// functions and the queuer the routes are given.
type noPodcasts struct{}

func (noPodcasts) Check() error { return nil }
func (noPodcasts) Open(context.Context, deliverables.Owner, deliverables.PodcastRequest) (int64, error) {
	return 0, errors.New("not in this test")
}

// videos queues a video as a row, as the real queuer does, and nothing more.
type videos struct {
	pool    *pgxpool.Pool
	briefs  []deliverables.VideoRequest
	threads []int64
}

func (v *videos) Check() error { return nil }
func (v *videos) Queue(ctx context.Context, owner deliverables.Owner, toolCallID string, request deliverables.VideoRequest) (int64, error) {
	v.briefs, v.threads = append(v.briefs, request), append(v.threads, owner.ThreadID)
	var id int64
	err := v.pool.QueryRow(ctx, `INSERT INTO deliverable_jobs (user_id, kind, title, thread_id, tool_call_id) VALUES ($1, 'video', $2, $3, $4) RETURNING id`,
		owner.UserID, request.Title, owner.ThreadID, toolCallID).Scan(&id)
	return id, err
}

type harness struct {
	t       *testing.T
	pool    *pgxpool.Pool
	api     huma.API
	model   *scripted
	runner  *Runner
	videos  *videos
	painter *painter
	opened  []deliverables.PodcastBrief
	focuses []string
	// languages is each opened podcast's asked language.
	languages []string
	// draftFails makes the podcast's draft fail to queue.
	draftFails bool
	stop       context.CancelFunc
	// routes is the routes as mounted, for the seam other packages call
	// them through (StartFromReport).
	routes Routes
}

func newHarness(t *testing.T, sandbox bool) *harness {
	pool := testpostgres.Open(t, databaseVariable)
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title) VALUES (10, '`+alice+`', 'Tides'), (20, '`+bob+`', 'His');
INSERT INTO folders (id, user_id, name, position) VALUES (5, '`+alice+`', 'Papers', 'a0');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, folder_id, status) VALUES
 (100, '`+alice+`', 'Moon', 'NOTE', 'The moon pulls the sea.', 'h100', NULL, '{"state":"ready"}'),
 (101, '`+alice+`', 'Sun', 'NOTE', 'The sun pulls less.', 'h101', 5, '{"state":"ready"}'),
 (102, '`+alice+`', 'Going', 'NOTE', 'Being deleted.', 'h102', 5, '{"state":"deleting"}'),
 (200, '`+bob+`', 'Secret', 'NOTE', 'BOBS SECRET TEXT', 'h200', NULL, '{"state":"ready"}');
INSERT INTO notebook_sources (id, notebook_id, user_id, document_id, folder_id, enabled) VALUES
 (1, 10, '`+alice+`', 100, NULL, true),
 (2, 10, '`+alice+`', NULL, 5, false),
 (3, 20, '`+bob+`', 200, NULL, true);`)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	local, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, pool: pool, model: &scripted{pool: `{"ideas":["moon's pull","sun's pull","spring tides","neap tides"],"examples":["explain it for sailors"]}`}, videos: &videos{pool: pool}, painter: &painter{}}
	specialist := deliverables.Specialist{
		Store: checkpoint.NewMemory(), Limits: deliverables.Limits{MaxSteps: 8, Timeout: time.Minute},
		Images: h.painter, Reader: deliverables.NoImageReader{}, Videos: h.videos,
		Workshop: deliverables.NoSandbox{}, Podcasts: noPodcasts{},
		Saver: deliverables.Saver{
			Rows: deliverables.Store{Pool: pool}, Files: local, Logger: quiet,
			NewID: func() string { return "00000000000000000000000000000abc" },
			Index: func(context.Context, int64, string) error { return nil },
		},
	}
	lifetime, stop := context.WithCancel(context.Background())
	h.stop = stop
	t.Cleanup(func() { stop(); h.runner.Wait() })
	if h.runner, err = NewRunner(pool, models{h.model}, specialist, 2, lifetime, quiet); err != nil {
		t.Fatal(err)
	}
	h.runner.beatEvery = 20 * time.Millisecond
	routes := Routes{
		Pool: pool, Models: models{h.model}, Runner: h.runner, Videos: h.videos, SandboxEnabled: sandbox,
		// A deployment with an image model, as every test here written
		// before the image gate assumed; TestWhatADeploymentCanMake is the
		// one that tries it without.
		ImageEnabled: true,
		OpenPodcast: func(ctx context.Context, owner deliverables.Owner, brief deliverables.PodcastBrief, request deliverables.PodcastRequest) (int64, error) {
			h.opened, h.focuses = append(h.opened, brief), append(h.focuses, request.Focus+"|"+request.Source)
			h.languages = append(h.languages, request.Language)
			var id int64
			err := pool.QueryRow(ctx, `INSERT INTO podcast_runs (user_id, title, status, thread_id) VALUES ($1, $2, 'awaiting_brief', $3) RETURNING id`,
				owner.UserID, request.Title, owner.ThreadID).Scan(&id)
			return id, err
		},
		StartDraft: func(ctx context.Context, userID string, podcastID int64, _ string) error {
			if h.draftFails {
				return errors.New("the queue is down")
			}
			_, err := pool.Exec(ctx, `UPDATE podcast_runs SET status = 'drafting' WHERE id = $1 AND user_id = $2`, podcastID, userID)
			return err
		},
		Errors: errorenvelope.Writer{}, Logger: quiet,
	}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	h.api = humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Register(h.api)
	h.routes = routes
	return h
}

func (h *harness) as(userID, method, path, body string) (int, string) {
	h.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: userID, Method: principal.MethodSession}))
	recorder := httptest.NewRecorder()
	h.api.Adapter().ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

func (h *harness) count(query string) (n int) {
	h.t.Helper()
	if err := h.pool.QueryRow(context.Background(), "SELECT count(*) FROM "+query).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// until waits for a condition a background run brings about.
func (h *harness) until(what, query string) {
	h.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if h.count(query) > 0 {
			return
		}
	}
	h.t.Fatalf("%s: never true: %s", what, query)
}

func expect(t *testing.T, name string, status int, body string, want int, says ...string) {
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

const generate = "/api/v1/notebooks/10/studio/generate"

func TestAnArtifactIsMadeFromTheCallersOwnSourcesAlone(t *testing.T) {
	h := newHarness(t, false)

	status, body := h.as(bob, "POST", generate, `{"kind":"mindmap"}`)
	expect(t, "her notebook asked by him", status, body, 404, notebookAbsent)
	status, body = h.as(alice, "POST", generate, `{"kind":"mindmap","source_ids":[3]}`)
	expect(t, "his source named in her notebook", status, body, 404, sourceAbsent)
	status, body = h.as(alice, "POST", generate, `{"kind":"mindmap","source_ids":[1,999]}`)
	expect(t, "a source that does not exist beside one that does", status, body, 404, sourceAbsent)
	status, body = h.as(alice, "POST", generate, `{"kind":"mindmap","source_ids":[]}`)
	expect(t, "no sources", status, body, 400, noSources)
	status, body = h.as(alice, "POST", generate, `{"kind":"report"}`)
	expect(t, "a built kind with no sandbox", status, body, 503, notEnabled)
	status, body = h.as(alice, "POST", generate, `{"kind":"mindmap","detail":"vast"}`)
	expect(t, "a preset outside its vocabulary", status, body, 422, "detail")
	status, body = h.as(alice, "POST", "/api/v1/notebooks/ten/studio/generate", `{"kind":"mindmap"}`)
	expect(t, "an id that is not a number", status, body, 422, "notebook_id")
	if h.count("notebook_outputs") != 0 || h.count("deliverable_jobs") != 0 || h.count("new_chat_threads") != 0 || h.model.calls() != 0 {
		t.Fatal("a refused request wrote a row or asked the model")
	}

	// Her ticked source (document 100) and nothing else.
	status, body = h.as(alice, "POST", generate, `{"kind":"mindmap","request_id":"click-1","focus":"spring tides"}`)
	expect(t, "her mind map", status, body, 201, `"kind":"mindmap"`, `"title":"Mind Map: Tides"`, `"source_count":1`,
		`"generated_by":{"model":"test-model"}`, `"focus":"spring tides"`, `"detail":"standard"`)
	h.until("the run finishes", "deliverable_jobs WHERE kind = 'artifact' AND status = 'ready' AND artifact_id IS NOT NULL AND progress = 100 AND finished_at IS NOT NULL")
	if h.count(`artifacts a JOIN deliverable_jobs j ON j.artifact_id = a.id JOIN new_chat_threads t ON t.id = a.thread_id
		WHERE a.user_id = '`+alice+`' AND a.format = 'mindmap' AND t.source = 'artifact_api' AND t.notebook_id = 10 AND j.thread_id = t.id`) != 1 {
		t.Error("the artifact should be hers, a mind map, saved under the generation's own hidden thread")
	}
	if h.count("artifacts WHERE user_id = '"+bob+"'") != 0 {
		t.Error("an artifact was made for him")
	}
	task := h.model.requests[0].Messages[len(h.model.requests[0].Messages)-1].Content
	for _, piece := range []string{"Kind of artifact: mindmap.", "## Source: Moon", "The moon pulls the sea.", "Focus: spring tides"} {
		if !strings.Contains(task, piece) {
			t.Errorf("the task lacks %q", piece)
		}
	}
	for _, absent := range []string{"BOBS SECRET TEXT", "The sun pulls less.", "Being deleted."} {
		if strings.Contains(task, absent) {
			t.Errorf("the task holds %q, which is not one of her ticked sources", absent)
		}
	}

	// The same click again is the same output, and nothing new is made.
	status, again := h.as(alice, "POST", generate, `{"kind":"mindmap","request_id":"click-1"}`)
	expect(t, "the same click", status, again, 201, `"status":"ready"`)
	if h.count("notebook_outputs") != 1 || h.count("deliverable_jobs") != 1 {
		t.Error("a repeated click made a second output or job")
	}
	if id := body[:strings.Index(body, ",")]; !strings.HasPrefix(again, id+",") {
		t.Errorf("the repeated click answered another output: %s then %s", body, again)
	}

	// A folder source she names reaches the folder's documents, but not
	// one being deleted.
	status, body = h.as(alice, "POST", generate, `{"kind":"mindmap","source_ids":[2]}`)
	expect(t, "her folder source", status, body, 201, `"source_count":1`)
	h.until("the second run finishes", "(SELECT 1 FROM deliverable_jobs WHERE kind = 'artifact' AND status = 'ready' HAVING count(*) = 2) AS finished")
	if h.count("notebook_outputs WHERE source_document_ids = '[101]'") != 1 {
		t.Error("the folder source should have reached document 101 alone")
	}
	if h.count("notebooks WHERE id = 10 AND updated_at > created_at") != 1 || h.count("notebooks WHERE id = 20 AND updated_at > created_at") != 0 {
		t.Error("generating should move her notebook to the top and leave his alone")
	}
}

func TestAudioAndVideoGoThroughTheChatsOwnMakers(t *testing.T) {
	h := newHarness(t, false)

	audio := `{"kind":"audio","format":"brief","length":"short","focus":"the moon","language":"es","request_id":"a1"}`
	status, body := h.as(alice, "POST", generate, audio)
	expect(t, "her audio overview", status, body, 201, `"kind":"audio"`, `"status":"running"`, `"phase":"drafting"`, `"format":"brief"`)
	// The same click again is the same output: no second podcast is
	// opened, so nothing is drafted or voiced twice. (An audio overview
	// has no job to carry the click's id; the output keeps it.)
	status, again := h.as(alice, "POST", generate, audio)
	if status != 201 || again != body || len(h.opened) != 1 || h.count("podcast_runs") != 1 {
		t.Errorf("the same audio click again: %d %s, %d podcast(s) opened", status, again, len(h.opened))
	}
	// And the database holds one output per click whatever the route does.
	if _, err := h.pool.Exec(context.Background(), `INSERT INTO notebook_outputs (notebook_id, user_id, kind, title, request_key)
		SELECT notebook_id, user_id, kind, title, request_key FROM notebook_outputs WHERE request_key IS NOT NULL`); err == nil {
		t.Error("a second output with the same click's key was stored")
	}
	if len(h.opened) != 1 || h.opened[0].Speakers != 1 || h.opened[0].MinSeconds != 60 || h.opened[0].MaxSeconds != 150 {
		t.Errorf("the podcast was opened as %+v", h.opened)
	}
	if !strings.HasPrefix(h.focuses[0], "Keep it a brief, focused overview. the moon|## Source: Moon\n\nThe moon pulls the sea.") {
		t.Errorf("the podcast's focus and source: %q", h.focuses[0])
	}
	// The dialog's language reaches the opener, which casts that
	// language's voices (deliverables' own test of the brief).
	if len(h.languages) != 1 || h.languages[0] != "es" {
		t.Errorf("the podcast was asked for in %q", h.languages)
	}
	if h.count("notebook_outputs o JOIN podcast_runs p ON p.id = o.podcast_id WHERE o.user_id = '"+alice+"' AND p.status = 'drafting' AND o.deliverable_job_id IS NULL") != 1 {
		t.Error("the output should point at the podcast, now drafting")
	}

	// A draft that cannot be queued leaves no output and no podcast
	// waiting at a gate nobody will open.
	h.draftFails = true
	status, body = h.as(alice, "POST", generate, `{"kind":"audio"}`)
	expect(t, "audio with the queue down", status, body, 503, "could not be queued")
	if h.count("notebook_outputs") != 1 || h.count("podcast_runs WHERE status = 'cancelled'") != 1 || h.count("podcast_runs WHERE status = 'awaiting_brief'") != 0 {
		t.Error("the unqueued podcast should be closed and not listed")
	}

	status, body = h.as(alice, "POST", generate, `{"kind":"video","format":"short","visual_style":"dark","request_id":"v1"}`)
	expect(t, "her video", status, body, 201, `"kind":"video"`, `"status":"queued"`, `"visual_style":"dark"`)
	if len(h.videos.briefs) != 1 {
		t.Fatalf("queued %d videos", len(h.videos.briefs))
	}
	queued := h.videos.briefs[0]
	if strings.Contains(queued.Brief, "\n") || !strings.Contains(queued.Brief, "Format: Short") || !strings.Contains(queued.Brief, "Visual style: Dark") ||
		queued.Material != "## Source: Moon\n\nThe moon pulls the sea." || queued.Title != "Video Overview: Tides" {
		t.Errorf("the queued video: %+v", queued)
	}
	if h.count("new_chat_threads WHERE id = "+strconv.FormatInt(h.videos.threads[0], 10)+" AND user_id = '"+alice+"' AND source = 'artifact_api'") != 1 {
		t.Error("the video should be queued under a hidden thread of hers")
	}
	status, body = h.as(alice, "POST", generate, `{"kind":"video","request_id":"v1"}`)
	expect(t, "the same video click", status, body, 201, `"kind":"video"`)
	if len(h.videos.briefs) != 1 || h.count("notebook_outputs WHERE kind = 'video'") != 1 {
		t.Error("a repeated click queued a second video")
	}
	status, body = h.as(bob, "POST", generate, `{"kind":"video"}`)
	expect(t, "her notebook's video asked by him", status, body, 404, notebookAbsent)
}

func TestARunStopsWhenAskedAndFailsHonestly(t *testing.T) {
	h := newHarness(t, false)

	// A run still going when the person deletes or cancels the output.
	h.model.block = true
	status, body := h.as(alice, "POST", generate, `{"kind":"mindmap"}`)
	expect(t, "a run that will be stopped", status, body, 201)
	h.until("the run is claimed", "deliverable_jobs WHERE status = 'running' AND claimed_at IS NOT NULL")
	testpostgres.Exec(t, h.pool, `UPDATE deliverable_jobs SET status = 'cancelling'`)
	h.until("the run stops", "deliverable_jobs WHERE status = 'cancelled' AND failure_code = 'cancelled' AND finished_at IS NOT NULL")

	// A model that fails is a failed job that says why, never a made-up
	// artifact.
	h.model.block, h.model.fail = false, errors.New("the proxy answered 500")
	status, body = h.as(alice, "POST", generate, `{"kind":"mindmap"}`)
	expect(t, "a run whose model fails", status, body, 201)
	h.until("the run fails", "deliverable_jobs WHERE status = 'failed' AND failure_code = 'generation_failed' AND internal_error LIKE '%proxy answered 500%'")
	if h.count("artifacts") != 0 {
		t.Error("a failed run saved an artifact")
	}

	// A job whose process died is failed by the sweep; a live one is not.
	now := time.Now().UTC()
	testpostgres.Exec(t, h.pool, `
INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, status, heartbeat_at, updated_at) VALUES
 (900, '`+alice+`', 'artifact', 'Dead', 'dead', 'running', now() - interval '10 minutes', now() - interval '10 minutes'),
 (901, '`+alice+`', 'artifact', 'Alive', 'alive', 'running', now(), now()),
 (902, '`+alice+`', 'video', 'Workers', 'video', 'running', now() - interval '10 minutes', now() - interval '10 minutes'),
 (903, '`+alice+`', 'artifact', 'Stopping', 'stopping', 'cancelling', now() - interval '10 minutes', now() - interval '10 minutes')`)
	failed, err := New(h.pool).FailAbandonedJobs(context.Background(), FailAbandonedJobsParams{Now: now, SilentSince: now.Add(-abandonedAfter)})
	if err != nil || failed != 2 {
		t.Fatalf("the sweep closed %d jobs, %v", failed, err)
	}
	if h.count("deliverable_jobs WHERE id = 900 AND status = 'failed' AND failure_code = 'interrupted'") != 1 ||
		h.count("deliverable_jobs WHERE id = 903 AND status = 'cancelled'") != 1 ||
		h.count("deliverable_jobs WHERE id IN (901, 902) AND status = 'running'") != 2 {
		t.Error("the sweep should fail the silent artifact job, close the one being stopped, and leave a live one and a worker's video alone")
	}
}

func TestSuggestionsAreWrittenOncePerSourceSetAndAreTheOwnersAlone(t *testing.T) {
	h := newHarness(t, false)
	const path = "/api/v1/notebooks/10/studio/suggestions"

	status, body := h.as(bob, "GET", path+"?kind=quiz", "")
	expect(t, "her notebook's ideas asked by him", status, body, 404, notebookAbsent)

	status, body = h.as(alice, "GET", path+"?kind=audio&variant=debate&language=fr", "")
	expect(t, "her ideas", status, body, 200, `"suggestions":["Moon's pull","Sun's pull","Spring tides"]`, `"placeholder":"Explain it for sailors"`, `"Neap tides"`)
	if h.model.calls() != 1 {
		t.Fatalf("%d model calls", h.model.calls())
	}
	asked := h.model.requests[0]
	if !strings.Contains(asked.Messages[0].Content, "debate") || !strings.Contains(asked.Messages[0].Content, "the code fr") ||
		!strings.Contains(asked.Messages[1].Content, "The moon pulls the sea.") || strings.Contains(asked.Messages[1].Content, "BOBS SECRET TEXT") {
		t.Errorf("the model was asked: %+v", asked.Messages)
	}

	// Asked again: the stored pool, and no model call. Another format is
	// another pool.
	status, body = h.as(alice, "GET", path+"?kind=audio&variant=debate&language=fr", "")
	expect(t, "her ideas again", status, body, 200, `"Moon's pull"`)
	if h.model.calls() != 1 {
		t.Errorf("a cached pool asked the model again")
	}
	h.as(alice, "GET", path+"?kind=audio&variant=brief&language=fr", "")
	if h.model.calls() != 2 {
		t.Errorf("another format should be another pool")
	}

	// "More ideas" asks for ones unlike those seen.
	h.model.pool = `{"ideas":["tidal bores","amphidromic points","tide tables"],"examples":["for surfers"]}`
	status, body = h.as(alice, "GET", path+"?kind=audio&variant=debate&language=fr&refresh=true", "")
	expect(t, "more ideas", status, body, 200, `"Tidal bores"`)
	if last := h.model.requests[len(h.model.requests)-1].Messages[0].Content; !strings.Contains(last, "already seen these; offer different ones: Moon's pull") {
		t.Errorf("a refresh should name what was seen: %s", last)
	}

	// A model that is away shows what there was; with nothing stored, no
	// ideas, and never an error in a dialog that works without them.
	h.model.fail = errors.New("away")
	status, body = h.as(alice, "GET", path+"?kind=audio&variant=debate&language=fr&refresh=true", "")
	expect(t, "a refresh with the model away", status, body, 200, `"Tidal bores"`)
	status, body = h.as(alice, "GET", path+"?kind=quiz", "")
	expect(t, "a new dialog with the model away", status, body, 200, `"suggestions":[]`, `"placeholder":null`)
	h.model.fail = nil

	// A changed source list drops every pool.
	testpostgres.Exec(t, h.pool, `UPDATE notebook_sources SET enabled = true WHERE id = 2`)
	before := h.model.calls()
	h.as(alice, "GET", path+"?kind=audio&variant=debate&language=fr", "")
	if h.model.calls() != before+1 || h.count(`notebooks WHERE id = 10 AND (SELECT count(*) FROM jsonb_object_keys(focus_suggestions)) = 1`) != 1 {
		t.Error("a changed source set should write a new pool and drop the old ones")
	}
	if h.count("notebooks WHERE id = 20 AND focus_suggestions IS NOT NULL") != 0 {
		t.Error("his notebook was given her pools")
	}

	// No ticked source: nothing to have ideas about, and no model call.
	testpostgres.Exec(t, h.pool, `UPDATE notebook_sources SET enabled = false WHERE notebook_id = 10`)
	before = h.model.calls()
	status, body = h.as(alice, "GET", path+"?kind=quiz", "")
	expect(t, "no ticked source", status, body, 200, `"suggestions":[]`)
	if h.model.calls() != before {
		t.Error("the model was asked about no sources")
	}
}
