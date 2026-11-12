package automations

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"git.danvics.com/danvics/soloclef/soloclef_automations/runqueue"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

const databaseVariable = "SOLOCLEF_API_TEST_AUTOMATIONS_DATABASE"

// published records the messages the routes enqueue; down makes the broker
// refuse them.
type published struct {
	mu       sync.Mutex
	messages []produce.Message
	down     bool
}

func (p *published) Enqueue(_ context.Context, message produce.Message) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		return "", errors.New("the broker is down")
	}
	p.messages = append(p.messages, message)
	return message.ID, nil
}

// menu is the model menu: the model that would answer, offered or not,
// and the names the proxy lists. down is a proxy that cannot be asked.
type menu struct {
	offered bool
	listed  []string
	down    bool
}

func (m menu) Look(context.Context, string) (assembly.Chosen, error) {
	return assembly.Chosen{Name: "chat-model", Offered: m.offered}, nil
}

func (m menu) NotOffered(_ context.Context, _ string, names []string) ([]string, error) {
	if len(names) > 0 && m.down {
		return nil, &assembly.ModelError{Unreachable: true, Message: assembly.ModelsUnreachable}
	}
	var withdrawn []string
	for _, name := range names {
		if !slices.Contains(m.listed, name) {
			withdrawn = append(withdrawn, name)
		}
	}
	return withdrawn, nil
}

type harness struct {
	t     *testing.T
	api   huma.API
	queue *published
}

func (h harness) call(userID, method, path, body string) (int, string) {
	h.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: userID, Method: principal.MethodSession}))
	recorder := httptest.NewRecorder()
	// The body reaches a route the way cmd/api's reader hands it over.
	requestbody.Middleware{
		Covers: func(r *http.Request) bool { return r.Method == http.MethodPost || r.Method == http.MethodPatch },
	}.Wrap(h.api.Adapter()).ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// made is the id in a response that carries one.
func (h harness) made(status int, body string, want int) int64 {
	h.t.Helper()
	var out struct {
		ID int64 `json:"id"`
	}
	if status != want || json.Unmarshal([]byte(body), &out) != nil || out.ID == 0 {
		h.t.Fatalf("%d %s, want %d and an id", status, body, want)
	}
	return out.ID
}

const digest = `{"name":"Digest","definition":{"name":"Digest","plan":[{"step_id":"digest","action":"agent_task","params":{"query":"Summarise what arrived."}}]},
 "triggers":[{"type":"schedule","params":{"cron":"0 9 * * *","timezone":"UTC"}}]}`

func TestAutomationsAreTheirOwnersAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	queue := &published{}
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes := Routes{Pool: pool, Queue: queue, Models: menu{offered: true}, Errors: errorenvelope.Writer{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
	if err := routes.Check(); err != nil {
		t.Fatal(err)
	}
	routes.Register(api)
	h := harness{t: t, api: api, queue: queue}
	expect := func(name string, status int, body string, want int, says ...string) {
		t.Helper()
		if status != want {
			t.Errorf("%s: %d %s, want %d", name, status, body, want)
		}
		for _, piece := range says {
			if !strings.Contains(body, piece) {
				t.Errorf("%s: %s lacks %s", name, body, piece)
			}
		}
	}
	one := func(id int64) string { return ListPath + "/" + strconv.FormatInt(id, 10) }

	// Alice saves an automation with a schedule; the trigger's next fire is
	// worked out from now, and the definition comes back with its defaults.
	status, body := h.call(alice, "POST", ListPath, digest)
	id := h.made(status, body, http.StatusCreated)
	expect("create", status, body, http.StatusCreated, `"status":"active"`, `"version":1`, `"concurrency":"drop_if_running"`,
		`"next_fire_at":"2026-11-12T09:00:00Z"`, `"type":"schedule"`)
	var created struct {
		Triggers []struct {
			ID int64 `json:"id"`
		} `json:"triggers"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || len(created.Triggers) != 1 {
		t.Fatalf("triggers in %s", body)
	}
	triggerPath := one(id) + "/triggers/" + strconv.FormatInt(created.Triggers[0].ID, 10)

	// Alice starts a run, so there is one to ask Bob about.
	status, body = h.call(alice, "POST", one(id)+"/run", "")
	runID := h.made(status, body, http.StatusAccepted)
	expect("run now", status, body, http.StatusAccepted, `"status":"pending"`, `"trigger_id":null`)
	if len(queue.messages) != 1 || queue.messages[0].Task != runqueue.ExecuteTask || queue.messages[0].Kwargs[runqueue.RunKey] != runID {
		t.Fatalf("published %+v, want the one run", queue.messages)
	}
	runPath := one(id) + "/runs/" + strconv.FormatInt(runID, 10)

	// Every route, as Bob, against Alice's automation, trigger and run:
	// each is the 404 of a thing that was never made, and nothing changes.
	for _, c := range []struct{ method, path, body, says string }{
		{"GET", one(id), "", automationAbsent},
		{"PATCH", one(id), `{"name":"Mine now"}`, automationAbsent},
		{"PATCH", one(id), `{"status":"paused"}`, automationAbsent},
		{"DELETE", one(id), "", automationAbsent},
		{"POST", one(id) + "/run", "", automationAbsent},
		{"POST", one(id) + "/triggers", `{"type":"schedule","params":{"cron":"* * * * *","timezone":"UTC"}}`, automationAbsent},
		{"PATCH", triggerPath, `{"enabled":false}`, triggerAbsent},
		{"DELETE", triggerPath, "", triggerAbsent},
		{"GET", one(id) + "/runs", "", automationAbsent},
		{"GET", runPath, "", runAbsent},
		{"POST", runPath + "/cancel", "", runAbsent},
	} {
		status, body := h.call(bob, c.method, c.path, c.body)
		expect("bob "+c.method+" "+c.path, status, body, http.StatusNotFound, c.says)
	}
	status, body = h.call(bob, "GET", ListPath, "")
	expect("bob's list", status, body, http.StatusOK, `"items":[]`, `"total":0`)
	// A trigger and a run are found only under their own automation, even
	// by their owner: Bob's automation does not reach Alice's rows either.
	status, body = h.call(bob, "POST", ListPath, digest)
	bobs := h.made(status, body, http.StatusCreated)
	status, body = h.call(bob, "PATCH", one(bobs)+"/triggers/"+strconv.FormatInt(created.Triggers[0].ID, 10), `{"enabled":false}`)
	expect("alice's trigger under bob's automation", status, body, http.StatusNotFound, triggerAbsent)
	status, body = h.call(bob, "POST", one(bobs)+"/runs/"+strconv.FormatInt(runID, 10)+"/cancel", "")
	expect("alice's run under bob's automation", status, body, http.StatusNotFound, runAbsent)

	// Alice's rows are as she left them.
	status, body = h.call(alice, "GET", one(id), "")
	expect("alice reads", status, body, http.StatusOK, `"name":"Digest"`, `"status":"active"`, `"enabled":true`)
	status, body = h.call(alice, "GET", runPath, "")
	expect("alice's run", status, body, http.StatusOK, `"status":"pending"`, `"step_results":[]`, `"output":null`, `"started_by":"hand"`)
	status, body = h.call(alice, "GET", ListPath, "")
	expect("alice's list", status, body, http.StatusOK, `"total":1`)

	// A second run is skipped while one waits: the automation drops it.
	status, body = h.call(alice, "POST", one(id)+"/run", "")
	expect("a second run", status, body, http.StatusConflict, "already has a run")

	// A trigger switched off waits for nothing; switched on again it waits
	// for the next time from now. New timing is checked like a new trigger.
	status, body = h.call(alice, "PATCH", triggerPath, `{"enabled":false}`)
	expect("switch off", status, body, http.StatusOK, `"enabled":false`, `"next_fire_at":null`)
	status, body = h.call(alice, "PATCH", triggerPath, `{"enabled":true,"params":{"cron":"30 10 * * *","timezone":"UTC"}}`)
	expect("switch on with new timing", status, body, http.StatusOK, `"next_fire_at":"2026-11-12T10:30:00Z"`)
	status, body = h.call(alice, "PATCH", triggerPath, `{"params":{"cron":"nonsense","timezone":"UTC"}}`)
	expect("bad timing", status, body, http.StatusUnprocessableEntity, "five fields")
	status, body = h.call(alice, "POST", one(id)+"/triggers", `{"type":"event","params":{"event_type":"document.entered_folder","filter":{"folder_id":7}}}`)
	expect("an event trigger", status, body, http.StatusCreated, `"type":"event"`, `"next_fire_at":null`)
	status, body = h.call(alice, "POST", one(id)+"/triggers", `{"type":"manual","params":{}}`)
	expect("a manual trigger", status, body, http.StatusUnprocessableEntity, "unknown trigger type")

	// An edit of the definition moves the version; a rename does not.
	status, body = h.call(alice, "PATCH", one(id), `{"name":"Morning digest"}`)
	expect("rename", status, body, http.StatusOK, `"name":"Morning digest"`, `"version":1`)
	status, body = h.call(alice, "PATCH", one(id), `{"definition":{"name":"Digest","plan":[{"step_id":"digest","action":"agent_task","params":{"query":"Shorter."}}]}}`)
	expect("edit", status, body, http.StatusOK, `"version":2`, `Shorter.`)
	status, body = h.call(alice, "PATCH", one(id), `{"definition":{"name":"Digest","plan":[{"step_id":"x","action":"send_email","params":{}}]}}`)
	expect("an action nothing runs", status, body, http.StatusUnprocessableEntity, "agent_task")
	status, body = h.call(alice, "PATCH", one(id), `{"colour":"red"}`)
	expect("an unknown field", status, body, http.StatusUnprocessableEntity, "colour")

	// Pausing cancels the waiting run, and a paused automation is not run.
	status, body = h.call(alice, "PATCH", one(id), `{"status":"paused"}`)
	expect("pause", status, body, http.StatusOK, `"status":"paused"`)
	status, body = h.call(alice, "GET", runPath, "")
	expect("the run after pausing", status, body, http.StatusOK, `"status":"cancelled"`, "The automation was paused.")
	status, body = h.call(alice, "POST", one(id)+"/run", "")
	expect("run while paused", status, body, http.StatusConflict, "paused")
	status, body = h.call(alice, "POST", runPath+"/cancel", "")
	expect("cancel a finished run", status, body, http.StatusConflict, runFinished)

	// Resumed, a run is created even when the broker is down: the row is
	// the outbox and the worker's tick publishes it.
	h.call(alice, "PATCH", one(id), `{"status":"active"}`)
	queue.down = true
	status, body = h.call(alice, "POST", one(id)+"/run", "")
	second := h.made(status, body, http.StatusAccepted)
	queue.down = false
	status, body = h.call(alice, "POST", one(id)+"/runs/"+strconv.FormatInt(second, 10)+"/cancel", "")
	expect("cancel a waiting run", status, body, http.StatusOK, `"status":"cancelled"`)
	status, body = h.call(alice, "GET", one(id)+"/runs?limit=1", "")
	expect("runs, newest first", status, body, http.StatusOK, `"total":2`, `"id":`+strconv.FormatInt(second, 10))
	status, body = h.call(alice, "GET", one(id)+"/runs?limit=0", "")
	expect("a bad page size", status, body, http.StatusUnprocessableEntity)

	// Deleting takes the triggers and runs with it, and only Alice's.
	status, body = h.call(alice, "DELETE", one(id), "")
	expect("delete", status, body, http.StatusNoContent)
	status, body = h.call(alice, "GET", one(id), "")
	expect("after delete", status, body, http.StatusNotFound, automationAbsent)
	var left int
	if err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM automation_triggers) + (SELECT count(*) FROM automation_runs)`).Scan(&left); err != nil || left != 1 {
		t.Errorf("%d triggers and runs are left, want Bob's one trigger (%v)", left, err)
	}
}

func TestWhatIsRefusedBeforeItIsSaved(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Pool: pool, Queue: &published{}, Models: menu{offered: false}, Errors: errorenvelope.Writer{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now}.Register(api)
	h := harness{t: t, api: api}
	for _, c := range []struct{ name, body, says string }{
		{"no body", ``, "could not be read"},
		{"no name", `{"definition":{"name":"n","plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}]}}`, "name must be"},
		{"no plan", `{"name":"n","definition":{"name":"n","plan":[]}}`, "at least one step"},
		{"a notebook nobody has", `{"name":"n","definition":{"name":"n","plan":[{"step_id":"a","action":"agent_task","params":{"query":"q","notebook_id":4}}]}}`, "notebook 4 is not one of yours"},
		{"an emailed result where no mail is sent", `{"name":"n","definition":{"name":"n","execution":{"email_result":true},"plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}]}}`, "not set up to send mail"},
		{"the old model ids", `{"name":"n","definition":{"name":"n","models":{"chat_model_id":0},"plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}]}}`, "chat_model_id"},
		{"a bad trigger", `{"name":"n","definition":{"name":"n","plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}]},"triggers":[{"type":"schedule","params":{"cron":"x","timezone":"UTC"}}]}`, "triggers[0]"},
		{"a workspace", `{"workspace_id":1,"name":"n","definition":{"name":"n","plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}]}}`, "workspace_id"},
	} {
		status, body := h.call(testpostgres.Alice, "POST", ListPath, c.body)
		if status != http.StatusUnprocessableEntity || !strings.Contains(body, c.says) {
			t.Errorf("%s: %d %s, want 422 saying %q", c.name, status, body, c.says)
		}
	}
	var saved int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM automations`).Scan(&saved); err != nil || saved != 0 {
		t.Errorf("%d automations were saved by refused requests (%v)", saved, err)
	}
	// The account's chat model is not offered, and the page is told so in
	// the words the chat would use.
	status, body := h.call(testpostgres.Alice, "GET", EligibilityPath, "")
	if status != http.StatusOK || !strings.Contains(body, `"allowed":false`) || !strings.Contains(body, "chat-model") {
		t.Errorf("eligibility: %d %s", status, body)
	}
	// A path that names no row is not found, not a server error.
	for _, path := range []string{ListPath + "/abc", ListPath + "/0", ListPath + "/1/runs/x"} {
		if status, body := h.call(testpostgres.Alice, "GET", path, ""); status != http.StatusNotFound {
			t.Errorf("GET %s: %d %s", path, status, body)
		}
	}
}

// What a definition names is the saver's own, or is not saved: a notebook,
// a connected account and a model are each checked, on a create and on an
// edit, and another account's is refused in the words one that never
// existed is.
func TestADefinitionMayOnlyNameWhatIsTheSaversOwn(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	ctx := context.Background()
	for _, statement := range []string{
		`INSERT INTO notebooks (id, user_id, title) VALUES (11, $1, 'Alice''s'), (22, $2, 'Bob''s')`,
		`INSERT INTO search_source_connectors (id, user_id, name, connector_type, config) VALUES
		  (31, $1, 'Email (IMAP)', 'EMAIL_IMAP_CONNECTOR', '{}'), (42, $2, 'Email (IMAP)', 'EMAIL_IMAP_CONNECTOR', '{}')`,
	} {
		if _, err := pool.Exec(ctx, statement, alice, bob); err != nil {
			t.Fatal(err)
		}
	}
	build := func(models menu, sendsMail bool) harness {
		api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
		Routes{Pool: pool, Queue: &published{}, Models: models, SendsMail: sendsMail, Errors: errorenvelope.Writer{},
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now}.Register(api)
		return harness{t: t, api: api}
	}
	h := build(menu{offered: true, listed: []string{"model-a", "model-b"}}, true)
	definition := func(extra, params string) string {
		return `{"name":"n"` + extra + `,"plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"` + params + `}}]}`
	}
	create := func(extra, params string) string {
		return `{"name":"n","definition":` + definition(extra, params) + `}`
	}

	// Everything Alice may name, named at once, is saved as she wrote it.
	status, body := h.call(alice, "POST", ListPath, create(`,"models":{"chat":"model-a"},"execution":{"email_result":true}`,
		`,"save_to_notebook_id":11,"retrieval_scope":"all","mentioned_connector_ids":[31],"model":"model-b"`))
	id := h.made(status, body, http.StatusCreated)
	for _, piece := range []string{`"models":{"chat":"model-a"}`, `"email_result":true`, `"save_to_notebook_id":11`, `"mentioned_connector_ids":[31]`, `"model":"model-b"`} {
		if !strings.Contains(body, piece) {
			t.Errorf("the saved definition lacks %s: %s", piece, body)
		}
	}
	status, body = h.call(alice, "POST", ListPath, create(``, `,"notebook_id":11`))
	h.made(status, body, http.StatusCreated)

	one := ListPath + "/" + strconv.FormatInt(id, 10)
	for _, c := range []struct{ name, extra, params, says string }{
		{"Bob's notebook to read", ``, `,"notebook_id":22`, "notebook 22 is not one of yours"},
		{"Bob's notebook to save to", ``, `,"save_to_notebook_id":22`, "notebook 22 is not one of yours"},
		{"Bob's connected account", ``, `,"mentioned_connector_ids":[31,42]`, "connected account 42 is not one of yours"},
		{"a model the proxy does not offer", `,"models":{"chat":"model-z"}`, ``, `model-z\" is not offered; choose one from the list`},
		{"a step's model the proxy does not offer", ``, `,"model":"model-z"`, `model-z\" is not offered; choose one from the list`},
	} {
		status, body := h.call(alice, "POST", ListPath, create(c.extra, c.params))
		if status != http.StatusUnprocessableEntity || !strings.Contains(body, c.says) {
			t.Errorf("creating with %s: %d %s, want 422 saying %q", c.name, status, body, c.says)
		}
		status, body = h.call(alice, "PATCH", one, `{"definition":`+definition(c.extra, c.params)+`}`)
		if status != http.StatusUnprocessableEntity || !strings.Contains(body, c.says) {
			t.Errorf("editing to %s: %d %s, want 422 saying %q", c.name, status, body, c.says)
		}
	}
	// And Bob may name his own.
	status, body = h.call(bob, "POST", ListPath, create(``, `,"notebook_id":22`))
	h.made(status, body, http.StatusCreated)

	// An edit that names no model runs on the account's own from then on:
	// the old choice is not kept behind the person's back.
	status, body = h.call(alice, "PATCH", one, `{"definition":`+definition(``, ``)+`}`)
	if status != http.StatusOK || strings.Contains(body, "model-a") || strings.Contains(body, `"models"`) {
		t.Errorf("an edit naming no model: %d %s", status, body)
	}

	// A proxy that cannot be asked is said, and nothing is saved on a guess;
	// a definition that names no model does not ask it at all.
	down := build(menu{offered: true, down: true}, true)
	status, body = down.call(alice, "POST", ListPath, create(`,"models":{"chat":"model-a"}`, ``))
	if status != http.StatusServiceUnavailable || !strings.Contains(body, assembly.ModelsUnreachable) {
		t.Errorf("a save while the proxy is down: %d %s", status, body)
	}
	status, body = down.call(alice, "POST", ListPath, create(``, ``))
	h.made(status, body, http.StatusCreated)

	var saved int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM automations`).Scan(&saved); err != nil || saved != 4 {
		t.Errorf("%d automations are saved, want Alice's three and Bob's one (%v)", saved, err)
	}
}

// Deleting an automation removes the hidden threads its runs made
// artifacts under, and no other thread: not another account's, and not a
// conversation a run's row happens to name.
func TestDeletingAnAutomationRemovesItsRunsThreads(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	ctx := context.Background()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Pool: pool, Queue: &published{}, Models: menu{offered: true}, Errors: errorenvelope.Writer{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now}.Register(api)
	h := harness{t: t, api: api}
	status, body := h.call(alice, "POST", ListPath, digest)
	alices := h.made(status, body, http.StatusCreated)
	status, body = h.call(bob, "POST", ListPath, digest)
	bobs := h.made(status, body, http.StatusCreated)
	if _, err := pool.Exec(ctx, `
		INSERT INTO new_chat_threads (id, user_id, title, source) VALUES
		  (501, $1, 'A run of Alice''s', 'artifact_api'), (502, $1, 'Alice''s own conversation', 'surfsense'),
		  (503, $2, 'A run of Bob''s', 'artifact_api')`, alice, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO automation_runs (automation_id, status, definition_snapshot, thread_id) VALUES
		  ($1, 'succeeded', '{}', 501), ($1, 'succeeded', '{}', 502), ($2, 'succeeded', '{}', 503)`, alices, bobs); err != nil {
		t.Fatal(err)
	}
	// Bob cannot delete Alice's automation, and her threads stay.
	if status, body := h.call(bob, "DELETE", ListPath+"/"+strconv.FormatInt(alices, 10), ""); status != http.StatusNotFound {
		t.Fatalf("Bob deleting Alice's automation: %d %s", status, body)
	}
	if status, body := h.call(alice, "DELETE", ListPath+"/"+strconv.FormatInt(alices, 10), ""); status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", status, body)
	}
	rows, err := pool.Query(ctx, `SELECT id FROM new_chat_threads ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var left []string
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		left = append(left, strconv.FormatInt(id, 10))
	}
	if strings.Join(left, ",") != "502,503" {
		t.Errorf("threads left: %v, want Alice's conversation and Bob's run", left)
	}
}
