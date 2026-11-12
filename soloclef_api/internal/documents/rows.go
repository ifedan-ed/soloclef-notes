package documents

import "time"

// Cited is the chunk a citation names, as get_document_by_chunk_id reads it:
// which document it belongs to, and where in that document it stands.
type Cited struct {
	ID         int64
	DocumentID int64
	Position   int64
}

// Record is a document row as DocumentWithChunksRead is built from it.
type Record struct {
	ID           int64
	Title        string
	DocumentType string
	// Metadata is documents.document_metadata as the column's text, nil for
	// SQL NULL. The column is jsonb, so Postgres has already decided the
	// key order and the numbers' spelling.
	Metadata             []byte
	Content              string
	ContentHash          string
	UniqueIdentifierHash *string
	CreatedAt            time.Time
	UpdatedAt            *time.Time
}

// Passage is one chunk as ChunkRead renders it.
type Passage struct {
	CreatedAt  time.Time
	ID         int64
	Content    string
	DocumentID int64
	PageStart  *int64
	PageEnd    *int64
}

// VersionSummary is one row of the version list, which leaves the markdown
// out.
type VersionSummary struct {
	Number      int64
	Title       *string
	ContentHash string
	CreatedAt   time.Time
}

// Version is one stored version, whole.
type Version struct {
	Number         int64
	Title          *string
	ContentHash    string
	SourceMarkdown *string
	CreatedAt      time.Time
}

// Restorable is a document as the restore reads it: whose it is, and the
// three columns a snapshot copies.
type Restorable struct {
	ID int64
	// UserID is the owner the row was read for, which is the caller. The
	// restore's own write names it again.
	UserID         string
	Title          string
	ContentHash    string
	SourceMarkdown *string
}
