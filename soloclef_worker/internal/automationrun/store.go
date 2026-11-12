package automationrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/notebookscope"
	"git.danvics.com/danvics/soloclef/soloclef_automations/definition"
)

// Store is Rows, and the account rows a run's agent is built from, in
// Postgres.
type Store struct {
	Pool *pgxpool.Pool
}

// Claim takes a pending run for this worker.
//
// It is one transaction under the automation's row lock, the lock the
// launch of a run takes too (soloclef_automations/launch), so "is another
// run of this automation running" cannot change between being asked and
// being acted on, and of any number of messages for one run exactly one
// finds it pending.
func (s Store) Claim(ctx context.Context, runID int64) (Run, Claim, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Run{}, 0, err
	}
	defer tx.Rollback(ctx)

	run := Run{ID: runID}
	var automationStatus, runStatus string
	err = tx.QueryRow(ctx, `
		SELECT a.id, a.user_id::text, a.name, a.status::text, r.status::text, r.definition_snapshot, r.inputs
		FROM automation_runs AS r
		JOIN automations AS a ON a.id = r.automation_id
		WHERE r.id = $1
		FOR UPDATE OF a`, runID).
		Scan(&run.AutomationID, &run.UserID, &run.Name, &automationStatus, &runStatus, &run.Snapshot, &run.Inputs)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, Gone, nil
	}
	if err != nil {
		return Run{}, 0, err
	}
	if runStatus != "pending" {
		return Run{}, Taken, nil
	}
	if automationStatus != "active" {
		// Pausing cancels the runs it finds; this is one created in the
		// instant after. It ends the way they did.
		reason, err := json.Marshal(failure("cancelled", "The automation was "+automationStatus+"."))
		if err != nil {
			return Run{}, 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE automation_runs SET status = 'cancelled', finished_at = now(), error = $2 WHERE id = $1`, runID, reason); err != nil {
			return Run{}, 0, err
		}
		return Run{}, Stopped, tx.Commit(ctx)
	}
	// One at a time unless the automation says its runs may overlap: a run
	// waits while another is running or an older one is still waiting, so
	// queued runs start in the order they were created.
	var concurrency string
	if err := tx.QueryRow(ctx, `SELECT COALESCE($1::jsonb->'execution'->>'concurrency', '')`, run.Snapshot).Scan(&concurrency); err != nil {
		return Run{}, 0, err
	}
	if concurrency != definition.Always {
		var ahead bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM automation_runs
			               WHERE automation_id = $1 AND id <> $2
			                 AND (status = 'running' OR (status = 'pending' AND id < $2)))`,
			run.AutomationID, runID).Scan(&ahead); err != nil {
			return Run{}, 0, err
		}
		if ahead {
			return Run{}, Waiting, nil
		}
	}
	// The run's own hidden thread, made with the claim: what the run makes
	// (a report, flashcards, a podcast) is made under a thread, whose
	// sandbox and attribution are keyed by it. `artifact_api` keeps it out
	// of every list of conversations, as a studio generation's is.
	if err := tx.QueryRow(ctx, `
		INSERT INTO new_chat_threads (user_id, title, source)
		VALUES ($1::uuid, left($2, 500), 'artifact_api') RETURNING id`,
		run.UserID, run.Name+" (automation run)").Scan(&run.ThreadID); err != nil {
		return Run{}, 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE automation_runs SET status = 'running', started_at = now(), thread_id = $2 WHERE id = $1`, runID, run.ThreadID); err != nil {
		return Run{}, 0, err
	}
	return run, Claimed, tx.Commit(ctx)
}

func (s Store) Status(ctx context.Context, runID int64) (string, error) {
	var status string
	err := s.Pool.QueryRow(ctx, `SELECT status::text FROM automation_runs WHERE id = $1`, runID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return status, err
}

// Finish writes the ending, and only over a run still running: a run that
// was cancelled or timed out from elsewhere keeps that ending, and its
// transcript is still saved beside it so the person can see how far it got.
func (s Store) Finish(ctx context.Context, runID int64, ending Ending) (bool, error) {
	steps, err := json.Marshal(ending.Steps)
	if err != nil {
		return false, err
	}
	output, err := encode(ending.Output)
	if err != nil {
		return false, err
	}
	failed, err := encode(ending.Error)
	if err != nil {
		return false, err
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE automation_runs
		SET status = $2::automation_run_status, step_results = $3, output = $4, error = $5, finished_at = now()
		WHERE id = $1 AND status = 'running'`, runID, ending.Status, steps, output, failed)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	// Ended from elsewhere: keep that status and its reason, add the work.
	_, err = s.Pool.Exec(ctx, `
		UPDATE automation_runs SET step_results = $2, output = $3
		WHERE id = $1 AND status IN ('cancelled', 'timed_out')`, runID, steps, output)
	return false, err
}

func (s Store) NextWaiting(ctx context.Context, automationID int64) (int64, bool, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `
		SELECT id FROM automation_runs
		WHERE automation_id = $1 AND status = 'pending'
		ORDER BY id LIMIT 1`, automationID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// Context is assembly.PromptRows for a run: what the prompt takes from the
// account, read by the account's id, and the notebook a step is confined
// to when it names one.
func (s Store) Context(ctx context.Context, turn assembly.Turn) (assembly.TurnContext, error) {
	var out assembly.TurnContext
	err := s.Pool.QueryRow(ctx, `
		SELECT chat_instructions, citations_enabled, COALESCE(display_name, ''), memory_md
		FROM "user" WHERE id = $1::uuid AND is_active`, turn.UserID).
		Scan(&out.Instructions, &out.Citations, &out.UserName, &out.UserMemory)
	if errors.Is(err, pgx.ErrNoRows) {
		// Deleted or deactivated since the run was created: nothing may
		// run for it.
		return assembly.TurnContext{}, fmt.Errorf("automationrun: account %s is not active", turn.UserID)
	}
	if err != nil || turn.Unattended == nil || turn.Unattended.NotebookID == 0 {
		return out, err
	}
	// A confined step. The notebook is read under the account, by the
	// statements the notebook's own chat is confined by (notebookscope).
	// One that is not the account's stays a confinement, to nothing:
	// failing open would be a search of everything the account has by a
	// step its author confined.
	notebook := &assembly.Notebook{ID: turn.Unattended.NotebookID}
	out.Notebook = notebook
	settings, found, err := notebookscope.SettingsOf(ctx, s.Pool, turn.UserID, notebook.ID)
	if err != nil || !found {
		return out, err
	}
	notebook.Title, notebook.Goal, notebook.Instructions, notebook.Length = settings.Title, settings.Goal, settings.Instructions, settings.Length
	sources, err := notebookscope.Read(ctx, s.Pool, turn.UserID, notebook.ID)
	if err != nil {
		return assembly.TurnContext{}, err
	}
	notebook.SourceTitles, notebook.DocumentIDs = sources.Titles, sources.DocumentIDs
	return out, nil
}

// Notebook is the title of one of the account's notebooks; found is false
// when it is not the account's or is not there.
func (s Store) Notebook(ctx context.Context, userID string, notebookID int64) (string, bool, error) {
	settings, found, err := notebookscope.SettingsOf(ctx, s.Pool, userID, notebookID)
	return settings.Title, found, err
}

// Connections is which of the ids are the account's connected accounts,
// by id. One that is another account's, or gone, is simply not among them.
func (s Store) Connections(ctx context.Context, userID string, ids []int64) ([]Connection, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name, connector_type::text, enabled
		FROM search_source_connectors
		WHERE user_id = $1::uuid AND id = ANY($2::bigint[])
		ORDER BY id`, userID, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Connection, error) {
		var found Connection
		err := row.Scan(&found.ID, &found.Name, &found.Kind, &found.Enabled)
		return found, err
	})
}

// errNotebookGone is a note that could not be filed because its notebook
// is no longer the account's.
var errNotebookGone = errors.New("automationrun: the notebook to save to is no longer the account's")

// SaveNote files a step's answer as a note of the account, in the
// notebook, in one transaction: the document and the row that makes it a
// note, as the notes routes write them (soloclef_api/internal/notes), with
// the origin that says an automation wrote it. The notebook is read under
// the account in the same transaction, so a note is never filed in another
// account's notebook.
func (s Store) SaveNote(ctx context.Context, note Note) (int64, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var owned bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notebooks WHERE id = $1 AND user_id = $2::uuid)`, note.NotebookID, note.UserID).Scan(&owned); err != nil {
		return 0, err
	}
	if !owned {
		return 0, errNotebookGone
	}
	hash := sha256.Sum256([]byte(note.Markdown))
	var id int64
	// A note with text starts "pending": a job is queued for it after the
	// commit, and the indexer makes it "ready".
	if err := tx.QueryRow(ctx, `
		INSERT INTO documents (user_id, title, document_type, content, content_hash,
		                       source_markdown, content_needs_reindexing, document_metadata, status)
		VALUES ($1::uuid, $2, 'NOTE', $3, $4, $3, true, '{"NOTE": true}'::jsonb, '{"state": "pending"}'::jsonb)
		RETURNING id`, note.UserID, note.Title, note.Markdown, hex.EncodeToString(hash[:])).Scan(&id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO notes (document_id, user_id, notebook_id, origin, automation_id)
		VALUES ($1, $2::uuid, $3, 'automation', $4)`, id, note.UserID, note.NotebookID, note.AutomationID); err != nil {
		return 0, err
	}
	// A notebook used is a notebook moved to the top of the list.
	if _, err := tx.Exec(ctx, `UPDATE notebooks SET updated_at = now() WHERE id = $1 AND user_id = $2::uuid`, note.NotebookID, note.UserID); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

// errAttended is the answer to a question an unattended turn never asks.
var errAttended = errors.New("automationrun: an unattended run consults no standing trust and stores none")

// Trusted and Trust complete assembly.PromptRows. The assembly does not
// call either for an unattended turn (assembly.Unattended is the one rule
// consulted), so reaching one is a bug, and it is an error rather than an
// answer: neither "yes" nor "no" would be true.
func (s Store) Trusted(context.Context, string, string) (bool, error) { return false, errAttended }

func (s Store) Trust(context.Context, string, string) error { return errAttended }

// ChatModel is assembly.ModelChoice: the account's chosen chat model, or ""
// when it has chosen none and the deployment's default answers.
func (s Store) ChatModel(ctx context.Context, userID string) (string, error) {
	var name string
	err := s.Pool.QueryRow(ctx, `SELECT model FROM user_models WHERE user_id = $1::uuid AND purpose = 'chat'`, userID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return name, err
}
