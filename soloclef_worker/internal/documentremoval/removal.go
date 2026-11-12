// Package documentremoval removes documents and everything stored for
// them: their points in the vector store, their chunk rows, the files of
// the documents and of the artifacts they carry, and the rows. The folder
// deletion and the account erasure both remove documents this way.
//
// # The order
//
// It is the order the API tier's single-document delete keeps
// (soloclef_api/internal/documentdeletion), for the same reasons:
//
//   - A batch's points go before its rows: a point is found from its row
//     (the point's id is the chunk's id), so rows deleted first would leave
//     points nobody could find again.
//   - The files go before the rows that name them, for the same reason.
//   - The document row is last. While it exists the removal can be run
//     again and carries on from where it stopped.
//
// Every step can be repeated: a point, a file or a batch already gone is
// not an error. So a removal that failed partway is finished by running it
// again, and nothing is ever left that no row points at.
//
// # Better than the Python job
//
// The Python folder job deleted the chunk rows and left every vector in the
// vector store, and deleted an artifact's document while its files went
// with the cascade, leaving them in the bucket for good. Both are removed
// here, and so is a video's narration, which only its artifact's metadata
// names (slideAudioOf).
package documentremoval

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// Vectors is the vector store as a removal uses it.
type Vectors interface {
	// Delete removes these chunks' points and returns once the store has
	// applied it. An id with no point is not an error.
	Delete(ctx context.Context, chunkIDs []int64) error
}

// Stores finds the store a file's row says it was written to
// (*filestore.Stores).
type Stores interface {
	Named(backend string) (filestore.Store, error)
}

// chunkBatch is how many chunks one round removes: one request to the
// vector store and one DELETE, holding its locks for milliseconds.
const chunkBatch = 1000

// Removal removes documents, always within one account: every statement
// names the owner, so a list of ids that strays into another account's
// documents removes nothing of theirs.
type Removal struct {
	Pool    *pgxpool.Pool
	Vectors Vectors
	Files   Stores
}

// Remove removes one of the account's documents. It returns nil when the
// document is gone, including when it was already gone or was never the
// account's.
func (r Removal) Remove(ctx context.Context, userID string, documentID int64) error {
	// The marker first, committed on its own: from here the document is out
	// of search and the indexer will not write to it.
	tag, err := r.Pool.Exec(ctx, `
		UPDATE documents SET status = '{"state": "deleting"}'::jsonb
		WHERE id = $1 AND user_id = $2::uuid AND status ->> 'state' IS DISTINCT FROM 'deleting'`, documentID, userID)
	if err != nil {
		return fmt.Errorf("marking document %d: %w", documentID, err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := r.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM documents WHERE id = $1 AND user_id = $2::uuid)`,
			documentID, userID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return nil
		}
	}

	if err := r.removeChunks(ctx, `SELECT id FROM chunks WHERE document_id = $1 AND user_id = $2::uuid LIMIT $3`,
		`DELETE FROM chunks WHERE id = ANY($1) AND document_id = $2 AND user_id = $3::uuid`, documentID, userID); err != nil {
		return fmt.Errorf("the chunks of document %d: %w", documentID, err)
	}

	files, err := r.storedFiles(ctx, `
		SELECT storage_backend, storage_key FROM document_files WHERE document_id = $1 AND user_id = $2::uuid
		UNION ALL
		SELECT f.storage_backend, f.storage_key FROM artifact_files AS f
		JOIN artifacts AS a ON a.id = f.artifact_id
		WHERE a.document_id = $1 AND a.user_id = $2::uuid
		UNION ALL
		SELECT `+slideAudioColumns+` FROM artifacts AS a `+slideAudioOf+`
		WHERE a.document_id = $1 AND a.user_id = $2::uuid AND `+slideHasAudio, documentID, userID)
	if err != nil {
		return fmt.Errorf("reading the files of document %d: %w", documentID, err)
	}
	if err := r.removeFiles(ctx, files); err != nil {
		return fmt.Errorf("document %d: %w", documentID, err)
	}

	// Its file rows, versions, notebook links and artifact cascade with it.
	if _, err := r.Pool.Exec(ctx, `DELETE FROM documents WHERE id = $1 AND user_id = $2::uuid`, documentID, userID); err != nil {
		return fmt.Errorf("deleting document %d: %w", documentID, err)
	}
	return nil
}

// removeChunks takes the chunks a batch at a time: points, then rows.
func (r Removal) removeChunks(ctx context.Context, choose, remove string, arguments ...any) error {
	for {
		rows, err := r.Pool.Query(ctx, choose, append(arguments, chunkBatch)...)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := r.Vectors.Delete(ctx, ids); err != nil {
			return fmt.Errorf("removing %d vectors: %w", len(ids), err)
		}
		if _, err := r.Pool.Exec(ctx, remove, append([]any{ids}, arguments...)...); err != nil {
			return fmt.Errorf("deleting %d chunks: %w", len(ids), err)
		}
	}
}

// A video's narration is one MP3 a slide, and no row names it: the
// artifact's metadata does, under slides[].audio_storage_key, with the
// store it is in beside it (videojob/artifact.go writes both). These three
// pieces read it out as (backend, key) rows, for the two removals to add to
// the files they take. Without them a deleted video's MP4 went and its
// narration stayed in the bucket for good, named by nothing.
//
// A slide that names audio and no store comes back with an empty backend,
// which removeFiles refuses: guessing a store would delete from the wrong
// one, and skipping the file would orphan it. Metadata whose slides are not
// a list names no audio, and a list entry that is not an object, or has no
// narration (a silent slide), is passed over.
const (
	slideAudioColumns = `COALESCE(slide.value ->> 'storage_backend', ''), slide.value ->> 'audio_storage_key'`
	slideAudioOf      = `CROSS JOIN LATERAL jsonb_array_elements(
			CASE WHEN jsonb_typeof(a.metadata -> 'slides') = 'array' THEN a.metadata -> 'slides' ELSE '[]'::jsonb END
		) AS slide (value)`
	slideHasAudio = `COALESCE(slide.value ->> 'audio_storage_key', '') <> ''`
)

// StoredFile is a file and the store its row says it is in.
type StoredFile struct {
	Backend string
	Key     string
}

func (r Removal) storedFiles(ctx context.Context, query string, arguments ...any) ([]StoredFile, error) {
	rows, err := r.Pool.Query(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (StoredFile, error) {
		var file StoredFile
		err := row.Scan(&file.Backend, &file.Key)
		return file, err
	})
}

func (r Removal) removeFiles(ctx context.Context, files []StoredFile) error {
	for _, file := range files {
		store, err := r.Files.Named(file.Backend)
		if err != nil {
			// A file in a store this process has no settings for is not
			// skipped: deleting its row would orphan it for good. The
			// document stays marked until the store can be reached.
			return fmt.Errorf("the stored file %q: %w", file.Key, err)
		}
		writer, writes := store.(filestore.Writer)
		if !writes {
			return fmt.Errorf("the %s store holding %q cannot be written to by this process", file.Backend, file.Key)
		}
		// A key that names nothing is not an error, so a file removed by
		// an earlier attempt does not stop this one.
		if err := writer.Remove(ctx, file.Key); err != nil {
			return fmt.Errorf("removing the stored file %q: %w", file.Key, err)
		}
	}
	return nil
}

// ErrStillActive is an erasure asked for an account that has not been
// locked out: the API deactivates an account before it queues its erasure,
// so an active one means the message is not the one the person asked for.
var ErrStillActive = errors.New("documentremoval: the account is still active, and is not erased")

// EraseAccount removes everything an account has stored outside its rows,
// then the account, whose rows cascade with it. It is the whole account at
// once rather than document by document: the chunks are taken by owner, in
// batches, which is a few rounds for an account of thousands of documents.
//
// Run again after a failure, it carries on; run for an account already
// gone, it does nothing.
func (r Removal) EraseAccount(ctx context.Context, userID string) error {
	var active bool
	err := r.Pool.QueryRow(ctx, `SELECT is_active FROM "user" WHERE id = $1::uuid`, userID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the account: %w", err)
	}
	if active {
		return ErrStillActive
	}
	// Out of search at once, and out of the indexer's reach.
	if _, err := r.Pool.Exec(ctx, `
		UPDATE documents SET status = '{"state": "deleting"}'::jsonb
		WHERE user_id = $1::uuid AND status ->> 'state' IS DISTINCT FROM 'deleting'`, userID); err != nil {
		return fmt.Errorf("marking the account's documents: %w", err)
	}
	if err := r.removeChunks(ctx, `SELECT id FROM chunks WHERE user_id = $1::uuid LIMIT $2`,
		`DELETE FROM chunks WHERE id = ANY($1) AND user_id = $2::uuid`, userID); err != nil {
		return fmt.Errorf("the account's chunks: %w", err)
	}
	files, err := r.storedFiles(ctx, `
		SELECT storage_backend, storage_key FROM document_files WHERE user_id = $1::uuid
		UNION ALL
		SELECT f.storage_backend, f.storage_key FROM artifact_files AS f
		JOIN artifacts AS a ON a.id = f.artifact_id WHERE a.user_id = $1::uuid
		UNION ALL
		SELECT `+slideAudioColumns+` FROM artifacts AS a `+slideAudioOf+`
		WHERE a.user_id = $1::uuid AND `+slideHasAudio, userID)
	if err != nil {
		return fmt.Errorf("reading the account's files: %w", err)
	}
	if err := r.removeFiles(ctx, files); err != nil {
		return fmt.Errorf("the account's files: %w", err)
	}
	// Everything the account owns cascades from this row. Only a locked-out
	// account: one reactivated meanwhile is left as it is.
	if _, err := r.Pool.Exec(ctx, `DELETE FROM "user" WHERE id = $1::uuid AND NOT is_active`, userID); err != nil {
		return fmt.Errorf("deleting the account: %w", err)
	}
	return nil
}
