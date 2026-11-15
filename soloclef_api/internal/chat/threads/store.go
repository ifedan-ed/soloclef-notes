package threads

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// Thread is a thread's row, as the routes answer with it and decide by it.
type Thread struct {
	ID       int64
	Title    string
	Archived bool
	// UserID is the account the thread belongs to: the one it was read
	// for, since no statement reads a thread for anybody else.
	UserID    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Usage is the tokens one answer cost, as its row holds them.
type Usage struct {
	PromptTokens, CompletionTokens, TotalTokens, CostMicros int64
	// Breakdown is the per-model summary as stored JSON, or nil.
	Breakdown []byte
}

// Message is one saved message.
type Message struct {
	ID       int64
	ThreadID int64
	// Role is the column's enum label: USER, ASSISTANT or SYSTEM.
	Role string
	// Content is the stored JSON, passed through untouched.
	Content   []byte
	CreatedAt time.Time
	AuthorID  *string
	TurnID    *string
	// AuthorName and AuthorAvatar are the author's account, when it is
	// still there.
	AuthorName   *string
	AuthorAvatar *string
	Usage        *Usage
}

// Store is the package's database.
//
// It is a struct over the pool and not an interface with a stand-in:
// nearly everything the routes do is decided by a statement (the list's
// filter, the upsert, the cascade), so a stand-in would be a second,
// untested implementation of that SQL. The tests run the routes against a
// real Postgres instead.
type Store struct {
	Pool *pgxpool.Pool
}

// threadRow is the columns every statement that answers a thread returns.
type threadRow struct {
	ID        int64
	Title     string
	Archived  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

func threadOf(userID string, row threadRow) Thread {
	return Thread{ID: row.ID, Title: row.Title, Archived: row.Archived, UserID: userID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

// Thread is the account's thread, and whether it has one by that id.
func (s Store) Thread(ctx context.Context, userID string, threadID int64) (Thread, bool, error) {
	row, err := New(s.Pool).Thread(ctx, ThreadParams{ThreadID: threadID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Thread{}, false, nil
	}
	if err != nil {
		return Thread{}, false, err
	}
	return threadOf(userID, threadRow(row)), true, nil
}

func (s Store) Create(ctx context.Context, userID, title string, archived bool) (Thread, error) {
	row, err := New(s.Pool).InsertThread(ctx, InsertThreadParams{Title: title, Archived: archived, UserID: userID})
	if err != nil {
		return Thread{}, err
	}
	return threadOf(userID, threadRow(row)), nil
}

// Listed is the account's threads, archived or not, newest activity first.
// max of 0 is no limit.
func (s Store) Listed(ctx context.Context, userID string, archived bool, max int64) ([]Thread, error) {
	params := ListedThreadsParams{UserID: userID, Archived: archived}
	if max > 0 {
		params.MaxRows = &max
	}
	rows, err := New(s.Pool).ListedThreads(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]Thread, len(rows))
	for i, row := range rows {
		out[i] = threadOf(userID, threadRow(row))
	}
	return out, nil
}

// likeEscapes makes text match itself in a LIKE pattern.
var likeEscapes = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Search is the account's threads whose title contains text, in any case.
func (s Store) Search(ctx context.Context, userID, text string) ([]Thread, error) {
	rows, err := New(s.Pool).SearchThreads(ctx, SearchThreadsParams{
		UserID: userID, Pattern: likeEscapes.Replace(text), MaxRows: maxSearchResults,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Thread, len(rows))
	for i, row := range rows {
		out[i] = threadOf(userID, threadRow(row))
	}
	return out, nil
}

// Update renames or archives; a nil field keeps its value. found is false
// if the thread went away since it was authorised.
func (s Store) Update(ctx context.Context, userID string, threadID int64, title *string, archived *bool) (Thread, bool, error) {
	row, err := New(s.Pool).UpdateThread(ctx, UpdateThreadParams{Title: title, Archived: archived, ThreadID: threadID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Thread{}, false, nil
	}
	if err != nil {
		return Thread{}, false, err
	}
	return threadOf(userID, threadRow(row)), true, nil
}

// Delete removes the account's thread and everything that hangs off it.
func (s Store) Delete(ctx context.Context, userID string, threadID int64) error {
	_, err := New(s.Pool).DeleteThread(ctx, DeleteThreadParams{ThreadID: threadID, UserID: userID})
	return err
}

// Messages is the thread's messages, oldest first. max of 0 is no limit.
func (s Store) Messages(ctx context.Context, userID string, threadID, skip, max int64) ([]Message, error) {
	params := ThreadMessagesParams{UserID: userID, ThreadID: threadID, Skip: skip}
	if max > 0 {
		params.MaxRows = &max
	}
	rows, err := New(s.Pool).ThreadMessages(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]Message, len(rows))
	for i, row := range rows {
		message := Message{
			ID: row.ID, ThreadID: row.ThreadID, Role: row.Role, Content: row.Content, CreatedAt: row.CreatedAt,
			AuthorID: row.AuthorID, TurnID: row.TurnID, AuthorName: row.AuthorDisplayName, AuthorAvatar: row.AuthorAvatarUrl,
		}
		// prompt_tokens is NOT NULL in its table, so it is absent exactly
		// when the message has no usage row.
		if row.PromptTokens != nil {
			message.Usage = &Usage{PromptTokens: *row.PromptTokens, Breakdown: row.ModelBreakdown}
			if row.CompletionTokens != nil {
				message.Usage.CompletionTokens = *row.CompletionTokens
			}
			if row.TotalTokens != nil {
				message.Usage.TotalTokens = *row.TotalTokens
			}
			if row.CostMicros != nil {
				message.Usage.CostMicros = *row.CostMicros
			}
		}
		out[i] = message
	}
	return out, nil
}

// Append adds a message and moves the thread to the top of the list, as
// one unit. A turn's message is written once: a second append for the same
// turn and role returns the row that is there, with what it holds.
//
// The author is the thread's owner, which is the caller: the statement takes
// it from the thread it found under that owner.
func (s Store) Append(ctx context.Context, userID string, threadID int64, role string, content []byte, turnID *string) (Message, error) {
	return database.Write(ctx, s.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (Message, error) {
		queries := New(tx)
		row, err := queries.InsertMessage(ctx, InsertMessageParams{
			Role: Newchatmessagerole(role), Content: content, ThreadID: threadID, UserID: userID, TurnID: turnID,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows) && turnID != nil:
			existing, err := queries.MessageOfTurn(ctx, MessageOfTurnParams{UserID: userID, ThreadID: threadID, TurnID: turnID, Role: Newchatmessagerole(role)})
			if err != nil {
				return Message{}, err
			}
			row = InsertMessageRow(existing)
		case err != nil:
			return Message{}, err
		}
		if err := queries.TouchThread(ctx, TouchThreadParams{ThreadID: threadID, UserID: userID}); err != nil {
			return Message{}, err
		}
		return Message{ID: row.ID, ThreadID: row.ThreadID, Role: row.Role, Content: row.Content, CreatedAt: row.CreatedAt, AuthorID: row.AuthorID, TurnID: row.TurnID}, nil
	})
}
