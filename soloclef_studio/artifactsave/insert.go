package artifactsave

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Rows is an artifact whose files are already in the store, written by
// Insert inside the caller's transaction.
type Rows struct {
	// ID is the id ReserveID gave: the files' keys already name it.
	ID     int64
	UserID string
	// ThreadID is nil when the artifact belongs to no conversation (its
	// thread was deleted while it was being made).
	ThreadID   *int64
	ToolCallID string
	Title      string
	Format     string
	Markdown   string
	Metadata   []byte
	// Primary is the artifact's file.
	Primary StoredFile
}

// StoredFile is a file already written to the store, as its row records it.
type StoredFile struct {
	Backend  string
	Key      string
	Filename string
	MimeType string
	Size     int64
	// SHA256 is the digest of the stored bytes, lowercase hex.
	SHA256 string
}

// ReserveID takes an id for an artifact whose files will be stored before
// its rows are written, so that their keys can name it.
func ReserveID(ctx context.Context, db DBTX) (int64, error) {
	return New(db).ReserveArtifactID(ctx)
}

// Insert writes the document, the artifact under its reserved id, and the
// primary file's row, in tx. It does not commit: the caller's transaction
// is the unit, and whatever else it writes (the job that made the artifact,
// marked ready) commits or rolls back with these rows.
func Insert(ctx context.Context, tx pgx.Tx, rows Rows) (Created, error) {
	switch {
	case rows.ID <= 0:
		return Created{}, errors.New("artifactsave: the artifact has no reserved id")
	case rows.Primary.Key == "" || rows.Primary.Backend == "" || rows.Primary.Size <= 0 || len(rows.Primary.SHA256) != 64:
		return Created{}, errors.New("artifactsave: the primary file is not fully described")
	}
	queries := New(tx)
	documentID, err := insertDocument(ctx, queries, rows.UserID, rows.Title, rows.Markdown)
	if err != nil {
		return Created{}, err
	}
	if err := queries.CreateArtifactWithID(ctx, CreateArtifactWithIDParams{
		ID: rows.ID, DocumentID: documentID, UserID: rows.UserID, ThreadID: rows.ThreadID,
		Format: rows.Format, ToolCallID: &rows.ToolCallID, Metadata: rows.Metadata,
	}); err != nil {
		return Created{}, err
	}
	if err := queries.LinkDocumentToArtifact(ctx, LinkDocumentToArtifactParams{ArtifactID: rows.ID, DocumentID: documentID, UserID: rows.UserID}); err != nil {
		return Created{}, err
	}
	fileID, err := queries.CreateArtifactFile(ctx, CreateArtifactFileParams{
		ArtifactID: rows.ID, Role: ArtifactFileRolePrimary, StorageBackend: rows.Primary.Backend, StorageKey: rows.Primary.Key,
		OriginalFilename: rows.Primary.Filename, MimeType: rows.Primary.MimeType,
		SizeBytes: rows.Primary.Size, ChecksumSha256: rows.Primary.SHA256,
	})
	if err != nil {
		return Created{}, err
	}
	return Created{ArtifactID: rows.ID, DocumentID: documentID, FileID: fileID}, nil
}
