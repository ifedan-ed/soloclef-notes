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
			IsPublic: row.IsPublic,
			Version:  row.Version, CreatedAt: row.CreatedAt,
		})
	}
	return prompts, nil
}

// PublicPrompts is list_public_prompts' query: the community shelf, less the
// caller's own.
func (s Store) PublicPrompts(ctx context.Context, callerID string) ([]Prompt, error) {
	rows, err := New(s.Pool).PublicPromptsOfOtherAccounts(ctx, callerID)
	if err != nil {
		return nil, err
	}
	prompts := make([]Prompt, 0, len(rows))
	for _, row := range rows {
		prompts = append(prompts, Prompt{
			ID: row.ID, Name: row.Name, Prompt: row.Prompt, Mode: row.Mode,
			IsPublic: row.IsPublic,
			Version:  row.Version, CreatedAt: row.CreatedAt,
			AuthorName: row.AuthorName,
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
		IsPublic: row.IsPublic,
		Version:  row.Version, CreatedAt: row.CreatedAt,
	}, nil
}

// PublishedPrompt is copy_public_prompt's source.
func PublishedPrompt(ctx context.Context, tx pgx.Tx, promptID int64) (*Prompt, error) {
	row, err := New(tx).PublicPromptByID(ctx, promptID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Prompt{
		ID: row.ID, Name: row.Name, Prompt: row.Prompt, Mode: row.Mode,
		IsPublic: row.IsPublic,
		Version:  row.Version, CreatedAt: row.CreatedAt,
	}, nil
}

// Insert writes a new prompt and returns the row as stored, which is what
// PromptRead renders — the Python route refreshes the instance for the same
// reason.
func Insert(ctx context.Context, tx pgx.Tx, callerID string, requested Requested, createdAt time.Time) (Prompt, error) {
	row, err := New(tx).CreatePrompt(ctx, CreatePromptParams{
		UserID:    callerID,
		Name:      requested.Name,
		Prompt:    requested.Prompt,
		Mode:      PromptMode(requested.Mode),
		Version:   initialVersion,
		IsPublic:  requested.IsPublic,
		CreatedAt: createdAt,
	})
	if err != nil {
		return Prompt{}, err
	}
	return Prompt{
		ID: row.ID, Name: row.Name, Prompt: row.Prompt, Mode: row.Mode,
		IsPublic: row.IsPublic,
		Version:  row.Version, CreatedAt: row.CreatedAt,
	}, nil
}

// Copy writes the copy copy_public_prompt makes.
//
// What it does **not** carry over is the point of the route: the copy belongs
// to the caller, is not published, and starts again at version 1 even when
// the source is at 7: only the name, the text and the mode are copied.
func Copy(ctx context.Context, tx pgx.Tx, callerID string, source Prompt, createdAt time.Time) (Prompt, error) {
	return Insert(ctx, tx, callerID, Requested{
		Name:     source.Name,
		Prompt:   source.Prompt,
		Mode:     source.Mode,
		IsPublic: false,
	}, createdAt)
}

// Update writes the fields the body mentioned, and bumps the version when one
// of them was content.
func Update(ctx context.Context, tx pgx.Tx, callerID string, promptID int64, changes Changes) (Prompt, error) {
	row, err := New(tx).UpdatePrompt(ctx, UpdatePromptParams{
		SetName:     changes.Name.Present,
		Name:        changes.Name.Value,
		SetPrompt:   changes.Prompt.Present,
		Prompt:      changes.Prompt.Value,
		SetMode:     changes.Mode.Present,
		Mode:        nullMode(changes.Mode.Value),
		SetIsPublic: changes.IsPublic.Present,
		IsPublic:    changes.IsPublic.Value,
		BumpVersion: changes.MentionsContent(),
		ID:          promptID,
		UserID:      callerID,
	})
	if err != nil {
		return Prompt{}, err
	}
	return Prompt{
		ID: row.ID, Name: row.Name, Prompt: row.Prompt, Mode: row.Mode,
		IsPublic: row.IsPublic,
		Version:  row.Version, CreatedAt: row.CreatedAt,
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
