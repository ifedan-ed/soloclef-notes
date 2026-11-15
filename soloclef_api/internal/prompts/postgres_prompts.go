package prompts

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store reads prompts. Its read methods take the pool and the write helpers
// take a transaction, which is the seam: a write's statements all run on the
// pgx.Tx internal/database.Write opened.
type Store struct {
	Pool *pgxpool.Pool
}

// Prompts is the caller's own library, newest first.
func (s Store) Prompts(ctx context.Context, callerID string) ([]Prompt, error) {
	rows, err := New(s.Pool).PromptsOfAccount(ctx, callerID)
	if err != nil {
		return nil, err
	}
	prompts := make([]Prompt, 0, len(rows))
	for _, row := range rows {
		prompts = append(prompts, Prompt{
			ID: row.ID, Name: row.Name, Prompt: row.Prompt, Mode: row.Mode,
			Version: row.Version, CreatedAt: row.CreatedAt,
		})
	}
	return prompts, nil
}

// PromptOf is the row the update and the delete resolve, inside their own
// transaction. Nothing absent is an error: a prompt that is not the caller's
// is a prompt that is not there, which is the 404 both routes answer.
func PromptOf(ctx context.Context, tx pgx.Tx, callerID string, promptID int64) (*Prompt, error) {
	row, err := New(tx).PromptOfAccount(ctx, PromptOfAccountParams{ID: promptID, UserID: callerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Prompt{
		ID: row.ID, Name: row.Name, Prompt: row.Prompt, Mode: row.Mode,
		Version: row.Version, CreatedAt: row.CreatedAt,
	}, nil
}

// Insert writes a new prompt and returns the row as stored, which is what
// PromptRead renders.
func Insert(ctx context.Context, tx pgx.Tx, callerID string, requested Requested, createdAt time.Time) (Prompt, error) {
	row, err := New(tx).CreatePrompt(ctx, CreatePromptParams{
		UserID:    callerID,
		Name:      requested.Name,
		Prompt:    requested.Prompt,
		Mode:      PromptMode(requested.Mode),
		Version:   initialVersion,
		CreatedAt: createdAt,
	})
	if err != nil {
		return Prompt{}, err
	}
	return Prompt{
		ID: row.ID, Name: row.Name, Prompt: row.Prompt, Mode: row.Mode,
		Version: row.Version, CreatedAt: row.CreatedAt,
	}, nil
}

// Update writes the fields the body mentioned and bumps the version.
func Update(ctx context.Context, tx pgx.Tx, callerID string, promptID int64, changes Changes) (Prompt, error) {
	row, err := New(tx).UpdatePrompt(ctx, UpdatePromptParams{
		SetName:   changes.Name.Present,
		Name:      changes.Name.Value,
		SetPrompt: changes.Prompt.Present,
		Prompt:    changes.Prompt.Value,
		SetMode:   changes.Mode.Present,
		Mode:      nullMode(changes.Mode.Value),
		ID:        promptID,
		UserID:    callerID,
	})
	if err != nil {
		return Prompt{}, err
	}
	return Prompt{
		ID: row.ID, Name: row.Name, Prompt: row.Prompt, Mode: row.Mode,
		Version: row.Version, CreatedAt: row.CreatedAt,
	}, nil
}

// Delete removes the prompt.
func Delete(ctx context.Context, tx pgx.Tx, callerID string, promptID int64) error {
	return New(tx).DeletePrompt(ctx, DeletePromptParams{ID: promptID, UserID: callerID})
}

func nullMode(value *string) NullPromptMode {
	if value == nil {
		return NullPromptMode{}
	}
	return NullPromptMode{PromptMode: PromptMode(*value), Valid: true}
}
