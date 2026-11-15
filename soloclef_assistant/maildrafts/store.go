package maildrafts

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Rows is the guard's rows. Every method takes the owner and every
// statement names it (queries.sql).
type Rows interface {
	// Insert stores a new draft. With replaces set, that draft of the
	// owner's, if still waiting, is marked superseded by this one in the
	// same transaction; ErrNoDraft when it is not one that can be.
	Insert(ctx context.Context, ownerID string, draft Draft, replaces string) error
	// Draft is one of the owner's drafts, or ErrNoDraft.
	Draft(ctx context.Context, ownerID, draftID string) (Draft, error)
	// Issue stores a go-ahead's hash for a draft's content, with the codes
	// of the unusual points it was issued with, after removing the
	// owner's spent ones.
	Issue(ctx context.Context, ownerID, draftID string, tokenHash []byte, contentHash string, unusual []string, now, expires time.Time) error
	// SentSince is how many sends the owner began after since.
	SentSince(ctx context.Context, ownerID string, since time.Time) (int, error)
	// BeginSend locks the owner and the go-ahead, reads what admit judges,
	// and, if admit passes, spends the go-ahead and marks the draft
	// sending, all in one transaction. It returns the draft as it was
	// admitted: the message that will be sent.
	BeginSend(ctx context.Context, ownerID, draftID string, tokenHash []byte, presentedHash string, acknowledged []string, now time.Time) (Draft, error)
	// FinishSend records how the send ended and writes its activity entry.
	FinishSend(ctx context.Context, ownerID string, finished Finished) error
	// Discard marks a waiting draft discarded; false when it was not one.
	Discard(ctx context.Context, ownerID, draftID string) (bool, error)
	// Waiting is up to most of the owner's drafts that still wait at now,
	// newest first (waiting.go).
	Waiting(ctx context.Context, ownerID string, now time.Time, most int) ([]WaitingDraft, error)
}

// Finished is how a send ended.
type Finished struct {
	Draft             Draft
	ProviderMessageID string
	// Failure is "" for a send the server accepted.
	Failure string
	At      time.Time
}

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

func optional(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}

func (s Store) Insert(ctx context.Context, ownerID string, draft Draft, replaces string) error {
	encoded, err := draft.Message.encoded()
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		queries := New(tx)
		if err := queries.InsertDraft(ctx, InsertDraftParams{
			ID: draft.ID, UserID: ownerID, Provider: draft.Provider, JmapAccountID: optional(draft.AccountID),
			Message: encoded, ContentHash: draft.StoredHash, ExpiresAt: draft.ExpiresAt,
		}); err != nil {
			return err
		}
		if replaces == "" {
			return nil
		}
		if !isUUID(replaces) {
			return ErrNoDraft
		}
		superseded, err := queries.SupersedeDraft(ctx, SupersedeDraftParams{SupersededBy: &draft.ID, ID: replaces, UserID: ownerID})
		if err == nil && superseded == 0 {
			return ErrNoDraft
		}
		return err
	})
}

// fromRow is a draft from a row of either query that reads one.
func fromRow(row DraftOfOwnerRow) (Draft, error) {
	message, err := decodeMessage(row.Message)
	if err != nil {
		return Draft{}, err
	}
	text := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	return Draft{ID: row.ID, Provider: row.Provider, AccountID: text(row.JmapAccountID), Message: message,
		StoredHash: row.ContentHash, Status: row.Status, SupersededBy: text(row.SupersededBy), Failure: text(row.Failure),
		ProviderMessageID: text(row.ProviderMessageID), CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt, SentAt: row.SentAt}, nil
}

// isUUID guards an id before it reaches a uuid column: anything else is
// simply not a draft.
func isUUID(text string) bool {
	if len(text) != 36 {
		return false
	}
	for index, r := range text {
		switch {
		case index == 8 || index == 13 || index == 18 || index == 23:
			if r != '-' {
				return false
			}
		case !('0' <= r && r <= '9' || 'a' <= r && r <= 'f' || 'A' <= r && r <= 'F'):
			return false
		}
	}
	return true
}

func (s Store) Draft(ctx context.Context, ownerID, draftID string) (Draft, error) {
	if !isUUID(draftID) {
		return Draft{}, ErrNoDraft
	}
	row, err := New(s.Pool).DraftOfOwner(ctx, DraftOfOwnerParams{ID: draftID, UserID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrNoDraft
	}
	if err != nil {
		return Draft{}, err
	}
	return fromRow(row)
}

func (s Store) Issue(ctx context.Context, ownerID, draftID string, tokenHash []byte, contentHash string, unusual []string, now, expires time.Time) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		queries := New(tx)
		if err := queries.ForgetSpentConfirmations(ctx, ForgetSpentConfirmationsParams{UserID: ownerID, Now: now}); err != nil {
			return err
		}
		if unusual == nil {
			unusual = []string{}
		}
		return queries.InsertConfirmation(ctx, InsertConfirmationParams{TokenHash: tokenHash, DraftID: draftID, UserID: ownerID,
			ContentHash: contentHash, Unusual: unusual, CreatedAt: now, ExpiresAt: expires})
	})
}

func (s Store) SentSince(ctx context.Context, ownerID string, since time.Time) (int, error) {
	sent, err := New(s.Pool).SendsSince(ctx, SendsSinceParams{UserID: ownerID, Since: &since})
	return int(sent), err
}

func (s Store) BeginSend(ctx context.Context, ownerID, draftID string, tokenHash []byte, presentedHash string, acknowledged []string, now time.Time) (Draft, error) {
	if !isUUID(draftID) {
		return Draft{}, ErrNoDraft
	}
	var admitted Draft
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		queries := New(tx)
		// The owner's row first: an owner's sends queue here, so the
		// count of the past hour each reads (the pace point) includes the
		// one before it.
		if _, err := queries.LockOwner(ctx, ownerID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNoDraft
			}
			return err
		}
		row, err := queries.LockDraftOfOwner(ctx, LockDraftOfOwnerParams{ID: draftID, UserID: ownerID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoDraft
		}
		if err != nil {
			return err
		}
		draft, err := fromRow(DraftOfOwnerRow(row))
		if err != nil {
			// A stored message that no longer reads as one has changed.
			return ErrDraftChanged
		}
		judged := admission{OwnerID: ownerID, DraftID: draftID, PresentedHash: presentedHash, Acknowledged: acknowledged, Draft: draft, Now: now}
		held, err := queries.LockConfirmation(ctx, tokenHash)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		default:
			judged.Confirmation = &confirmation{DraftID: held.DraftID, OwnerID: held.UserID, ContentHash: held.ContentHash,
				ExpiresAt: held.ExpiresAt, Used: held.UsedAt != nil, Unusual: held.Unusual}
		}
		hourAgo := now.Add(-time.Hour)
		sent, err := queries.SendsSince(ctx, SendsSinceParams{UserID: ownerID, Since: &hourAgo})
		if err != nil {
			return err
		}
		judged.SentLastHour = int(sent)
		if err := admit(judged); err != nil {
			return err
		}
		if spent, err := queries.SpendConfirmation(ctx, SpendConfirmationParams{UsedAt: &now, TokenHash: tokenHash}); err != nil || spent != 1 {
			if err == nil {
				err = ErrConfirmationSpent
			}
			return err
		}
		if begun, err := queries.BeginSending(ctx, BeginSendingParams{SendingAt: &now, ID: draftID, UserID: ownerID}); err != nil || begun != 1 {
			if err == nil {
				err = ErrDraftNotPending
			}
			return err
		}
		admitted = draft
		return nil
	})
	return admitted, err
}

func (s Store) FinishSend(ctx context.Context, ownerID string, finished Finished) error {
	entry := activityOf(finished)
	metadata, err := json.Marshal(entry.metadata)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		queries := New(tx)
		if finished.Failure == "" {
			if _, err := queries.FinishSent(ctx, FinishSentParams{SentAt: &finished.At,
				ProviderMessageID: optional(finished.ProviderMessageID), ID: finished.Draft.ID, UserID: ownerID}); err != nil {
				return err
			}
		} else if _, err := queries.FinishFailed(ctx, FinishFailedParams{Failure: &finished.Failure, ID: finished.Draft.ID, UserID: ownerID}); err != nil {
			return err
		}
		return queries.WriteActivity(ctx, WriteActivityParams{UserID: ownerID, Level: Loglevel(entry.level), Status: Logstatus(entry.status),
			Message: entry.message, Source: activitySource, LogMetadata: metadata, CreatedAt: finished.At})
	})
}

func (s Store) Discard(ctx context.Context, ownerID, draftID string) (bool, error) {
	if !isUUID(draftID) {
		return false, nil
	}
	discarded, err := New(s.Pool).DiscardDraft(ctx, DiscardDraftParams{ID: draftID, UserID: ownerID})
	return discarded == 1, err
}
