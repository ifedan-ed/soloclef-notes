package chat

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// What a notebook chat's turn owes its notebook before the agent is built.
//
// A notebook chat reads the notebook's enabled sources and nothing else
// (context.go). Mentioning a document with @ in such a chat is the person
// saying "use this one": if it is a document of theirs that is not yet a
// source, it is made one here, before the turn's sources are read, so
// the search finds it instead of answering that it is outside the notebook.
// It is the same write the notebook's own "add sources" does, and it owes
// the same thing afterwards: a notebook that still names itself is named
// again from its sources.
//
// Three things hold, on purpose:
//
//   - A source the owner unticked stays unticked. The mention is then said
//     to be outside the notebook's enabled sources, which is true.
//   - Only the account's own documents and folders are linked, into its
//     own notebook. A mention that names another account's document names
//     nothing: the statements select by owner, and the schema's composite
//     keys refuse the pair besides.
//   - A turn resumed after an approval adds nothing: the sources were
//     settled when the question was asked, and a source removed while the
//     turn waited is not put back by answering "approve".
//
// The second thing owed is the name itself: a notebook that names itself
// and has never been named is named from its first question, when it has
// no readable source to be named from. The naming is a queued task of the
// worker tier (notebook_autoname); this file only says when it is due.

// The naming task, as the worker tier registers it
// (soloclef_worker/internal/notebookname): notebook_autoname(notebook_id,
// trigger, question), on the default queue.
const (
	autonameTask = "notebook_autoname"
	// A batch of documents finishes within seconds of each other; waiting
	// a moment lets the first name see more than the very first one.
	autonameSettle = 4 * time.Second
	// autonameQuestionChars is how much of a question the task is given.
	autonameQuestionChars = 2000
)

// NotebookTurn is what one turn did to its notebook and what naming it is
// owed.
type NotebookTurn struct {
	// NotebookID is 0 for a thread that is not a notebook's.
	NotebookID int64
	// AddedDocuments and AddedFolders are the mentions that became sources
	// in this turn.
	AddedDocuments []int64
	AddedFolders   []int64
	// NameFromSources says the notebook names itself and gained a source
	// that can be read.
	NameFromSources bool
	// NameFromQuestion says the notebook names itself and never has been.
	NameFromQuestion bool
}

// NotebookRows is the part of the database a notebook chat's turn writes
// before it runs. Store is the one a server uses.
type NotebookRows interface {
	// PrepareNotebook links the turn's mentions to the thread's notebook
	// and reports what naming the notebook is owed. It does nothing for a
	// thread that is not a notebook's.
	PrepareNotebook(ctx context.Context, turn Turn) (NotebookTurn, error)
}

// Enqueuer publishes one task for the worker tier (celeryqueue.Queue).
type Enqueuer interface {
	Enqueue(ctx context.Context, message produce.Message) (string, error)
}

// autoTitleRuns is how many times a notebook has named itself, from its
// auto_title column: {"runs": n, …}. Anything that is not such an object
// counts as never, as the worker's own reading of the column does
// (autoname/policy.py).
func autoTitleRuns(column []byte) int {
	var state struct {
		Runs any `json:"runs"`
	}
	if len(column) == 0 || json.Unmarshal(column, &state) != nil {
		return 0
	}
	if runs, isNumber := state.Runs.(float64); isNumber && runs > 0 && runs == float64(int(runs)) {
		return int(runs)
	}
	return 0
}

func (s Store) PrepareNotebook(ctx context.Context, turn Turn) (NotebookTurn, error) {
	// The common turn is a plain chat, or a notebook chat that mentions
	// nothing: one read, with no transaction around it (measured, a third
	// of the round trips). Only a turn with something to link opens one.
	notebook, err := New(s.Pool).NotebookOfThread(ctx, NotebookOfThreadParams{ThreadID: turn.ThreadID, UserID: turn.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return NotebookTurn{}, nil
	}
	if err != nil {
		return NotebookTurn{}, err
	}
	out := NotebookTurn{NotebookID: notebook.ID}
	selfNamed := notebook.TitleSource == "auto"
	out.NameFromQuestion = selfNamed && autoTitleRuns(notebook.AutoTitle) == 0
	if len(turn.DocumentIDs)+len(turn.FolderIDs) == 0 {
		return out, nil
	}
	// One transaction: a notebook never has the new source without having
	// risen in the list, and what is reported as added is what was. The
	// notebook was read outside it; the inserts name it by id, and a
	// notebook deleted in between fails them on its foreign key.
	return database.Write(ctx, s.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (NotebookTurn, error) {
		queries := New(tx)
		var err error
		if len(turn.DocumentIDs) > 0 {
			if out.AddedDocuments, err = queries.LinkMentionedDocuments(ctx, LinkMentionedDocumentsParams{NotebookID: notebook.ID, UserID: turn.UserID, DocumentIds: turn.DocumentIDs}); err != nil {
				return NotebookTurn{}, err
			}
		}
		if len(turn.FolderIDs) > 0 {
			if out.AddedFolders, err = queries.LinkMentionedFolders(ctx, LinkMentionedFoldersParams{NotebookID: notebook.ID, UserID: turn.UserID, FolderIds: turn.FolderIDs}); err != nil {
				return NotebookTurn{}, err
			}
		}
		if len(out.AddedDocuments) == 0 && len(out.AddedFolders) == 0 {
			return out, nil
		}
		if err := queries.TouchNotebook(ctx, TouchNotebookParams{NotebookID: notebook.ID, UserID: turn.UserID}); err != nil {
			return NotebookTurn{}, err
		}
		if selfNamed && len(out.AddedDocuments) > 0 {
			if out.NameFromSources, err = queries.AnyDocumentReady(ctx, AnyDocumentReadyParams{UserID: turn.UserID, DocumentIds: out.AddedDocuments}); err != nil {
				return NotebookTurn{}, err
			}
		}
		return out, nil
	})
}

// prepareNotebook is run by the routes that start a turn from a question
// (a new message, a question asked again), before the turn's agent is
// built: the agent's search is confined to the sources as they are after
// this.
//
// A failure to link is the turn's failure: the person mentioned a document
// and would otherwise be answered without it and told it is outside the
// notebook. A failure to queue the naming is not: the answer does not
// depend on the notebook's title, so it is logged and the turn goes on,
// and the next source or question queues it again.
func (s *Service) prepareNotebook(ctx context.Context, turn Turn, question string) error {
	done, err := s.Notebooks.PrepareNotebook(ctx, turn)
	if err != nil || done.NotebookID == 0 {
		return err
	}
	queue := func(message produce.Message) {
		if _, err := s.Queue.Enqueue(ctx, message); err != nil {
			s.Logger.WarnContext(ctx, "a notebook's naming could not be queued", "error", err.Error(), "notebook_id", done.NotebookID)
		}
	}
	if done.NameFromSources {
		queue(produce.Message{Task: autonameTask, Args: []any{done.NotebookID, "source", nil}, Countdown: autonameSettle})
	}
	if done.NameFromQuestion && hasText(question) {
		queue(produce.Message{Task: autonameTask, Args: []any{done.NotebookID, "question", clipped(question, autonameQuestionChars)}})
	}
	return nil
}

// hasText reports whether a question is more than white space.
func hasText(text string) bool {
	for _, r := range text {
		if r != ' ' && r != '\n' && r != '\t' && r != '\r' {
			return true
		}
	}
	return false
}
