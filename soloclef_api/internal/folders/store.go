package folders

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is Rows over Postgres. Every method is one statement, and so its
// own transaction, except the delete's marker, which reads and then writes
// the same rows and holds them in between.
type Store struct{ Pool *pgxpool.Pool }

var _ Rows = Store{}

// The two constraints a folder write can break, by the names the schema
// gives them (soloclef_schema/migrations).
const (
	siblingNameConstraint = "uq_folders_owner_parent_name"
	parentConstraint      = "folders_parent_owner_fkey"
)

// refused turns the two constraint violations a route answers for into the
// errors it knows, by constraint name and not by searching the message: any
// other violation is a fault and stays one.
func refused(err error) error {
	var violation *pgconn.PgError
	if errors.As(err, &violation) {
		switch violation.ConstraintName {
		case siblingNameConstraint:
			return ErrNameTaken
		case parentConstraint:
			return ErrParentGone
		}
	}
	return err
}

func (s Store) Folder(ctx context.Context, userID string, folderID int64) (*Folder, error) {
	row, err := New(s.Pool).FolderOwned(ctx, FolderOwnedParams{FolderID: folderID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	found := Folder(row)
	return &found, nil
}

func (s Store) Folders(ctx context.Context, userID string) ([]Folder, error) {
	rows, err := New(s.Pool).FoldersOwned(ctx, userID)
	if err != nil {
		return nil, err
	}
	tree := make([]Folder, len(rows))
	for index, row := range rows {
		tree[index] = Folder(row)
	}
	return tree, nil
}

func (s Store) Ancestors(ctx context.Context, userID string, folderID int64) ([]Breadcrumb, error) {
	rows, err := New(s.Pool).FolderAncestors(ctx, FolderAncestorsParams{FolderID: folderID, UserID: userID})
	if err != nil {
		return nil, err
	}
	chain := make([]Breadcrumb, len(rows))
	for index, row := range rows {
		chain[index] = Breadcrumb(row)
	}
	return chain, nil
}

func (s Store) Depth(ctx context.Context, userID string, folderID int64) (int, error) {
	depth, err := New(s.Pool).FolderDepth(ctx, FolderDepthParams{FolderID: folderID, UserID: userID})
	return int(depth), err
}

func (s Store) SubtreeDepth(ctx context.Context, userID string, folderID int64) (int, error) {
	depth, err := New(s.Pool).SubtreeDepth(ctx, SubtreeDepthParams{FolderID: folderID, UserID: userID})
	return int(depth), err
}

func (s Store) LastSiblingPosition(ctx context.Context, userID string, parentID *int64) (*string, error) {
	position, err := New(s.Pool).LastSiblingPosition(ctx,
		LastSiblingPositionParams{UserID: userID, ParentID: parentID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &position, nil
}

func (s Store) Insert(ctx context.Context, userID string, folder Folder) (Folder, error) {
	row, err := New(s.Pool).InsertFolder(ctx, InsertFolderParams{
		UserID:    userID,
		Name:      folder.Name,
		Position:  folder.Position,
		ParentID:  folder.ParentID,
		CreatedAt: folder.CreatedAt,
		UpdatedAt: folder.UpdatedAt,
	})
	if err != nil {
		return Folder{}, refused(err)
	}
	return Folder(row), nil
}

func (s Store) Rename(ctx context.Context, userID string, folderID int64, name string, at time.Time) error {
	_, err := New(s.Pool).RenameFolder(ctx, RenameFolderParams{
		FolderID: folderID, UserID: userID, Name: name, UpdatedAt: at})
	return refused(err)
}

func (s Store) Reparent(ctx context.Context, userID string, folderID int64, parentID *int64, position string, at time.Time) error {
	_, err := New(s.Pool).ReparentFolder(ctx, ReparentFolderParams{
		FolderID: folderID, UserID: userID, ParentID: parentID, Position: position, UpdatedAt: at})
	return refused(err)
}

func (s Store) Reposition(ctx context.Context, userID string, folderID int64, position string, at time.Time) error {
	_, err := New(s.Pool).RepositionFolder(ctx, RepositionFolderParams{
		FolderID: folderID, UserID: userID, Position: position, UpdatedAt: at})
	return err
}

func (s Store) ClearWatched(ctx context.Context, userID string, folderID int64, at time.Time) error {
	_, err := New(s.Pool).ClearWatchedFlag(ctx, ClearWatchedFlagParams{
		FolderID: folderID, UserID: userID, UpdatedAt: at})
	return err
}

func (s Store) Documents(ctx context.Context, userID string, documentIDs []int64) ([]Document, error) {
	rows, err := New(s.Pool).DocumentsOwned(ctx, DocumentsOwnedParams{UserID: userID, DocumentIds: documentIDs})
	if err != nil {
		return nil, err
	}
	found := make([]Document, len(rows))
	for index, row := range rows {
		found[index] = Document(row)
	}
	return found, nil
}

func (s Store) MoveDocuments(ctx context.Context, userID string, documentIDs []int64, folderID *int64) error {
	return New(s.Pool).MoveDocuments(ctx, MoveDocumentsParams{
		UserID: userID, DocumentIds: documentIDs, FolderID: folderID})
}

// MarkSubtreeDeleting reads the subtree, locks its documents, and marks
// them, in one transaction: the statuses it returns are exactly the ones
// the marker replaced, because nothing could change a locked row in
// between.
func (s Store) MarkSubtreeDeleting(ctx context.Context, userID string, folderID int64) (Marked, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Marked{}, err
	}
	// A no-op after Commit; on any early return it undoes everything.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	queries := New(tx)

	var marked Marked
	marked.SubtreeIDs, err = queries.FolderSubtreeIDs(ctx, FolderSubtreeIDsParams{FolderID: folderID, UserID: userID})
	if err != nil {
		return Marked{}, err
	}
	rows, err := queries.LockDocumentsInFolders(ctx,
		LockDocumentsInFoldersParams{UserID: userID, FolderIds: marked.SubtreeIDs})
	if err != nil {
		return Marked{}, err
	}
	marked.Documents = make([]MarkedDocument, len(rows))
	for index, row := range rows {
		marked.Documents[index] = MarkedDocument(row)
	}
	if len(rows) > 0 {
		err = queries.MarkDocumentsDeleting(ctx,
			MarkDocumentsDeletingParams{UserID: userID, DocumentIds: marked.DocumentIDs()})
		if err != nil {
			return Marked{}, err
		}
	}
	return marked, tx.Commit(ctx)
}

func (s Store) RestoreMarked(ctx context.Context, userID string, marked Marked) error {
	if len(marked.Documents) == 0 {
		return nil
	}
	// One object of id to status, so every document is restored by one
	// statement however many there are.
	statuses := make(map[string]json.RawMessage, len(marked.Documents))
	for _, document := range marked.Documents {
		statuses[strconv.FormatInt(document.ID, 10)] = document.Status
	}
	encoded, err := json.Marshal(statuses)
	if err != nil {
		return err
	}
	return New(s.Pool).RestoreDocumentStatuses(ctx,
		RestoreDocumentStatusesParams{UserID: userID, Statuses: encoded})
}
