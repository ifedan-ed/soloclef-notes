package automationrun

import (
	"context"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

const queued = `{"name":"Digest","plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}],"execution":{"concurrency":"queue"}}`

// The run's row against a real Postgres: a run is claimed once, queued runs
// start one at a time in the order they were made, a paused automation's
// run is cancelled instead of started, an ending is written only over a
// running run, and the account a run acts as is read by its own id.
func TestARunIsClaimedOnceInOrderAndEndedOnlyWhileRunning(t *testing.T) {
	pool := testdatabase.Open(t)
	ctx := context.Background()
	owner, other := testdatabase.Account(t, pool), testdatabase.Account(t, pool)
	var automationID, first, second int64
	if err := pool.QueryRow(ctx, `INSERT INTO automations (user_id, name, definition) VALUES ($1::uuid, 'Digest', $2) RETURNING id`, owner, queued).Scan(&automationID); err != nil {
		t.Fatal(err)
	}
	insert := func() (id int64) {
		t.Helper()
		if err := pool.QueryRow(ctx, `INSERT INTO automation_runs (automation_id, definition_snapshot, inputs) VALUES ($1, $2, '{"n":1}') RETURNING id`, automationID, queued).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first, second = insert(), insert()
	store := Store{Pool: pool}
	status := func(id int64) string {
		t.Helper()
		got, err := store.Status(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// The second run waits behind the first, even when its message comes
	// first.
	if _, claim, err := store.Claim(ctx, second); err != nil || claim != Waiting {
		t.Fatalf("the younger run: %v %v, want waiting", claim, err)
	}
	run, claim, err := store.Claim(ctx, first)
	if err != nil || claim != Claimed || run.UserID != owner || run.AutomationID != automationID || string(run.Inputs) != `{"n": 1}` {
		t.Fatalf("the older run: %+v %v %v", run, claim, err)
	}
	// A second message for the same run finds it taken; the one behind it
	// still waits while it runs.
	if _, claim, _ := store.Claim(ctx, first); claim != Taken {
		t.Errorf("a duplicate claim: %v, want taken", claim)
	}
	if _, claim, _ := store.Claim(ctx, second); claim != Waiting {
		t.Errorf("the younger run while the older runs: %v, want waiting", claim)
	}
	if next, waiting, err := store.NextWaiting(ctx, automationID); err != nil || !waiting || next != second {
		t.Errorf("next waiting %d %v %v, want %d", next, waiting, err, second)
	}

	written, err := store.Finish(ctx, first, Ending{Status: Succeeded, Steps: []StepResult{{StepID: "a", Action: "agent_task", Status: Succeeded}},
		Output: map[string]any{"final_message": "Done."}})
	if err != nil || !written || status(first) != Succeeded {
		t.Fatalf("finish: %v %v, status %s", written, err, status(first))
	}
	var output, steps string
	if err := pool.QueryRow(ctx, `SELECT output::text, step_results::text FROM automation_runs WHERE id = $1 AND finished_at IS NOT NULL AND error IS NULL`, first).Scan(&output, &steps); err != nil ||
		!strings.Contains(output, "Done.") || !strings.Contains(steps, `"step_id": "a"`) {
		t.Errorf("the row holds %s and %s (%v)", output, steps, err)
	}

	// Now it is the second run's turn. Cancelled from the page while it
	// runs, it keeps that ending, and still gets the work it had done.
	if _, claim, err := store.Claim(ctx, second); err != nil || claim != Claimed {
		t.Fatalf("the younger run after the older ended: %v %v", claim, err)
	}
	testdatabase.Exec(t, pool, `UPDATE automation_runs SET status = 'cancelled', error = '{"type":"cancelled"}' WHERE id = $1`, second)
	written, err = store.Finish(ctx, second, Ending{Status: Succeeded, Steps: []StepResult{{StepID: "a", Status: Succeeded}}, Output: map[string]any{"final_message": "Late."}})
	if err != nil || written || status(second) != Cancelled {
		t.Errorf("finishing a cancelled run: %v %v, status %s", written, err, status(second))
	}
	if err := pool.QueryRow(ctx, `SELECT output::text FROM automation_runs WHERE id = $1 AND error->>'type' = 'cancelled'`, second).Scan(&output); err != nil || !strings.Contains(output, "Late.") {
		t.Errorf("the cancelled run's work was not kept: %s (%v)", output, err)
	}

	// A run made in the instant after a pause is cancelled by the claim.
	third := insert()
	testdatabase.Exec(t, pool, `UPDATE automations SET status = 'paused' WHERE id = $1`, automationID)
	if _, claim, err := store.Claim(ctx, third); err != nil || claim != Stopped || status(third) != Cancelled {
		t.Errorf("a paused automation's run: %v %v, status %s", claim, err, status(third))
	}
	if _, claim, _ := store.Claim(ctx, 1<<40); claim != Gone {
		t.Errorf("a run that does not exist: %v, want gone", claim)
	}

	// The prompt's rows are the account's own, by its id; an account that
	// is not active gets none.
	testdatabase.Exec(t, pool, `UPDATE "user" SET display_name = 'Ada', memory_md = 'likes tides', chat_instructions = 'Be brief.' WHERE id = $1::uuid`, owner)
	testdatabase.Exec(t, pool, `INSERT INTO user_models (user_id, purpose, model) VALUES ($1::uuid, 'chat', 'ada-model')`, owner)
	about, err := store.Context(ctx, assembly.Turn{UserID: owner})
	if err != nil || about.UserName != "Ada" || about.UserMemory != "likes tides" || about.Instructions != "Be brief." || about.Notebook != nil {
		t.Errorf("the owner's context: %+v %v", about, err)
	}
	if theirs, err := store.Context(ctx, assembly.Turn{UserID: other}); err != nil || theirs.UserName != "" || theirs.UserMemory != "" {
		t.Errorf("the other account's context holds the owner's: %+v %v", theirs, err)
	}
	if name, err := store.ChatModel(ctx, owner); err != nil || name != "ada-model" {
		t.Errorf("the owner's model: %q %v", name, err)
	}
	if name, err := store.ChatModel(ctx, other); err != nil || name != "" {
		t.Errorf("the other account's model: %q %v", name, err)
	}
	testdatabase.Exec(t, pool, `UPDATE "user" SET is_active = false WHERE id = $1::uuid`, other)
	if _, err := store.Context(ctx, assembly.Turn{UserID: other}); err == nil {
		t.Error("an account that is not active was given a context")
	}
}

// What a run reads and writes for its owner, against a real Postgres, as
// two accounts: the claim gives the run a hidden thread of the owner's, a
// confined step reads only its own notebook's sources, a task's connected
// accounts are the owner's, and a note is filed only in a notebook of the
// account it is written for.
func TestARunsThreadNotebookAccountsAndNotesAreItsOwnersAlone(t *testing.T) {
	pool := testdatabase.Open(t)
	ctx := context.Background()
	owner, other := testdatabase.Account(t, pool), testdatabase.Account(t, pool)
	store := Store{Pool: pool}
	one := func(query string, args ...any) (id int64) {
		t.Helper()
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	automationID := one(`INSERT INTO automations (user_id, name, definition) VALUES ($1::uuid, 'Digest', $2) RETURNING id`, owner, queued)
	runID := one(`INSERT INTO automation_runs (automation_id, definition_snapshot) VALUES ($1, $2) RETURNING id`, automationID, queued)

	// The claim makes the run's thread: the owner's, hidden from every
	// list of conversations, and remembered on the run.
	run, claim, err := store.Claim(ctx, runID)
	if err != nil || claim != Claimed || run.ThreadID == 0 {
		t.Fatalf("claim: %+v %v %v", run, claim, err)
	}
	var threadOwner, source, title string
	var remembered int64
	if err := pool.QueryRow(ctx, `
		SELECT t.user_id::text, t.source, t.title, r.thread_id
		FROM automation_runs r JOIN new_chat_threads t ON t.id = r.thread_id WHERE r.id = $1`, runID).Scan(&threadOwner, &source, &title, &remembered); err != nil ||
		threadOwner != owner || source != "artifact_api" || title != "Digest (automation run)" || remembered != run.ThreadID {
		t.Errorf("the run's thread: owner %s, source %s, title %q, remembered %d (%v)", threadOwner, source, title, remembered, err)
	}

	// Each account has a notebook with one source of its own.
	notebook := func(account, name string) (notebookID, documentID int64) {
		notebookID = one(`INSERT INTO notebooks (user_id, title, chat_instructions) VALUES ($1::uuid, $2, 'Answer briefly.') RETURNING id`, account, name)
		documentID = one(`INSERT INTO documents (user_id, title, document_type, content, content_hash, status)
			VALUES ($1::uuid, $2, 'FILE', 'text', $3, '{"state":"ready"}') RETURNING id`, account, name+" source", name+"-hash")
		testdatabase.Exec(t, pool, `INSERT INTO notebook_sources (notebook_id, user_id, document_id) VALUES ($1, $2::uuid, $3)`, notebookID, account, documentID)
		return notebookID, documentID
	}
	mine, myDocument := notebook(owner, "Tides")
	theirs, _ := notebook(other, "Theirs")

	confined := func(notebookID int64) assembly.Turn {
		return assembly.Turn{UserID: owner, Unattended: &assembly.Unattended{NotebookID: notebookID}}
	}
	about, err := store.Context(ctx, confined(mine))
	if err != nil || about.Notebook == nil || about.Notebook.Title != "Tides" || about.Notebook.Instructions != "Answer briefly." ||
		len(about.Notebook.DocumentIDs) != 1 || about.Notebook.DocumentIDs[0] != myDocument || len(about.Notebook.SourceTitles) != 1 {
		t.Fatalf("the owner's notebook: %+v %v", about.Notebook, err)
	}
	// Another account's notebook confines the step to nothing. It is never
	// "no confinement", and never that account's sources.
	about, err = store.Context(ctx, confined(theirs))
	if err != nil || about.Notebook == nil || about.Notebook.Title != "" || len(about.Notebook.DocumentIDs) != 0 || len(about.Notebook.SourceTitles) != 0 {
		t.Errorf("another account's notebook: %+v %v", about.Notebook, err)
	}
	if name, found, err := store.Notebook(ctx, owner, mine); err != nil || !found || name != "Tides" {
		t.Errorf("the owner's notebook by id: %q %v %v", name, found, err)
	}
	if _, found, err := store.Notebook(ctx, owner, theirs); err != nil || found {
		t.Errorf("another account's notebook was found: %v %v", found, err)
	}

	// A task's connected accounts are looked up under the owner.
	connection := func(account, name string, enabled bool) int64 {
		return one(`INSERT INTO search_source_connectors (user_id, name, connector_type, config, enabled)
			VALUES ($1::uuid, $2, 'EMAIL_IMAP_CONNECTOR', '{}', $3) RETURNING id`, account, name, enabled)
	}
	on, off, foreign := connection(owner, "Mail", true), connection(owner, "Old mail", false), connection(other, "Their mail", true)
	found, err := store.Connections(ctx, owner, []int64{on, off, foreign, 1 << 40})
	if err != nil || len(found) != 2 || found[0] != (Connection{ID: on, Name: "Mail", Kind: "EMAIL_IMAP_CONNECTOR", Enabled: true}) ||
		found[1].ID != off || found[1].Enabled {
		t.Errorf("the owner's connected accounts: %+v %v", found, err)
	}

	// A note is filed in the owner's notebook, as a note an automation
	// wrote, waiting to be indexed; and never in another account's.
	noteID, err := store.SaveNote(ctx, Note{UserID: owner, AutomationID: automationID, NotebookID: mine, Title: "Digest, 12 November 2026", Markdown: "High tide at noon."})
	if err != nil {
		t.Fatal(err)
	}
	var noteOwner, kind, state, origin, markdown string
	var filedIn, by int64
	if err := pool.QueryRow(ctx, `
		SELECT d.user_id::text, d.document_type::text, d.status->>'state', n.origin, d.source_markdown, n.notebook_id, n.automation_id
		FROM documents d JOIN notes n ON n.document_id = d.id AND n.user_id = d.user_id WHERE d.id = $1`, noteID).
		Scan(&noteOwner, &kind, &state, &origin, &markdown, &filedIn, &by); err != nil ||
		noteOwner != owner || kind != "NOTE" || state != "pending" || origin != "automation" || markdown != "High tide at noon." || filedIn != mine || by != automationID {
		t.Errorf("the note: owner %s, %s, %s, origin %s, %q, notebook %d, automation %d (%v)", noteOwner, kind, state, origin, markdown, filedIn, by, err)
	}
	if _, err := store.SaveNote(ctx, Note{UserID: owner, AutomationID: automationID, NotebookID: theirs, Title: "t", Markdown: "m"}); err != errNotebookGone {
		t.Errorf("a note for another account's notebook: %v", err)
	}
	var notes int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notes WHERE notebook_id = $1`, theirs).Scan(&notes); err != nil || notes != 0 {
		t.Errorf("%d notes were filed in the other account's notebook (%v)", notes, err)
	}
}
