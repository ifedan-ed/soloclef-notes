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

// NewLink is a public link to write.
type NewLink struct {
	ThreadID int64
	// UserID is the thread's owner, and so the link's.
	UserID     string
	Token      string
	Hash       string
	Data       []byte
	MessageIDs []int64
	ExpiresAt  *time.Time
}

// Link is a public link as written or found.
type Link struct {
	ID        int64
	Token     string
	ExpiresAt *time.Time
	// IsNew is false when the thread already had a link to this content.
	IsNew bool
	// CreatedAt and Messages are set by the list.
	CreatedAt time.Time
	Messages  int64
}

// Podcast is a finished podcast as a public link embeds it.
type Podcast struct {
	ID         int64
	Title      string
	Transcript []byte
	ArtifactID *int64
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

// AccountName is an account's display name: nil if it has none, and found
// false if the account is gone.
func (s Store) AccountName(ctx context.Context, userID string) (name *string, found bool, err error) {
	name, err = New(s.Pool).AccountName(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return name, err == nil, err
}

// ReadyPodcasts is the finished podcasts among ids that are the account's
// own, by id.
func (s Store) ReadyPodcasts(ctx context.Context, userID string, ids []int64) (map[int64]Podcast, error) {
	out := map[int64]Podcast{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := New(s.Pool).ReadyPodcasts(ctx, ReadyPodcastsParams{PodcastIds: ids, UserID: userID})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = Podcast{ID: row.ID, Title: row.Title, Transcript: row.PodcastTranscript, ArtifactID: row.ArtifactID}
	}
	return out, nil
}

// OwnedArtifacts is the ids among ids that are the account's own artifacts,
// ascending.
func (s Store) OwnedArtifacts(ctx context.Context, userID string, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return []int64{}, nil
	}
	return New(s.Pool).OwnedArtifacts(ctx, OwnedArtifactsParams{ArtifactIds: ids, UserID: userID})
}

// SaveLink writes a public link, or finds the thread's live link to the
// same content and gives it the new expiry.
func (s Store) SaveLink(ctx context.Context, link NewLink) (Link, error) {
	return database.Write(ctx, s.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (Link, error) {
		queries := New(tx)
		if err := queries.DeleteExpiredLink(ctx, DeleteExpiredLinkParams{ThreadID: &link.ThreadID, UserID: link.UserID, ContentHash: link.Hash}); err != nil {
			return Link{}, err
		}
		row, err := queries.UpsertLink(ctx, UpsertLinkParams{
			ThreadID: link.ThreadID, UserID: link.UserID, ExpiresAt: link.ExpiresAt, ShareToken: link.Token,
			ContentHash: link.Hash, SnapshotData: link.Data, MessageIds: link.MessageIDs,
		})
		if err != nil {
			return Link{}, err
		}
		return Link{ID: row.ID, Token: row.ShareToken, ExpiresAt: row.ExpiresAt, IsNew: row.IsNew}, nil
	})
}

// Links is the live links of the account's thread, newest first.
func (s Store) Links(ctx context.Context, userID string, threadID int64) ([]Link, error) {
	rows, err := New(s.Pool).ThreadLinks(ctx, ThreadLinksParams{ThreadID: &threadID, UserID: userID})
	if err != nil {
		return nil, err
	}
	out := make([]Link, len(rows))
	for i, row := range rows {
		out[i] = Link{ID: row.ID, Token: row.ShareToken, CreatedAt: row.CreatedAt, Messages: row.MessageCount}
	}
	return out, nil
}

// DeleteLink takes down one link of the account's thread, and reports
// whether there was one: false for a link that is not there, is of another
// thread, or is another account's.
func (s Store) DeleteLink(ctx context.Context, userID string, threadID, linkID int64) (bool, error) {
	deleted, err := New(s.Pool).DeleteLink(ctx, DeleteLinkParams{LinkID: linkID, ThreadID: &threadID, UserID: userID})
	return deleted > 0, err
}
