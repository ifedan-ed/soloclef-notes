package notebooks

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Notebook is the part of a `notebooks` row these routes read.
type Notebook struct {
	ID               int64
	Title            string
	Icon             *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ChatGoal         string
	ChatInstructions *string
	ResponseLength   string
	TitleSource      string
	// AutoTitle is the jsonb column as Postgres wrote it, or nil for SQL
	// NULL. It is read in naming.go and nowhere else.
	AutoTitle []byte
	// FeaturedAt is when the owner pinned the notebook, or nil.
	FeaturedAt *time.Time
}

// The statements are free functions over a DBTX rather than methods on a
// store, so the same statement runs on the pool or inside a transaction and
// a route says which by what it passes. Each takes the owner's id, which is
// always the signed-in caller's.

// loadNotebook is the account's notebook, or nil when it has none by that
// id: never made, or somebody else's.
func loadNotebook(ctx context.Context, db DBTX, userID string, notebookID int64) (*Notebook, error) {
	row, err := New(db).NotebookOfOwner(ctx, NotebookOfOwnerParams{ID: notebookID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	notebook := Notebook(row)
	return &notebook, nil
}

// listNotebooks is the account's notebooks, most recently used first.
func listNotebooks(ctx context.Context, db DBTX, userID string) ([]Notebook, error) {
	rows, err := New(db).NotebooksOfOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	notebooks := make([]Notebook, 0, len(rows))
	for _, row := range rows {
		notebooks = append(notebooks, Notebook(row))
	}
	return notebooks, nil
}

// sourceTotals is one notebook's `(all sources, ticked sources)`.
type sourceTotals struct {
	total   int64
	enabled int64
}

// countSources is the totals of each of the account's notebooks named. No
// notebooks, no statement.
func countSources(ctx context.Context, db DBTX, userID string, notebookIDs []int64) (map[int64]sourceTotals, error) {
	counts := map[int64]sourceTotals{}
	if len(notebookIDs) == 0 {
		return counts, nil
	}
	rows, err := New(db).SourceCounts(ctx, SourceCountsParams{NotebookIds: notebookIDs, UserID: userID})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.NotebookID] = sourceTotals{total: row.Total, enabled: row.Enabled}
	}
	return counts, nil
}
