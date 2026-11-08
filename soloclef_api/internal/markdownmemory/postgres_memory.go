package markdownmemory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrTargetMissing is save_memory's "User not found." / "Workspace not found."
// branch: the row the memory belongs to is gone. Neither route can reach it —
// the user route runs behind an authenticated account and the team route behind
// the workspace gate — but the branch is in the Python service, so it is here.
var ErrTargetMissing = errors.New("markdownmemory: the memory's owner is not there")

// Store reads and writes the two memory columns. The read takes the pool and
// the write takes a transaction, which is the seam: a write reads the stored
// document and replaces it on one pgx.Tx, as the Python service does on one
// session.
type Store struct {
	Pool *pgxpool.Pool
}

// UserMemory is read_memory for the user scope. A row that is not there is ""
// rather than an error, as read_memory returns "" for a missing target.
func (s Store) UserMemory(ctx context.Context, userID string) (string, error) {
	memory, err := New(s.Pool).UserMemory(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return memory, err
}

// TeamMemory is read_memory for the team scope.
func (s Store) TeamMemory(ctx context.Context, workspaceID int64) (string, error) {
	memory, err := New(s.Pool).TeamMemory(ctx, workspaceID)
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

// StoredTeamMemory is the team scope's version.
func StoredTeamMemory(ctx context.Context, tx pgx.Tx, workspaceID int64) (string, error) {
	memory, err := New(tx).TeamMemory(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTargetMissing
	}
	return memory, err
}

// WriteUserMemory stores the rendered document.
func WriteUserMemory(ctx context.Context, tx pgx.Tx, userID, memory string) error {
	return New(tx).SaveUserMemory(ctx, SaveUserMemoryParams{ID: userID, MemoryMd: &memory})
}

// WriteTeamMemory stores the rendered document.
func WriteTeamMemory(ctx context.Context, tx pgx.Tx, workspaceID int64, memory string) error {
	return New(tx).SaveTeamMemory(ctx, SaveTeamMemoryParams{ID: workspaceID, SharedMemoryMd: &memory})
}
