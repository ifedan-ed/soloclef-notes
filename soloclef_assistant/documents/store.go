package documents

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is Rows over the pool. Each statement is its own transaction: a
// search writes nothing.
type Store struct {
	Pool *pgxpool.Pool
}

// noDocuments is passed for "every document": the statements take the list
// and a flag, because an empty array and "no restriction" must not be the
// same thing to a query that filters by it.
var noDocuments = []int64{}

func scopeArgs(documentIDs []int64) (all bool, ids []int64) {
	if len(documentIDs) == 0 {
		return true, noDocuments
	}
	return false, documentIDs
}

func (s Store) KeywordRanks(ctx context.Context, userID, query string, documentIDs []int64, limit int) ([]Ranked, error) {
	all, ids := scopeArgs(documentIDs)
	rows, err := New(s.Pool).KeywordRanks(ctx, KeywordRanksParams{
		Query: query, UserID: userID, AllDocuments: all, DocumentIds: ids, RowLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Ranked, len(rows))
	for i, row := range rows {
		out[i] = Ranked{ChunkID: row.ID, Rank: row.Rank}
	}
	return out, nil
}

func (s Store) Chunks(ctx context.Context, userID string, chunkIDs, documentIDs []int64) ([]Chunk, error) {
	all, ids := scopeArgs(documentIDs)
	rows, err := New(s.Pool).ChunksByID(ctx, ChunksByIDParams{
		UserID: userID, ChunkIds: chunkIDs, AllDocuments: all, DocumentIds: ids,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Chunk, len(rows))
	for i, row := range rows {
		out[i] = Chunk{
			ID: row.ID, Content: row.Content, Position: row.Position, Page: row.PageStart,
			DocumentID: row.DocumentID, Title: row.Title, DocumentType: row.DocumentType,
			Metadata: row.DocumentMetadata,
		}
	}
	return out, nil
}

func (s Store) ChunkIDsOfDocuments(ctx context.Context, userID string, documentIDs []int64) ([]int64, error) {
	return New(s.Pool).ChunkIDsOfDocuments(ctx, ChunkIDsOfDocumentsParams{UserID: userID, DocumentIds: documentIDs})
}

func (s Store) DocumentsInFolders(ctx context.Context, userID string, folderIDs []int64) ([]int64, error) {
	return New(s.Pool).DocumentsInFolders(ctx, DocumentsInFoldersParams{UserID: userID, FolderIds: folderIDs})
}
