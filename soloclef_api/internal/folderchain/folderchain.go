// Package folderchain finds or makes a chain of nested folders inside a
// transaction that is writing documents into them.
//
// Two routes bring a folder tree in together with its files: the Nextcloud
// import (internal/nextcloudimport) and the folder upload
// (internal/uploads). Both need "the folder a/b/c under this parent, made
// if it is not there", both must survive another request making the same
// folder at the same moment, and both must place a new folder where the
// folder tree itself would. That is one rule, so it is written once, here.
//
// # Two requests, one folder
//
// The web app uploads a folder as several requests at once, and each of
// them needs the same top folder. The table's unique constraint on (owner,
// parent, name) lets exactly one of them insert it. The loser's insert runs
// under a savepoint, so the unique violation undoes that insert and not the
// transaction around it, and the loser then reads the winner's row. Without
// that, two requests each select, find nothing and insert, and one of them
// fails with its whole batch.
//
// # Whose folders
//
// Every function takes the account the folders belong to, and both
// statements name it: a chain is found and made among that account's
// folders only, and `under` must be one of them (the table's composite
// foreign key refuses a parent of another account).
package folderchain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folders"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/fractionalindex"
)

// uniqueViolation is Postgres's code for a row that breaks a unique index.
const uniqueViolation = "23505"

// Ensure makes sure the folders named by chain exist, each inside the one
// before it and the first inside `under` (nil for the top level), and
// returns the innermost folder's id. An empty chain returns `under`.
//
// The names are used as given: the caller has already made each one a name
// a folder may carry. A new folder goes after its last sibling, by the
// folder tree's own statement and key generator (internal/folders,
// internal/fractionalindex), so a folder made here sorts exactly as one
// made in the tree.
func Ensure(ctx context.Context, tx pgx.Tx, userID string, under *int64, chain []string, now time.Time) (*int64, error) {
	parent := under
	for _, name := range chain {
		id, _, err := EnsureOne(ctx, tx, userID, parent, name, now)
		if err != nil {
			return nil, err
		}
		parent = &id
	}
	return parent, nil
}

// EnsureOne is one link of the chain: the folder called name inside parent,
// and whether this call made it.
func EnsureOne(ctx context.Context, tx pgx.Tx, userID string, parent *int64, name string, now time.Time) (id int64, made bool, err error) {
	named := FolderNamedParams{UserID: userID, Name: name, ParentID: parent}
	id, err = New(tx).FolderNamed(ctx, named)
	if errors.Is(err, pgx.ErrNoRows) {
		id, err = insert(ctx, tx, userID, name, parent, now)
		made = err == nil
		var refused *pgconn.PgError
		if errors.As(err, &refused) && refused.Code == uniqueViolation {
			// Somebody made the same folder in the meantime. The insert
			// ran under a savepoint, so the transaction is still usable,
			// and the folder is there to be read.
			id, err = New(tx).FolderNamed(ctx, named)
		}
	}
	if err != nil {
		return 0, false, fmt.Errorf("the folder %q: %w", name, err)
	}
	return id, made, nil
}

// insert inserts one folder under a savepoint, so that a unique violation
// undoes the insert and not the whole transaction.
func insert(ctx context.Context, tx pgx.Tx, userID string, name string, parent *int64, now time.Time) (int64, error) {
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = savepoint.Rollback(ctx) }()

	var last *string
	position, err := folders.New(savepoint).LastSiblingPosition(ctx, folders.LastSiblingPositionParams{UserID: userID, ParentID: parent})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return 0, err
	default:
		last = &position
	}
	key, err := fractionalindex.KeyBetween(last, nil)
	if err != nil {
		return 0, err
	}
	row, err := folders.New(savepoint).InsertFolder(ctx, folders.InsertFolderParams{
		UserID: userID, Name: name, Position: key, ParentID: parent, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return 0, err
	}
	return row.ID, savepoint.Commit(ctx)
}
