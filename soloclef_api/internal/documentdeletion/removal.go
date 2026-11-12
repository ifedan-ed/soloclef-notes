package documentdeletion

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// StoredFile is one file of a document, and the store it was written to.
type StoredFile struct {
	Backend string
	Key     string
}

// Rows is the database as a removal uses it.
type Rows interface {
	// Mark sets the document "deleting". False means there is no such
	// document.
	Mark(ctx context.Context, documentID int64) (bool, error)
	// ChunkIDs is up to `batch` of the document's chunk ids.
	ChunkIDs(ctx context.Context, documentID int64, batch int) ([]int64, error)
	DeleteChunks(ctx context.Context, documentID int64, ids []int64) error
	Files(ctx context.Context, documentID int64) ([]StoredFile, error)
	// DeleteDocument deletes the marked row and everything that cascades
	// from it.
	DeleteDocument(ctx context.Context, documentID int64) error
}

// Stores finds the store a file's row says it was written to
// (*filestore.Stores).
type Stores interface {
	Named(backend string) (filestore.Store, error)
}

// chunkBatch is how many chunks one round removes: one request to the
// vector store and one DELETE. A thousand ids are a few kilobytes of JSON
// and a statement that holds its locks for milliseconds, and a long
// document is a handful of rounds.
const chunkBatch = 1000

// Removal removes a document and everything stored for it.
type Removal struct {
	Rows    Rows
	Vectors Vectors
	Files   Stores
}

// Remove deletes the document. It returns nil when the document is gone,
// including when it was already gone.
//
// The order is what makes a failure at any point harmless and a retry
// complete:
//
//   - The marker is first, and committed on its own, so search stops
//     returning the document before anything of it is removed, and the
//     indexing worker stops writing to it. Otherwise a search could return
//     a chunk whose row is deleted a moment later.
//   - A batch's points go before its rows. The points are found from the
//     rows (a point's id is its chunk's id), so rows deleted first would
//     leave points nobody could find again.
//   - The files go before the document row, for the same reason: the row
//     (through its file rows, which cascade with it) is the only record of
//     each file's key. A removal that stopped after the row was gone would
//     leave objects in the store that nothing names.
//   - The document row is last. While it exists, the delete can be asked
//     for again and carries on.
func (r Removal) Remove(ctx context.Context, documentID int64) error {
	found, err := r.Rows.Mark(ctx, documentID)
	if err != nil {
		return fmt.Errorf("marking document %d: %w", documentID, err)
	}
	if !found {
		return nil
	}

	for {
		ids, err := r.Rows.ChunkIDs(ctx, documentID, chunkBatch)
		if err != nil {
			return fmt.Errorf("reading the chunks of document %d: %w", documentID, err)
		}
		if len(ids) == 0 {
			break
		}
		if err := r.Vectors.Delete(ctx, ids); err != nil {
			return fmt.Errorf("removing %d vectors of document %d: %w", len(ids), documentID, err)
		}
		if err := r.Rows.DeleteChunks(ctx, documentID, ids); err != nil {
			return fmt.Errorf("deleting %d chunks of document %d: %w", len(ids), documentID, err)
		}
	}

	files, err := r.Rows.Files(ctx, documentID)
	if err != nil {
		return fmt.Errorf("reading the files of document %d: %w", documentID, err)
	}
	for _, file := range files {
		store, err := r.Files.Named(file.Backend)
		if err != nil {
			// A file in a backend this process was given no settings for.
			// It is not skipped: deleting the row would orphan the object
			// for good. The document stays marked until the deployment can
			// reach the store.
			return fmt.Errorf("the stored file %q of document %d: %w", file.Key, documentID, err)
		}
		writer, writes := store.(filestore.Writer)
		if !writes {
			return fmt.Errorf("the %s store holding %q of document %d cannot be written to by this process", file.Backend, file.Key, documentID)
		}
		// A key that names nothing is not an error (filestore.Writer), so a
		// file removed by an earlier attempt does not stop this one.
		if err := writer.Remove(ctx, file.Key); err != nil {
			return fmt.Errorf("removing the stored file %q of document %d: %w", file.Key, documentID, err)
		}
	}

	if err := r.Rows.DeleteDocument(ctx, documentID); err != nil {
		return fmt.Errorf("deleting document %d: %w", documentID, err)
	}
	return nil
}

// PostgresRows is Rows over the pool. Each statement is its own
// transaction on purpose: every step is committed before the next begins,
// which is what lets a retry carry on from where a failure stopped, and no
// lock is held while the vector store or the file store is being called.
type PostgresRows struct {
	Pool *pgxpool.Pool
}

func (p PostgresRows) Mark(ctx context.Context, documentID int64) (bool, error) {
	marked, err := New(p.Pool).MarkDocumentDeleting(ctx, documentID)
	return marked == 1, err
}

func (p PostgresRows) ChunkIDs(ctx context.Context, documentID int64, batch int) ([]int64, error) {
	return New(p.Pool).ChunkIDsOf(ctx, ChunkIDsOfParams{DocumentID: documentID, Batch: int32(batch)})
}

func (p PostgresRows) DeleteChunks(ctx context.Context, documentID int64, ids []int64) error {
	return New(p.Pool).DeleteChunks(ctx, DeleteChunksParams{DocumentID: documentID, Ids: ids})
}

func (p PostgresRows) Files(ctx context.Context, documentID int64) ([]StoredFile, error) {
	rows, err := New(p.Pool).FilesOf(ctx, documentID)
	if err != nil {
		return nil, err
	}
	files := make([]StoredFile, len(rows))
	for index, row := range rows {
		files[index] = StoredFile{Backend: row.StorageBackend, Key: row.StorageKey}
	}
	return files, nil
}

func (p PostgresRows) DeleteDocument(ctx context.Context, documentID int64) error {
	return New(p.Pool).DeleteDocument(ctx, documentID)
}

// Owns reports whether the account has the document (Owners).
func (p PostgresRows) Owns(ctx context.Context, documentID int64, userID string) (bool, error) {
	owned, err := New(p.Pool).OwnsDocument(ctx, OwnsDocumentParams{ID: documentID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return owned, err
}
