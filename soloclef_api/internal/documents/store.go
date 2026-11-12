package documents

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folders"
)

// Store is this slice's statements over the pool.
//
// Every read is on the pool and only the restore's writes are in a
// transaction, which is the shape every write of this tier has
// (internal/documentdeletion).
//
// Every method that reads a document, a chunk or a folder takes the account
// that is asking, and its statement names that account: a row is the
// caller's or it does not exist.
type Store struct {
	Pool *pgxpool.Pool
}

// CitedChunk is the account's chunk a citation names, or nil when the
// account has none by that id.
func (s Store) CitedChunk(ctx context.Context, chunkID int64, userID string) (*Cited, error) {
	row, err := New(s.Pool).CitedChunkByID(ctx, CitedChunkByIDParams{ID: chunkID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Cited{ID: row.ID, DocumentID: row.DocumentID, Position: row.Position}, nil
}

// RecordOf is the account's document a cited chunk belongs to, or nil. The
// foreign key makes nil unreachable in one snapshot; a document deleted
// between the chunk's read and this one is the route's 404.
func (s Store) RecordOf(ctx context.Context, documentID int64, userID string) (*Record, error) {
	row, err := New(s.Pool).DocumentOfChunk(ctx, DocumentOfChunkParams{ID: documentID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Record{
		ID:                   row.ID,
		Title:                row.Title,
		DocumentType:         row.DocumentType,
		Metadata:             row.DocumentMetadata,
		Content:              row.Content,
		ContentHash:          row.ContentHash,
		UniqueIdentifierHash: row.UniqueIdentifierHash,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
	}, nil
}

// Owns reports whether the account has the document. False is the answer
// for another account's document and for an id that names nothing alike.
func (s Store) Owns(ctx context.Context, documentID int64, userID string) (bool, error) {
	owned, err := New(s.Pool).OwnsDocument(ctx, OwnsDocumentParams{ID: documentID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return owned, err
}

// ChunkCount is how many chunks the account's document has.
func (s Store) ChunkCount(ctx context.Context, documentID int64, userID string) (int64, error) {
	return New(s.Pool).CountChunksOfDocument(ctx, CountChunksOfDocumentParams{DocumentID: documentID, UserID: userID})
}

// ChunksBefore is how many of a document's chunks come before the cited one.
func (s Store) ChunksBefore(ctx context.Context, cited Cited, userID string) (int64, error) {
	return New(s.Pool).CountChunksBefore(ctx, CountChunksBeforeParams{
		DocumentID:    cited.DocumentID,
		UserID:        userID,
		CitedPosition: cited.Position,
		CitedID:       cited.ID,
	})
}

// ChunkPage is one run of a document's chunks in reading order.
func (s Store) ChunkPage(ctx context.Context, documentID int64, userID string, limit, offset int64) ([]Passage, error) {
	rows, err := New(s.Pool).ChunksOfDocumentPage(ctx, ChunksOfDocumentPageParams{
		DocumentID: documentID,
		UserID:     userID,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	passages := make([]Passage, 0, len(rows))
	for _, row := range rows {
		passages = append(passages, Passage{
			CreatedAt:  row.CreatedAt,
			ID:         row.ID,
			Content:    row.Content,
			DocumentID: row.DocumentID,
			PageStart:  row.PageStart,
			PageEnd:    row.PageEnd,
		})
	}
	return passages, nil
}

// Versions is a document's history, newest first. The caller has asked Owns
// first: a version row carries no owner of its own.
func (s Store) Versions(ctx context.Context, documentID int64) ([]VersionSummary, error) {
	rows, err := New(s.Pool).VersionsOfDocument(ctx, documentID)
	if err != nil {
		return nil, err
	}
	versions := make([]VersionSummary, 0, len(rows))
	for _, row := range rows {
		versions = append(versions, VersionSummary{
			Number:      row.VersionNumber,
			Title:       row.Title,
			ContentHash: row.ContentHash,
			CreatedAt:   row.CreatedAt,
		})
	}
	return versions, nil
}

// Version is one version of a document, or nil when it has none by that
// number.
func (s Store) Version(ctx context.Context, documentID, number int64) (*Version, error) {
	row, err := New(s.Pool).VersionOfDocument(ctx, VersionOfDocumentParams{
		DocumentID:    documentID,
		VersionNumber: number,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Version{
		Number:         row.VersionNumber,
		Title:          row.Title,
		ContentHash:    row.ContentHash,
		SourceMarkdown: row.SourceMarkdown,
		CreatedAt:      row.CreatedAt,
	}, nil
}

// Restorable is the account's document as the restore reads it, or nil.
func (s Store) Restorable(ctx context.Context, documentID int64, userID string) (*Restorable, error) {
	row, err := New(s.Pool).DocumentForRestore(ctx, DocumentForRestoreParams{ID: documentID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Restorable{
		ID:             row.ID,
		UserID:         userID,
		Title:          row.Title,
		ContentHash:    row.ContentHash,
		SourceMarkdown: row.SourceMarkdown,
	}, nil
}

// Restore is the snapshot and the restore in one transaction: everything
// between the route's last refusal and its `session.commit()`. It registers
// no effect, because the enqueue that follows must be able to fail the
// request and an effect cannot (internal/database).
func (s Store) Restore(ctx context.Context, work RestoreWork) error {
	_, err := database.Write(ctx, s.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
			return struct{}{}, applyRestore(ctx, transactionRows{queries: New(tx)}, work)
		})
	return err
}

// transactionRows is versionRows over one transaction's generated queries.
type transactionRows struct {
	queries *Queries
}

func (r transactionRows) HighestVersionNumber(ctx context.Context, documentID int64) (int64, error) {
	return r.queries.HighestVersionNumberOfDocument(ctx, documentID)
}

func (r transactionRows) InsertVersion(ctx context.Context, documentID, number int64, state snapshot) error {
	return r.queries.InsertVersion(ctx, InsertVersionParams{
		DocumentID:     documentID,
		VersionNumber:  number,
		SourceMarkdown: state.sourceMarkdown,
		ContentHash:    state.contentHash,
		Title:          &state.title,
		CreatedAt:      state.takenAt,
	})
}

func (r transactionRows) DeleteVersionsOlderThan(ctx context.Context, documentID int64, cutoff time.Time) error {
	return r.queries.DeleteVersionsOlderThan(ctx, DeleteVersionsOlderThanParams{
		DocumentID: documentID,
		CreatedAt:  cutoff,
	})
}

func (r transactionRows) CountVersions(ctx context.Context, documentID int64) (int64, error) {
	return r.queries.CountVersionsOfDocument(ctx, documentID)
}

func (r transactionRows) OldestVersionIDs(ctx context.Context, documentID, excess int64) ([]int64, error) {
	return r.queries.OldestVersionIDs(ctx, OldestVersionIDsParams{
		DocumentID: documentID,
		Excess:     excess,
	})
}

func (r transactionRows) DeleteVersions(ctx context.Context, documentID int64, versionIDs []int64) error {
	return r.queries.DeleteVersionsByID(ctx, DeleteVersionsByIDParams{DocumentID: documentID, Ids: versionIDs})
}

func (r transactionRows) UpdateDocument(ctx context.Context, documentID int64, userID string, sourceMarkdown *string, title string) error {
	return r.queries.UpdateRestoredDocument(ctx, UpdateRestoredDocumentParams{
		ID:             documentID,
		UserID:         userID,
		SourceMarkdown: sourceMarkdown,
		Title:          title,
	})
}

// ProgressOf is the account's documents among the ids given, in id order,
// as the status poll reads them.
func (s Store) ProgressOf(ctx context.Context, userID string, documentIDs []int64) ([]Progress, error) {
	rows, err := New(s.Pool).StatusOfDocuments(ctx, StatusOfDocumentsParams{
		UserID:      userID,
		DocumentIds: documentIDs,
	})
	if err != nil {
		return nil, err
	}
	progress := make([]Progress, 0, len(rows))
	for _, row := range rows {
		progress = append(progress, Progress(row))
	}
	return progress, nil
}

// TypeCountsOf is how many documents the account has of each type, in the
// order of the type.
func (s Store) TypeCountsOf(ctx context.Context, userID string) ([]TypeCount, error) {
	rows, err := New(s.Pool).TypeCountsOfOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	counts := make([]TypeCount, 0, len(rows))
	for _, row := range rows {
		counts = append(counts, TypeCount{DocumentType: row.DocumentType, Count: row.Documents})
	}
	return counts, nil
}

// WatchedFoldersOf is the account's top-level folders marked as watched, in
// id order.
func (s Store) WatchedFoldersOf(ctx context.Context, userID string) ([]folders.Folder, error) {
	rows, err := New(s.Pool).WatchedFoldersOfOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	watched := make([]folders.Folder, 0, len(rows))
	for _, row := range rows {
		// Field by field, not a conversion: the folder type is another
		// package's and may grow a field this statement does not select.
		watched = append(watched, folders.Folder{
			ID: row.ID, Name: row.Name, Position: row.Position, ParentID: row.ParentID,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Metadata: row.Metadata,
		})
	}
	return watched, nil
}

// RecentTitles is the account's documents, most recently updated first.
func (s Store) RecentTitles(ctx context.Context, userID string, limit, offset int64) ([]TitleRow, error) {
	rows, err := New(s.Pool).RecentTitlesOfOwner(ctx, RecentTitlesOfOwnerParams{
		UserID:     userID,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	titles := make([]TitleRow, 0, len(rows))
	for _, row := range rows {
		titles = append(titles, TitleRow(row))
	}
	return titles, nil
}

// MatchingTitles is the account's documents whose title is like the term or
// contains it, most alike first. The ILIKE pattern is the term between two
// percent signs, with nothing in the term escaped.
func (s Store) MatchingTitles(ctx context.Context, userID, term string, limit, offset int64) ([]TitleRow, error) {
	rows, err := New(s.Pool).MatchingTitlesOfOwner(ctx, MatchingTitlesOfOwnerParams{
		UserID:     userID,
		Term:       term,
		Pattern:    "%" + term + "%",
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	titles := make([]TitleRow, 0, len(rows))
	for _, row := range rows {
		titles = append(titles, TitleRow(row))
	}
	return titles, nil
}
