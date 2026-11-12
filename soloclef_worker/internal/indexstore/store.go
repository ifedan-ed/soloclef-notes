// Package indexstore is the indexing job's Postgres: the one package in the
// Go tier that writes chunk rows.
//
// It reads the document a job names, moves the document's status as the web
// and mobile apps read it, and replaces a document's chunks in one
// transaction. Vectors are not here, and the schema has no column for one:
// Qdrant is the only vector store.
//
// # The status a client reads
//
// `documents.status` is JSON, and the apps read `state`: pending, processing,
// ready, failed (with `reason`). Two keys are added while a document is
// processing and removed when it ends:
//
//	stage    parsing, chunking, embedding or indexing, so a slow upload says
//	         what it is waiting on
//	attempt  how many times a worker has started on it, which is what bounds
//	         a retry (see indexing.Config.MaxAttempts)
//
// Nothing that reads `state` has to change.
//
// # Hand-written SQL
//
// The API tier generates its queries with sqlc. This module has no sqlc
// configuration of its own, and six statements written out here cost less
// than a second one to keep in step. They are held to the real schema by
// the database test, which migrates a throwaway Postgres with
// soloclef_schema and runs them.
package indexstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The states and stages written to `documents.status`.
const (
	StateProcessing = "processing"
	StateDeleting   = "deleting"

	StageParsing   = "parsing"
	StageChunking  = "chunking"
	StageEmbedding = "embedding"
	StageIndexing  = "indexing"
	// StageRetrying is a document waiting to be tried again after a failure
	// that may pass (the parser service or the embedding service was down).
	StageRetrying = "retrying"
)

var (
	// ErrGone: the document does not exist, or is being deleted. Either way
	// there is nothing to index and nothing to mark.
	ErrGone = errors.New("indexstore: the document is gone or being deleted")
	// ErrChanged: another job replaced this document's chunks while this one
	// was working, so what this one planned to keep is no longer there. The
	// job is run again from the start rather than writing a mixture.
	ErrChanged = errors.New("indexstore: the document's chunks changed underneath this job")
)

// Document is what a job needs to know about the document it was handed.
type Document struct {
	ID int64
	// UserID is the account that owns the document, the uuid in
	// `documents.user_id`. It is copied onto every chunk row and every
	// vector, because a search is always inside one account.
	UserID string
	// Type is the `documenttype` value, which is a search filter in Qdrant.
	Type  string
	Title string
	State string
	// Attempt is how many times a worker has already started on it.
	Attempt int
	// Parser is the parser chosen for this document, or empty when nobody
	// chose and the deployment's named default applies. ParserFrom says
	// which of the three places the choice was found in (chooseParser).
	Parser     string
	ParserFrom ParserSource
	// SourceMarkdown is the Markdown already on the row: a note, a crawled
	// page, or a file parsed before. It is indexed when there is no File,
	// and when TextIsNewer.
	SourceMarkdown string
	// TextIsNewer says the row's Markdown was put there by a person after
	// the file was parsed (`content_needs_reindexing`, which a version
	// restore sets): an older version of the text, brought back. Parsing
	// the file again would throw it away, so the text is what is indexed.
	TextIsNewer bool
	// File is the uploaded original, or nil for a document that has none.
	File *File
	// Vision says the upload asked for the vision model
	// (`documents.use_vision_llm`): an uploaded picture is described, and
	// a PDF's embedded pictures too. It is on the row, so a re-index keeps
	// it.
	Vision bool
	// VisionChoice is the owner's own vision model (`user_models`, purpose
	// vision), "" when the owner chose none and the deployment's applies.
	VisionChoice string
}

// File is where a document's uploaded original is stored.
type File struct {
	Backend string
	Key     string
	// Name is the name it was uploaded under; its extension chooses the
	// parser.
	Name string
	Size int64
}

// ParserSource is where a document's parser was decided.
type ParserSource string

const (
	// ParserFromDocument: the document's own `parser` column, written by
	// the upload, the import or the re-index that asked for it.
	ParserFromDocument ParserSource = "document"
	// ParserFromConnector: the parser of the connection the document came
	// through (`search_source_connectors.parser`), which its owner set for
	// every file of that source.
	ParserFromConnector ParserSource = "connector"
	// ParserFromDefault: neither named one, so the deployment's
	// SOLOCLEF_PARSER_DEFAULT applies. The indexer resolves the name; this
	// package only says that nothing above it chose.
	ParserFromDefault ParserSource = "default"
)

// chooseParser decides which parser a document is read with, from the two
// columns that can name one. The order is fixed and each step is a place a
// person made a choice, never a guess:
//
//  1. the document's own parser, when its row names one;
//  2. otherwise the parser of the document's connection, when the document
//     has a connection and that row names one;
//  3. otherwise none here: the deployment's named default.
//
// The document comes first because it is the narrower choice: a re-index
// that asks for another parser writes it on the document, and must not be
// undone by the connection's setting. A connection's parser therefore
// reaches the documents that never chose, which is what "the parser for
// this source" means, including documents that existed before it was set.
// A NULL and an empty string both mean "not chosen": the column's CHECK
// allows only the four names or NULL, so an empty string cannot be stored,
// and treating it as a name would hand the indexer a parser called "".
func chooseParser(document, connector *string) (string, ParserSource) {
	if document != nil && *document != "" {
		return *document, ParserFromDocument
	}
	if connector != nil && *connector != "" {
		return *connector, ParserFromConnector
	}
	return "", ParserFromDefault
}

// Store reads and writes through one pool.
type Store struct {
	pool *pgxpool.Pool
}

// New wraps a pool the caller opened and will close.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Load reads the document and its original file, or ErrGone.
func (s *Store) Load(ctx context.Context, documentID int64) (Document, error) {
	var (
		document Document
		parser   *string
		// connectorParser is the parser of the document's connection, NULL
		// when the document has no connection or the connection names none.
		connectorParser *string
		markdown        *string
		backend         *string
		key             *string
		name            *string
		size            *int64
		visionChoice    *string
	)
	// The newest ORIGINAL: a re-upload adds a row and the latest is the file.
	//
	// The connection is joined on its primary key, so the document's parser
	// and its connection's arrive in the one round trip the job already
	// makes; chooseParser then picks between them in Go, where the order is
	// written down and tested, not folded into a COALESCE.
	err := s.pool.QueryRow(ctx, `
		SELECT d.id, d.user_id::text, d.document_type::text, d.title,
		       COALESCE(d.status ->> 'state', 'ready'),
		       COALESCE((d.status ->> 'attempt')::int, 0),
		       d.parser, c.parser, d.source_markdown, d.content_needs_reindexing,
		       f.storage_backend, f.storage_key, f.original_filename, f.size_bytes,
		       d.use_vision_llm,
		       (SELECT m.model FROM user_models AS m WHERE m.user_id = d.user_id AND m.purpose = 'vision')
		FROM documents AS d
		LEFT JOIN search_source_connectors AS c ON c.id = d.connector_id
		LEFT JOIN LATERAL (
		    SELECT storage_backend, storage_key, original_filename, size_bytes
		    FROM document_files
		    WHERE document_id = d.id AND kind = 'ORIGINAL'
		    ORDER BY id DESC
		    LIMIT 1
		) AS f ON true
		WHERE d.id = $1`, documentID).Scan(
		&document.ID, &document.UserID, &document.Type, &document.Title,
		&document.State, &document.Attempt, &parser, &connectorParser, &markdown, &document.TextIsNewer,
		&backend, &key, &name, &size, &document.Vision, &visionChoice)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrGone
	}
	if err != nil {
		return Document{}, fmt.Errorf("indexstore: reading document %d: %w", documentID, err)
	}
	if document.State == StateDeleting {
		return Document{}, ErrGone
	}
	document.Parser, document.ParserFrom = chooseParser(parser, connectorParser)
	if markdown != nil {
		document.SourceMarkdown = *markdown
	}
	if visionChoice != nil {
		document.VisionChoice = *visionChoice
	}
	if key != nil {
		document.File = &File{Backend: *backend, Key: *key, Name: *name, Size: *size}
	}
	return document, nil
}

// Begin marks the document processing, at its first stage, on this attempt.
// A document being deleted is left alone and reported as ErrGone.
func (s *Store) Begin(ctx context.Context, documentID int64, stage string, attempt int) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE documents
		SET status = jsonb_build_object('state', 'processing', 'stage', $2::text, 'attempt', $3::int)
		WHERE id = $1 AND COALESCE(status ->> 'state', 'ready') <> 'deleting'`,
		documentID, stage, attempt)
	if err != nil {
		return fmt.Errorf("indexstore: marking document %d processing: %w", documentID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrGone
	}
	return nil
}

// Stage records which step a processing document has reached. It changes
// nothing on a document that is no longer processing: a job that lost a race
// must not move a finished document's status.
func (s *Store) Stage(ctx context.Context, documentID int64, stage string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE documents
		SET status = status || jsonb_build_object('stage', $2::text)
		WHERE id = $1 AND status ->> 'state' = 'processing'`,
		documentID, stage)
	if err != nil {
		return fmt.Errorf("indexstore: recording that document %d is %s: %w", documentID, stage, err)
	}
	return nil
}

// reasonLimit is the longest reason stored, the Python tier's own limit: the
// reason is shown in a list row.
const reasonLimit = 500

// Fail marks the document failed with a reason a person can read. The
// document's chunks are left as they were, so a re-index that fails does not
// take a searchable document out of search.
func (s *Store) Fail(ctx context.Context, documentID int64, reason string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE documents
		SET status = jsonb_build_object('state', 'failed', 'reason', left($2::text, $3::int))
		WHERE id = $1 AND COALESCE(status ->> 'state', 'ready') <> 'deleting'`,
		documentID, reason, reasonLimit)
	if err != nil {
		return fmt.Errorf("indexstore: marking document %d failed: %w", documentID, err)
	}
	return nil
}

// ExistingChunk is a chunk row the document already has.
type ExistingChunk struct {
	ID      int64
	Content string
}

// Chunks reads the document's current chunks, in order.
func (s *Store) Chunks(ctx context.Context, documentID int64) ([]ExistingChunk, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, content FROM chunks WHERE document_id = $1 ORDER BY "position", id`, documentID)
	if err != nil {
		return nil, fmt.Errorf("indexstore: reading the chunks of document %d: %w", documentID, err)
	}
	chunks, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ExistingChunk, error) {
		var chunk ExistingChunk
		return chunk, row.Scan(&chunk.ID, &chunk.Content)
	})
	if err != nil {
		return nil, fmt.Errorf("indexstore: reading the chunks of document %d: %w", documentID, err)
	}
	return chunks, nil
}

// AllocateChunkIDs takes ids from the chunk table's own sequence, before the
// rows exist.
//
// The ids are needed early because a chunk's Qdrant point is written before
// its row is committed (see Replace), and the point's id is the row's id. A
// sequence value is never handed out twice, so an id taken here and never
// used -- the job failed before its commit -- can collide with nothing.
func (s *Store) AllocateChunkIDs(ctx context.Context, count int) ([]int64, error) {
	if count == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT nextval('chunks_id_seq') FROM generate_series(1, $1::int)`, count)
	if err != nil {
		return nil, fmt.Errorf("indexstore: allocating %d chunk ids: %w", count, err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("indexstore: allocating %d chunk ids: %w", count, err)
	}
	return ids, nil
}

// Place is where a chunk sits in its document.
type Place struct {
	// Position is the chunk's index in document order.
	Position int
	// StartLine and EndLine are 1-based and inclusive.
	StartLine, EndLine int
	// PageStart and PageEnd are nil for a document with no pages.
	PageStart, PageEnd *int
}

// KeptChunk is an existing row whose text is unchanged; only its place may
// have moved.
type KeptChunk struct {
	ID int64
	Place
}

// NewChunk is a row to insert, under an id from AllocateChunkIDs.
type NewChunk struct {
	ID      int64
	Content string
	Place
}

// Replacement is a document's whole new set of chunks.
type Replacement struct {
	DocumentID int64
	// UserID is the document's owner, written on every new chunk row. The
	// chunk table's foreign key is (document_id, user_id), so a value that
	// is not the document's own owner is refused by the database.
	UserID string
	// Markdown becomes the document's content and its source Markdown, so a
	// later re-index never needs the parser again.
	Markdown string
	// FromSourceMarkdown is a document indexed from its own Markdown (a
	// note: no file, no parser). Its source Markdown is what a person
	// writes, so it is never overwritten here: a save that landed while this
	// job ran would be lost. If the text changed since the job read it, the
	// document keeps the state that save gave it (pending, with its own job
	// queued) rather than being marked ready with stale passages.
	FromSourceMarkdown bool
	Kept               []KeptChunk
	New                []NewChunk
}

// Replace swaps the document's chunks for the replacement and marks it
// ready, in one transaction, and returns the ids of the rows it deleted.
//
// # Why this is atomic for a reader
//
// A search has two legs. The keyword leg reads chunk rows; the meaning leg
// gets ids from Qdrant and re-reads those rows. The caller writes the new
// chunks' points to Qdrant BEFORE calling this, under ids no row has yet, so
// until this commits those points re-read to nothing and the old chunks
// answer both legs. At the commit the old rows vanish and the new ones
// appear with their points already searchable. No reader sees half of each,
// and none sees a document with no chunks.
//
// # Two jobs for one document
//
// The document row is locked first, so two replacements run one after the
// other. The second finds the first's result: if a row it planned to keep
// was deleted, it stops with ErrChanged and the job starts again. If its
// kept rows are all still there it deletes everything else of the document,
// including what the first job inserted, and returns those ids so their
// points are removed too. Either way the document ends as exactly one job's
// chunks. No lock outlives the transaction, which matters because the
// deployment's pooler hands a session to another client between transactions.
func (s *Store) Replace(ctx context.Context, replacement Replacement) (removed []int64, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("indexstore: beginning the replacement: %w", err)
	}
	// A no-op after Commit; on any early return it undoes everything.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var state string
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(status ->> 'state', 'ready') FROM documents WHERE id = $1 FOR UPDATE`,
		replacement.DocumentID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && state == StateDeleting) {
		return nil, ErrGone
	}
	if err != nil {
		return nil, fmt.Errorf("indexstore: locking document %d: %w", replacement.DocumentID, err)
	}

	keptIDs := make([]int64, len(replacement.Kept))
	if len(replacement.Kept) > 0 {
		// Ids are bigint and go as int64. A place is an int4 column and is
		// narrowed here: a line or page number past two thousand million
		// is not a document this pipeline can have chunked.
		var (
			positions  = make([]int32, len(replacement.Kept))
			startLines = make([]int32, len(replacement.Kept))
			endLines   = make([]int32, len(replacement.Kept))
			pageStarts = make([]*int32, len(replacement.Kept))
			pageEnds   = make([]*int32, len(replacement.Kept))
		)
		for index, chunk := range replacement.Kept {
			keptIDs[index] = chunk.ID
			positions[index] = int32(chunk.Position)
			startLines[index] = int32(chunk.StartLine)
			endLines[index] = int32(chunk.EndLine)
			pageStarts[index] = narrow(chunk.PageStart)
			pageEnds[index] = narrow(chunk.PageEnd)
		}
		// One statement for every kept row, however many there are.
		tag, err := tx.Exec(ctx, `
			UPDATE chunks AS c
			SET "position" = u.position, start_line = u.start_line, end_line = u.end_line,
			    page_start = u.page_start, page_end = u.page_end
			FROM unnest($2::bigint[], $3::int[], $4::int[], $5::int[], $6::int[], $7::int[])
			     AS u(id, position, start_line, end_line, page_start, page_end)
			WHERE c.id = u.id AND c.document_id = $1`,
			replacement.DocumentID, keptIDs, positions, startLines, endLines, pageStarts, pageEnds)
		if err != nil {
			return nil, fmt.Errorf("indexstore: moving the kept chunks of document %d: %w", replacement.DocumentID, err)
		}
		if tag.RowsAffected() != int64(len(replacement.Kept)) {
			return nil, ErrChanged
		}
	}

	rows, err := tx.Query(ctx,
		`DELETE FROM chunks WHERE document_id = $1 AND id <> ALL($2::bigint[]) RETURNING id`,
		replacement.DocumentID, keptIDs)
	if err != nil {
		return nil, fmt.Errorf("indexstore: deleting the old chunks of document %d: %w", replacement.DocumentID, err)
	}
	if removed, err = pgx.CollectRows(rows, pgx.RowTo[int64]); err != nil {
		return nil, fmt.Errorf("indexstore: deleting the old chunks of document %d: %w", replacement.DocumentID, err)
	}

	if len(replacement.New) > 0 {
		// COPY, not INSERT: a long document is thousands of rows, and COPY
		// sends them as one stream instead of a statement each.
		// The owner is sent as text into a uuid column, which the binary
		// COPY protocol does not convert, so it is parsed once here.
		var owner pgtype.UUID
		if err := owner.Scan(replacement.UserID); err != nil {
			return nil, fmt.Errorf("indexstore: the owner of document %d is not a uuid: %w", replacement.DocumentID, err)
		}
		createdAt := time.Now().UTC()
		_, err = tx.CopyFrom(ctx, pgx.Identifier{"chunks"},
			[]string{"id", "content", "position", "start_line", "end_line", "page_start", "page_end", "document_id", "user_id", "created_at"},
			pgx.CopyFromSlice(len(replacement.New), func(index int) ([]any, error) {
				chunk := replacement.New[index]
				return []any{
					chunk.ID, chunk.Content, int32(chunk.Position),
					int32(chunk.StartLine), int32(chunk.EndLine),
					narrow(chunk.PageStart), narrow(chunk.PageEnd),
					replacement.DocumentID, owner, createdAt,
				}, nil
			}))
		if err != nil {
			return nil, fmt.Errorf("indexstore: inserting the chunks of document %d: %w", replacement.DocumentID, err)
		}
	}

	// In SET, source_markdown is the row's value before this statement, so
	// "written since" is a comparison with what was indexed.
	_, err = tx.Exec(ctx, `
		UPDATE documents
		SET content = $2,
		    source_markdown = CASE WHEN $3 THEN source_markdown ELSE $2 END,
		    content_needs_reindexing = ($3 AND source_markdown IS DISTINCT FROM $2),
		    updated_at = now(),
		    status = CASE WHEN $3 AND source_markdown IS DISTINCT FROM $2 THEN status
		                  ELSE '{"state": "ready"}'::jsonb END
		WHERE id = $1`,
		replacement.DocumentID, replacement.Markdown, replacement.FromSourceMarkdown)
	if err != nil {
		return nil, fmt.Errorf("indexstore: marking document %d ready: %w", replacement.DocumentID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("indexstore: committing the chunks of document %d: %w", replacement.DocumentID, err)
	}
	return removed, nil
}

// narrow is a nullable page number as the int4 column takes it.
func narrow(value *int) *int32 {
	if value == nil {
		return nil
	}
	narrowed := int32(*value)
	return &narrowed
}
