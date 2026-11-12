package markdownmemory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrTargetMissing means the account the memory belongs to is gone. The routes
// run behind an authenticated account, so only an account erased between the
// gate and the write can reach it.
var ErrTargetMissing = errors.New("markdownmemory: the memory's owner is not there")

// Store reads and writes the account's memory column. The read takes the pool and
// the write takes a transaction, which is the seam: a write reads the stored
// document and replaces it on one pgx.Tx, as the Python service does on one
// session.
type Store struct {
	Pool *pgxpool.Pool
}

// UserMemory is the account's memory. A row that is not there is ""
// rather than an error, as read_memory returns "" for a missing target.
func (s Store) UserMemory(ctx context.Context, userID string) (string, error) {
	memory, err := New(s.Pool).UserMemory(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return memory, err
}

// StoredUserMemory is the same read inside a write's transaction, where a
// missing row is the refusal rather than "".
func StoredUserMemory(ctx context.Context, tx pgx.Tx, userID string) (string, error) {
	memory, err := New(tx).UserMemory(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTargetMissing
	}
	return memory, err
}

// WriteUserMemory stores the rendered document.
func WriteUserMemory(ctx context.Context, tx pgx.Tx, userID, memory string) error {
	return New(tx).SaveUserMemory(ctx, SaveUserMemoryParams{ID: userID, MemoryMd: memory})
}
