package automationdrafts

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxChoices bounds the notebooks and folders read to resolve a name. An
// account with more still has the newest notebooks and the first folders by
// name to choose from, and the model is told the list was cut.
const maxChoices = 300

// Choice is a notebook or a folder a draft may name, by id and title.
type Choice struct {
	ID    int64
	Title string
}

// ErrNotWaiting is a replaces_draft_id that names no draft of the caller's
// that is still waiting.
var ErrNotWaiting = errors.New("automationdrafts: the draft to replace is not waiting")

// Store keeps drafts in automation_drafts and reads what a draft may name.
type Store struct {
	Pool *pgxpool.Pool
}

// Save stores a checked request as a draft waiting on its card, and returns
// its id. With replaces, the older draft is set aside in the same
// transaction, so the older card says "replaced" exactly when the newer one
// exists; a replaces that is not the caller's waiting draft saves nothing
// (ErrNotWaiting).
func (s Store) Save(ctx context.Context, userID string, request []byte, replaces string) (string, error) {
	var id string
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := New(tx)
		var err error
		if id, err = q.InsertDraft(ctx, InsertDraftParams{UserID: userID, Request: request}); err != nil {
			return err
		}
		if replaces == "" {
			return nil
		}
		changed, err := q.SupersedeDraft(ctx, SupersedeDraftParams{ReplacedBy: &id, ID: replaces, UserID: userID})
		if err != nil {
			return err
		}
		if changed == 0 {
			return ErrNotWaiting
		}
		return nil
	})
	return id, err
}

// Notebooks are the caller's notebooks, newest first.
func (s Store) Notebooks(ctx context.Context, userID string) ([]Choice, error) {
	rows, err := New(s.Pool).NotebookChoices(ctx, NotebookChoicesParams{UserID: userID, MaxRows: maxChoices})
	if err != nil {
		return nil, err
	}
	out := make([]Choice, len(rows))
	for i, row := range rows {
		out[i] = Choice{ID: row.ID, Title: row.Title}
	}
	return out, nil
}

// Folders are the caller's folders, by name.
func (s Store) Folders(ctx context.Context, userID string) ([]Choice, error) {
	rows, err := New(s.Pool).FolderChoices(ctx, FolderChoicesParams{UserID: userID, MaxRows: maxChoices})
	if err != nil {
		return nil, err
	}
	out := make([]Choice, len(rows))
	for i, row := range rows {
		out[i] = Choice{ID: row.ID, Title: row.Name}
	}
	return out, nil
}
