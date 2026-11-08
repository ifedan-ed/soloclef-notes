package folders

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store reads the folders and the documents inside them. The read methods take
// the pool and the write methods take a pgx.Tx, so a slice cannot run a write
// statement outside the transaction that is meant to hold it
// (internal/database).
type Store struct{ Pool *pgxpool.Pool }

// Folder is `session.get(Folder, id)`: nil for a row that is not there.
func (s Store) Folder(ctx context.Context, folderID int64) (*Folder, error) {
	row, err := New(s.Pool).FolderByID(ctx, folderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	found := Folder(row)
	return &found, nil
}

// Ancestors is the chain from the root down to the folder itself, which is
// both the breadcrumb and the set move_folder's circular-reference check asks
// about.
func (s Store) Ancestors(ctx context.Context, folderID int64) ([]Breadcrumb, error) {
	rows, err := New(s.Pool).FolderAncestors(ctx, folderID)
	if err != nil {
		return nil, err
	}
	chain := make([]Breadcrumb, 0, len(rows))
	for _, row := range rows {
		chain = append(chain, Breadcrumb{ID: row.ID, Name: row.Name})
	}
	return chain, nil
}

// Depth is get_folder_depth: a root folder is 1, and a folder that is not
// there is 0 — which is Python's `result.scalar() or 0`.
func (s Store) Depth(ctx context.Context, folderID int64) (int, error) {
	depth, err := New(s.Pool).FolderDepth(ctx, folderID)
	return int(depth), err
}

// SubtreeDepth is get_subtree_max_depth: how many levels hang below a folder,
// 0 for a leaf.
func (s Store) SubtreeDepth(ctx context.Context, folderID int64) (int, error) {
	deepest, err := New(s.Pool).SubtreeMaxDepth(ctx, &folderID)
	if err != nil {
		return 0, err
	}
	// The query answers -1 for a folder with no children at all, and the
	// Python helper turns anything from 0 upwards into one more than itself.
	if deepest < 0 {
		return 0, nil
	}
	return int(deepest) + 1, nil
}

// SubtreeIDs is the folder and everything under it, in the worktable's own
// order, which is the order the enqueued message carries.
func (s Store) SubtreeIDs(ctx context.Context, folderID int64) ([]int64, error) {
	return New(s.Pool).FolderSubtreeIDs(ctx, folderID)
}

// DocumentsToDelete is delete_folder's document read.
func (s Store) DocumentsToDelete(ctx context.Context, folderIDs []int64) ([]int64, error) {
	return New(s.Pool).DocumentsInFoldersNotDeleting(ctx, folderIDs)
}

// NextPosition is generate_folder_position's append case: a key after the last
// sibling, or the first key of all when there is no sibling.
//
// Returning the sibling's position rather than the key itself keeps the one
// place that calls fractionalindex in one file (position.go), so the two
// branches of the Python helper stay recognisable.
func (s Store) LastSiblingPosition(ctx context.Context, workspaceID int64, parentID *int64) (*string, error) {
	position, err := New(s.Pool).LastSiblingPosition(ctx,
		LastSiblingPositionParams{WorkspaceID: workspaceID, ParentID: parentID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &position, nil
}

// Insert writes the new folder inside the caller's transaction.
func Insert(ctx context.Context, tx pgx.Tx, folder Folder) (Folder, error) {
	row, err := New(tx).InsertFolder(ctx, InsertFolderParams{
		Name:        folder.Name,
		Position:    folder.Position,
		ParentID:    folder.ParentID,
		WorkspaceID: folder.WorkspaceID,
		CreatedByID: folder.CreatedByID,
		CreatedAt:   folder.CreatedAt,
		UpdatedAt:   folder.UpdatedAt,
	})
	if err != nil {
		return Folder{}, err
	}
	return Folder(row), nil
}

// Rename, Reparent, Reposition and SetMetadata are the four flushes, each
// carrying the `updated_at` the ORM's onupdate would have written. A route
// calls one only when a column's value actually changes, because SQLAlchemy
// emits no UPDATE otherwise and so leaves `updated_at` alone — which is
// invisible in every response and visible in a row diff.
func Rename(ctx context.Context, tx pgx.Tx, folderID int64, name string, at time.Time) error {
	return New(tx).RenameFolder(ctx, RenameFolderParams{ID: folderID, Name: name, UpdatedAt: at})
}

func Reparent(ctx context.Context, tx pgx.Tx, folderID int64, parentID *int64, position string, at time.Time) error {
	return New(tx).ReparentFolder(ctx, ReparentFolderParams{
		ID: folderID, ParentID: parentID, Position: position, UpdatedAt: at})
}

func Reposition(ctx context.Context, tx pgx.Tx, folderID int64, position string, at time.Time) error {
	return New(tx).RepositionFolder(ctx, RepositionFolderParams{
		ID: folderID, Position: position, UpdatedAt: at})
}

func SetMetadata(ctx context.Context, tx pgx.Tx, folderID int64, metadata []byte, at time.Time) error {
	return New(tx).SetFolderMetadata(ctx, SetFolderMetadataParams{
		ID: folderID, Column2: metadata, UpdatedAt: at})
}

// MarkDeleting and MarkReady are the delete's marker and its compensation.
func MarkDeleting(ctx context.Context, tx pgx.Tx, documentIDs []int64) error {
	return New(tx).MarkDocumentsDeleting(ctx, documentIDs)
}

func MarkReady(ctx context.Context, tx pgx.Tx, documentIDs []int64) error {
	return New(tx).MarkDocumentsReady(ctx, documentIDs)
}
