package uploads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// Staged is one file that has been streamed to the store and not yet
// recorded.
type Staged struct {
	// DocumentID is the id the storage key was built from: the existing
	// document's when Existing, otherwise one taken from the sequence.
	DocumentID int64
	Existing   bool
	// Name is the file's name as uploaded, without any folder.
	Name string
	// ContentType is what the client said the file is, or "".
	ContentType string
	Identity    string
	Backend     string
	Key         string
	Size        int64
	SHA256      string
}

// Recorded is what one staged file became.
type Recorded struct {
	DocumentID int64
	Outcome    Outcome
}

// Batch is one request's files and what is the same for all of them.
type Batch struct {
	// UserID is the account the documents are made for: the caller.
	UserID string
	Parser string
	// Vision is the upload's use_vision_llm, stored on every document so a
	// re-index describes its pictures too.
	Vision bool
	Files  []Staged
}

// ErrMovedMeanwhile: between the read that chose a file's document and the
// transaction that records it, that document was deleted, or another
// upload of the same name got there first with different bytes. Nothing is
// recorded; trying again reads the new state.
type ErrMovedMeanwhile struct{ Name string }

func (e *ErrMovedMeanwhile) Error() string {
	return fmt.Sprintf("uploads: the document named %q changed while its file was uploading", e.Name)
}

// ErrBeingDeleted: the document of this name is being deleted.
type ErrBeingDeleted struct{ Name string }

func (e *ErrBeingDeleted) Error() string {
	return fmt.Sprintf("uploads: the document named %q is being deleted", e.Name)
}

// Rows is the database as an upload uses it.
type Rows interface {
	// NextDocumentID takes an id for a document that does not exist yet.
	NextDocumentID(ctx context.Context) (int64, error)
	// UploadNamed is the document an earlier upload of this identity made
	// in this account, or nil.
	UploadNamed(ctx context.Context, userID, identity string) (*Prior, error)
	// Record writes the whole batch in one transaction and reports what
	// each file became, in order. On any error nothing is written.
	Record(ctx context.Context, batch Batch) ([]Recorded, error)
}

// PostgresRows is Rows over the pool.
type PostgresRows struct {
	Pool *pgxpool.Pool
}

func (p PostgresRows) NextDocumentID(ctx context.Context) (int64, error) {
	return New(p.Pool).NextDocumentID(ctx)
}

func (p PostgresRows) UploadNamed(ctx context.Context, userID, identity string) (*Prior, error) {
	row, err := New(p.Pool).UploadNamed(ctx, UploadNamedParams{UserID: userID, Identity: identity})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return priorOf(row.ID, row.State, row.Checksum), nil
}

func priorOf(id int64, state string, checksum *string) *Prior {
	prior := &Prior{ID: id, State: state}
	if checksum != nil {
		prior.Checksum = *checksum
	}
	return prior
}

// Record is the one transaction of an upload.
func (p PostgresRows) Record(ctx context.Context, batch Batch) ([]Recorded, error) {
	return database.Write(ctx, p.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) ([]Recorded, error) {
		queries := New(tx)
		recorded := make([]Recorded, len(batch.Files))
		for index, file := range batch.Files {
			var err error
			recorded[index], err = recordFile(ctx, queries, batch, file, placement{
				documentType: fileType,
				metadata: map[string]any{
					"FILE_NAME":   file.Name,
					"file_size":   file.Size,
					"upload_time": time.Now().UTC().Format(time.RFC3339),
				},
			})
			if err != nil {
				return nil, err
			}
		}
		return recorded, nil
	})
}

// The two kinds of document an upload makes, as the `documenttype` enum
// spells them.
const (
	fileType       = "FILE"
	folderFileType = "LOCAL_FOLDER_FILE"
)

// placement is what the two upload routes differ in when a file is
// recorded: the kind of document, the folder it goes in, and its metadata.
type placement struct {
	documentType string
	// folderID is the folder the document is put in, or nil for none. An
	// existing document is moved only when one is named.
	folderID *int64
	// metadata is written whole to a new document and merged over the old
	// keys of an existing one.
	metadata map[string]any
	// touchUnchanged records the metadata and the folder on a document
	// whose bytes are the ones just uploaded. A folder sync needs it (see
	// the TouchUnchangedFolderFile statement); a plain upload leaves a
	// duplicate exactly as it was.
	touchUnchanged bool
}

// recordFile decides what one stored file becomes and writes it, inside the
// request's transaction.
//
// The file's document is read again here, locked, and the decision is made
// from that read, not from the one that chose the storage key: the two can
// differ only if something happened in between, and then the key may name
// the wrong document, so the batch is refused (ErrMovedMeanwhile) instead
// of recording a file under a row it was not stored for.
func recordFile(ctx context.Context, queries *Queries, batch Batch, file Staged, placed placement) (Recorded, error) {
	var prior *Prior
	row, err := queries.LockUploadNamed(ctx, LockUploadNamedParams{UserID: batch.UserID, Identity: file.Identity})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return Recorded{}, err
	default:
		prior = priorOf(row.ID, row.State, row.Checksum)
	}

	outcome := decide(prior, file.SHA256)
	if outcome == BeingDeleted {
		return Recorded{}, &ErrBeingDeleted{Name: file.Name}
	}
	// The key was built from file.DocumentID. That is only right for a file
	// that is kept if the document is still the one the first read saw.
	if outcome.keepsFile() {
		stillAbsent := prior == nil && !file.Existing
		stillTheSame := prior != nil && file.Existing && prior.ID == file.DocumentID
		if !stillAbsent && !stillTheSame {
			return Recorded{}, &ErrMovedMeanwhile{Name: file.Name}
		}
	}

	metadata, err := json.Marshal(placed.metadata)
	if err != nil {
		return Recorded{}, err
	}

	var recorded Recorded
	switch outcome {
	case Created:
		id, err := queries.InsertPendingUpload(ctx, InsertPendingUploadParams{
			ID: file.DocumentID, Title: file.Name, DocumentType: placed.documentType, Metadata: metadata, Checksum: file.SHA256,
			Identity: file.Identity, UserID: batch.UserID, FolderID: placed.folderID, Parser: batch.Parser, UseVisionLlm: batch.Vision,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// ON CONFLICT DO NOTHING: an upload of the same name committed
			// between the lock (which found no row to hold) and this
			// insert.
			return Recorded{}, &ErrMovedMeanwhile{Name: file.Name}
		}
		if err != nil {
			return Recorded{}, err
		}
		recorded = Recorded{DocumentID: id, Outcome: Created}
	case Replaced, Retried:
		changed, err := queries.RequeueUpload(ctx, RequeueUploadParams{
			ID: prior.ID, UserID: batch.UserID, Parser: batch.Parser, UseVisionLlm: batch.Vision, Checksum: file.SHA256, Metadata: metadata, FolderID: placed.folderID,
		})
		if err != nil {
			return Recorded{}, err
		}
		if changed != 1 {
			return Recorded{}, &ErrMovedMeanwhile{Name: file.Name}
		}
		recorded = Recorded{DocumentID: prior.ID, Outcome: outcome}
	case Duplicate:
		// Only when the bytes are the ones the document holds: a document
		// still being indexed from other bytes is a Duplicate too (a job is
		// on its way), and saying "seen, unchanged" of it would be untrue.
		if placed.touchUnchanged && placed.folderID != nil && prior.Checksum != "" && prior.Checksum == file.SHA256 {
			if err := queries.TouchUnchangedFolderFile(ctx, TouchUnchangedFolderFileParams{
				ID: prior.ID, UserID: batch.UserID, Metadata: metadata, FolderID: *placed.folderID,
			}); err != nil {
				return Recorded{}, err
			}
		}
		return Recorded{DocumentID: prior.ID, Outcome: Duplicate}, nil
	}

	if !outcome.keepsFile() {
		return recorded, nil
	}
	var mimeType *string
	if file.ContentType != "" {
		mimeType = &file.ContentType
	}
	checksum := file.SHA256
	if err := queries.InsertOriginalFile(ctx, InsertOriginalFileParams{
		DocumentID: recorded.DocumentID, UserID: batch.UserID,
		StorageBackend: file.Backend, StorageKey: file.Key, OriginalFilename: file.Name,
		MimeType: mimeType, SizeBytes: file.Size, Checksum: &checksum,
	}); err != nil {
		return Recorded{}, err
	}
	return recorded, nil
}
