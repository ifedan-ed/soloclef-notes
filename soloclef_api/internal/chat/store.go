package chat

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// Thread is a chat thread the gate has answered: one of the caller's own.
type Thread struct {
	ID int64
	// UserID is the account the thread belongs to, which is the caller it
	// was read for.
	UserID string
}

// OpenedTurn is the rows a turn starts with.
type OpenedTurn struct {
	// UserMessageID is zero for a resumed turn, which has no new question.
	UserMessageID      int64
	AssistantMessageID int64
}

// NewTurn is what Rows.OpenTurn writes.
type NewTurn struct {
	ThreadID int64
	UserID   string
	TurnID   string
	// UserContent is the question's content, or nil for a resumed turn.
	UserContent []byte
	// AssistantContent is the answer's content before anything has
	// streamed: one empty text part, which is what the apps expect to fill.
	AssistantContent []byte
}

// Usage is the tokens one answer cost.
type Usage struct {
	PromptTokens, CompletionTokens, TotalTokens int64
	// Breakdown and Calls are the per-model summary and the per-call list,
	// as JSON; nil when no model was called.
	Breakdown, Calls []byte
}

// ClosedTurn is what Rows.CloseTurn writes.
type ClosedTurn struct {
	ThreadID, MessageID int64
	UserID              string
	Content             []byte
	// Usage is nil for a turn that called no model.
	Usage *Usage
}

// HistoryMessage is one saved message: who said it and its content parts.
type HistoryMessage struct {
	Assistant bool
	Content   []byte
}

// Question is a saved question and what asking it again replaces.
type Question struct {
	ID int64
	// TurnID is the turn the question opened; "" on a row from before turns
	// had ids.
	TurnID string
	// Content is the question as saved: its text and its mention chips.
	Content []byte
	// At, with ID, is the question's place in the thread.
	At time.Time
	// Replaced are the ids of the question and every message after it.
	Replaced []int64
}

// errNotInThread says a regenerate named a message its thread does not have.
var errNotInThread = errors.New("chat: the message is not in this thread")

// Rows is the chat's database.
type Rows interface {
	// Thread is the account's thread, or nil if it has none by that id:
	// another account's thread is nil, like one that does not exist.
	Thread(ctx context.Context, userID string, threadID int64) (*Thread, error)

	// OpenTurn writes the question (if any) and the empty answer, moves the
	// thread to the top of the list and marks who is being answered, as
	// one unit: a page never sees a question with no answer row to fill.
	OpenTurn(ctx context.Context, turn NewTurn) (OpenedTurn, error)

	// MessageContent is one message's content, or nil if the thread has no
	// such message.
	MessageContent(ctx context.Context, threadID, messageID int64) ([]byte, error)

	// StoreContent saves the answer so far, while the turn runs.
	StoreContent(ctx context.Context, messageID int64, content []byte) error

	// CloseTurn saves the finished answer and its usage and clears who is
	// being answered, as one unit.
	CloseTurn(ctx context.Context, turn ClosedTurn) error

	// History is the thread's saved messages, oldest first.
	History(ctx context.Context, threadID int64) ([]HistoryMessage, error)
	// AssistantMessages counts the thread's answers.
	AssistantMessages(ctx context.Context, threadID int64) (int64, error)
	// LatestAssistantMessage is the newest answer's id, or 0.
	LatestAssistantMessage(ctx context.Context, threadID int64) (int64, error)
	SetTitle(ctx context.Context, userID string, threadID int64, title string) error

	// Question finds the question a regenerate replaces: the one the named
	// message is or answers, or the thread's latest when fromMessageID is
	// zero. It returns nil when the thread has no question, and
	// errNotInThread when the named message is not in the thread.
	Question(ctx context.Context, threadID, fromMessageID int64) (*Question, error)
	// HistoryBefore is the thread's saved messages before a question.
	HistoryBefore(ctx context.Context, threadID int64, question *Question) ([]HistoryMessage, error)
	// ReplaceTurn removes a question and everything after it and opens the
	// turn that takes their place, as one unit: a page never sees the old
	// answer gone and nothing in its place, or both answers at once.
	ReplaceTurn(ctx context.Context, question *Question, turn NewTurn) (OpenedTurn, error)

	PromptRows
}

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) Thread(ctx context.Context, userID string, threadID int64) (*Thread, error) {
	id, err := New(s.Pool).ThreadForTurn(ctx, ThreadForTurnParams{ThreadID: threadID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Thread{ID: id, UserID: userID}, nil
}

func (s Store) OpenTurn(ctx context.Context, turn NewTurn) (OpenedTurn, error) {
	return database.Write(ctx, s.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (OpenedTurn, error) {
		return openTurn(ctx, New(tx), turn)
	})
}

// openTurn writes a turn's first rows inside a transaction the caller has
// open.
func openTurn(ctx context.Context, queries *Queries, turn NewTurn) (OpenedTurn, error) {
	var opened OpenedTurn
	var err error
	if turn.UserContent != nil {
		opened.UserMessageID, err = queries.InsertUserMessage(ctx, InsertUserMessageParams{
			Content: turn.UserContent, ThreadID: turn.ThreadID, AuthorID: &turn.UserID, TurnID: &turn.TurnID,
		})
		// No row answered means the turn id was already used on this
		// thread. Turn ids carry the millisecond, and one thread runs
		// one turn at a time, so this is a bug to surface, not a
		// duplicate to absorb.
		if err != nil {
			return opened, err
		}
		if err := queries.TouchThread(ctx, TouchThreadParams{ThreadID: turn.ThreadID, UserID: turn.UserID}); err != nil {
			return opened, err
		}
	}
	opened.AssistantMessageID, err = queries.InsertAssistantMessage(ctx, InsertAssistantMessageParams{
		Content: turn.AssistantContent, ThreadID: turn.ThreadID, TurnID: &turn.TurnID,
	})
	if err != nil {
		return opened, err
	}
	return opened, queries.MarkResponding(ctx, MarkRespondingParams{ThreadID: turn.ThreadID, UserID: &turn.UserID})
}

func (s Store) MessageContent(ctx context.Context, threadID, messageID int64) ([]byte, error) {
	content, err := New(s.Pool).MessageContent(ctx, MessageContentParams{MessageID: messageID, ThreadID: threadID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return content, err
}

func (s Store) StoreContent(ctx context.Context, messageID int64, content []byte) error {
	_, err := New(s.Pool).StoreMessageContent(ctx, StoreMessageContentParams{Content: content, MessageID: messageID})
	return err
}

func (s Store) CloseTurn(ctx context.Context, turn ClosedTurn) error {
	_, err := database.Write(ctx, s.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
		queries := New(tx)
		if _, err := queries.StoreMessageContent(ctx, StoreMessageContentParams{Content: turn.Content, MessageID: turn.MessageID}); err != nil {
			return struct{}{}, err
		}
		if turn.Usage != nil {
			if err := queries.RecordTokenUsage(ctx, RecordTokenUsageParams{
				PromptTokens: turn.Usage.PromptTokens, CompletionTokens: turn.Usage.CompletionTokens, TotalTokens: turn.Usage.TotalTokens,
				ModelBreakdown: turn.Usage.Breakdown, CallDetails: turn.Usage.Calls,
				ThreadID: &turn.ThreadID, MessageID: &turn.MessageID, UserID: turn.UserID,
			}); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, queries.MarkResponding(ctx, MarkRespondingParams{ThreadID: turn.ThreadID})
	})
	return err
}

func (s Store) History(ctx context.Context, threadID int64) ([]HistoryMessage, error) {
	rows, err := New(s.Pool).HistoryMessages(ctx, threadID)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryMessage, len(rows))
	for i, row := range rows {
		out[i] = HistoryMessage{Assistant: row.Role == "ASSISTANT", Content: row.Content}
	}
	return out, nil
}

func (s Store) AssistantMessages(ctx context.Context, threadID int64) (int64, error) {
	return New(s.Pool).AssistantMessageCount(ctx, threadID)
}

func (s Store) LatestAssistantMessage(ctx context.Context, threadID int64) (int64, error) {
	id, err := New(s.Pool).LatestAssistantMessage(ctx, threadID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s Store) SetTitle(ctx context.Context, userID string, threadID int64, title string) error {
	return New(s.Pool).SetThreadTitle(ctx, SetThreadTitleParams{Title: title, ThreadID: threadID, UserID: userID})
}

// questionRow is the columns the four statements that find a question
// share.
type questionRow struct {
	ID        int64
	Role      string
	TurnID    *string
	CreatedAt time.Time
	Content   []byte
}

func (s Store) Question(ctx context.Context, threadID, fromMessageID int64) (*Question, error) {
	queries := New(s.Pool)
	var row questionRow
	if fromMessageID == 0 {
		latest, err := queries.LatestQuestion(ctx, threadID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		row = questionRow(latest)
	} else {
		named, err := queries.MessageInThread(ctx, MessageInThreadParams{MessageID: fromMessageID, ThreadID: threadID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errNotInThread
		}
		if err != nil {
			return nil, err
		}
		row = questionRow(named)
		if row.Role != "USER" {
			// An answer stands for its turn's question: the one that
			// shares its turn id, else (an answer written by a resumed
			// stretch, which has an id of its own) the newest before it.
			found := false
			if row.TurnID != nil {
				same, err := queries.QuestionOfTurn(ctx, QuestionOfTurnParams{ThreadID: threadID, TurnID: *row.TurnID})
				switch {
				case err == nil:
					row, found = questionRow(same), true
				case !errors.Is(err, pgx.ErrNoRows):
					return nil, err
				}
			}
			if !found {
				before, err := queries.QuestionBefore(ctx, QuestionBeforeParams{ThreadID: threadID, CreatedAt: row.CreatedAt, MessageID: row.ID})
				if errors.Is(err, pgx.ErrNoRows) {
					return nil, nil
				}
				if err != nil {
					return nil, err
				}
				row = questionRow(before)
			}
		}
	}
	question := &Question{ID: row.ID, Content: row.Content, At: row.CreatedAt}
	if row.TurnID != nil {
		question.TurnID = *row.TurnID
	}
	var err error
	question.Replaced, err = queries.MessageIDsFrom(ctx, MessageIDsFromParams{ThreadID: threadID, CreatedAt: row.CreatedAt, MessageID: row.ID})
	return question, err
}

func (s Store) HistoryBefore(ctx context.Context, threadID int64, question *Question) ([]HistoryMessage, error) {
	rows, err := New(s.Pool).HistoryBefore(ctx, HistoryBeforeParams{ThreadID: threadID, CreatedAt: question.At, MessageID: question.ID})
	if err != nil {
		return nil, err
	}
	out := make([]HistoryMessage, len(rows))
	for i, row := range rows {
		out[i] = HistoryMessage{Assistant: row.Role == "ASSISTANT", Content: row.Content}
	}
	return out, nil
}

func (s Store) ReplaceTurn(ctx context.Context, question *Question, turn NewTurn) (OpenedTurn, error) {
	return database.Write(ctx, s.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (OpenedTurn, error) {
		queries := New(tx)
		// The snapshots first: they name the messages by id, and are found
		// by those ids.
		if err := queries.DeleteSnapshotsOfMessages(ctx, DeleteSnapshotsOfMessagesParams{ThreadID: &turn.ThreadID, MessageIds: question.Replaced}); err != nil {
			return OpenedTurn{}, err
		}
		if err := queries.DeleteMessages(ctx, DeleteMessagesParams{ThreadID: turn.ThreadID, MessageIds: question.Replaced}); err != nil {
			return OpenedTurn{}, err
		}
		return openTurn(ctx, queries, turn)
	})
}

func (s Store) Trusted(ctx context.Context, userID, tool string) (bool, error) {
	return New(s.Pool).ToolIsTrusted(ctx, ToolIsTrustedParams{UserID: userID, Tool: tool})
}

func (s Store) Trust(ctx context.Context, userID, tool string) error {
	return New(s.Pool).TrustTool(ctx, TrustToolParams{UserID: userID, Tool: tool})
}
