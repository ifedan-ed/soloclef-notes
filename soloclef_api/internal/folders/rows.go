package folders

import (
	"context"
	"errors"
	"time"
)

// Rows is the folders and documents of one account, as the routes read and
// write them. Every method takes the account first, and none can reach a
// row of another: that is the whole of this package's access control, and it
// is why no route checks ownership after a read.
//
// Each write is stored when its method returns. A method that finds nothing
// to change (the folder was deleted a moment ago) reports nothing; the
// route has already answered for a row it read, and the next read says it
// is gone.
type Rows interface {
	// Folder is one folder, or nil when the account has no such folder.
	Folder(ctx context.Context, userID string, folderID int64) (*Folder, error)
	// Folders is the account's whole tree, flat, ordered by position and
	// then id.
	Folders(ctx context.Context, userID string) ([]Folder, error)
	// Ancestors is the chain from the root down to the folder itself.
	Ancestors(ctx context.Context, userID string, folderID int64) ([]Breadcrumb, error)
	// Depth is how deep the folder sits: 1 at the top level.
	Depth(ctx context.Context, userID string, folderID int64) (int, error)
	// SubtreeDepth is how many levels hang below the folder: 0 for a leaf.
	SubtreeDepth(ctx context.Context, userID string, folderID int64) (int, error)
	// LastSiblingPosition is the last position among the folders under the
	// parent (nil for the top level), or nil when there are none.
	LastSiblingPosition(ctx context.Context, userID string, parentID *int64) (*string, error)

	// Insert stores a new folder (its ID is ignored) and returns it as
	// stored. ErrNameTaken and ErrParentGone are its two refusals.
	Insert(ctx context.Context, userID string, folder Folder) (Folder, error)
	// Rename can refuse with ErrNameTaken.
	Rename(ctx context.Context, userID string, folderID int64, name string, at time.Time) error
	// Reparent can refuse with ErrNameTaken or ErrParentGone.
	Reparent(ctx context.Context, userID string, folderID int64, parentID *int64, position string, at time.Time) error
	Reposition(ctx context.Context, userID string, folderID int64, position string, at time.Time) error
	// ClearWatched turns the folder's `watched` flag off if it is on.
	ClearWatched(ctx context.Context, userID string, folderID int64, at time.Time) error

	// Documents is the documents among these ids that are the account's,
	// in id order.
	Documents(ctx context.Context, userID string, documentIDs []int64) ([]Document, error)
	// MoveDocuments files the documents in the folder, or in none.
	MoveDocuments(ctx context.Context, userID string, documentIDs []int64, folderID *int64) error

	// MarkSubtreeDeleting marks every document in the folder's subtree as
	// being deleted, and returns what a deletion job needs and what undoing
	// the marker needs.
	MarkSubtreeDeleting(ctx context.Context, userID string, folderID int64) (Marked, error)
	// RestoreMarked undoes MarkSubtreeDeleting for a job that was never
	// queued.
	RestoreMarked(ctx context.Context, userID string, marked Marked) error
}

// Marked is a folder delete's marker, as written.
type Marked struct {
	// SubtreeIDs is the folder and every folder under it, the folder first.
	SubtreeIDs []int64
	// Documents is the documents that were marked, in id order, each with
	// the status it held before.
	Documents []MarkedDocument
}

// MarkedDocument is one document a folder delete marked.
type MarkedDocument struct {
	ID int64
	// Status is the status the marker replaced, as stored.
	Status []byte
}

// DocumentIDs is the marked documents' ids, in order.
func (m Marked) DocumentIDs() []int64 {
	ids := make([]int64, len(m.Documents))
	for index, document := range m.Documents {
		ids[index] = document.ID
	}
	return ids
}

var (
	// ErrNameTaken: the parent already has a folder of this name.
	ErrNameTaken = errors.New("folders: a sibling folder already has this name")
	// ErrParentGone: the parent named for a folder is not there, because it
	// was deleted between the route's read of it and the write.
	ErrParentGone = errors.New("folders: the parent folder is no longer there")
)
