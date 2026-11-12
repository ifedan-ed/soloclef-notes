package chat

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/notebookscope"
)

// Context reads the turn's context.
func (s Store) Context(ctx context.Context, turn Turn) (TurnContext, error) {
	queries := New(s.Pool)
	row, err := queries.TurnContext(ctx, TurnContextParams{ThreadID: turn.ThreadID, UserID: turn.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		// The route authorised this thread a moment ago, so it was deleted
		// in between. That is a failed turn, not an empty prompt.
		return TurnContext{}, fmt.Errorf("chat: thread %d is no longer the account's", turn.ThreadID)
	}
	if err != nil {
		return TurnContext{}, err
	}
	out := TurnContext{
		Instructions: row.ChatInstructions, Citations: row.CitationsEnabled,
		UserName: row.DisplayName, UserMemory: row.MemoryMd,
	}
	if row.NotebookID == nil {
		return out, nil
	}

	notebook := &Notebook{ID: *row.NotebookID}
	out.Notebook = notebook
	if row.NotebookTitle == nil {
		// The thread names a notebook this account does not have. It
		// stays a notebook chat, with nothing to read: failing open here
		// would be a search of everything the account has by a chat the
		// person was told is confined.
		return out, nil
	}
	notebook.Title = *row.NotebookTitle
	if row.NotebookGoal != nil {
		notebook.Goal = *row.NotebookGoal
	}
	if row.NotebookInstructions != nil {
		notebook.Instructions = *row.NotebookInstructions
	}
	if row.NotebookLength != nil {
		notebook.Length = *row.NotebookLength
	}
	// The line every tier draws the same way (notebookscope): which of
	// the account's documents this notebook's enabled sources reach.
	sources, err := notebookscope.Read(ctx, s.Pool, turn.UserID, notebook.ID)
	if err != nil {
		return TurnContext{}, err
	}
	notebook.SourceTitles, notebook.DocumentIDs = sources.Titles, sources.DocumentIDs
	return out, nil
}

// clipped is text cut to at most max characters.
func clipped(text string, max int) string {
	if runes := []rune(text); len(runes) > max {
		return string(runes[:max])
	}
	return text
}
