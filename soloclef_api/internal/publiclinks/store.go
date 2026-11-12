package publiclinks

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the two statements over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

// Links is every link the account has made, newest first.
func (s Store) Links(ctx context.Context, userID string) ([]Link, error) {
	rows, err := New(s.Pool).LinksOfOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	links := make([]Link, 0, len(rows))
	for _, row := range rows {
		links = append(links, Link{
			ID:               row.ID,
			ShareToken:       row.ShareToken,
			CreatedAt:        row.CreatedAt,
			ExpiresAt:        row.ExpiresAt,
			OwnerID:          row.UserID,
			ThreadID:         row.ThreadID,
			NotebookOutputID: row.NotebookOutputID,
			NotebookID:       row.NotebookID,
			StoredTitle:      row.StoredTitle,
			ThreadTitle:      row.ThreadTitle,
			Messages:         row.Messages,
		})
	}
	return links, nil
}

// Revoke takes the account's link down and reports whether there was one:
// false is a link that does not exist or is somebody else's.
//
// One statement on the pool, not a transaction: the guarded DELETE is the
// read, the check and the write at once, so there is nothing for a
// transaction to hold together, and nothing is owed after it (no event, no
// queue message) that would need the write seam's effects.
func (s Store) Revoke(ctx context.Context, userID string, linkID int64) (bool, error) {
	removed, err := New(s.Pool).RevokeLinkOfOwner(ctx, RevokeLinkOfOwnerParams{ID: linkID, UserID: userID})
	return removed == 1, err
}
