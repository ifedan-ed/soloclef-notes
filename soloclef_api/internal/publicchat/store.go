package publicchat

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the three routes' statements over the pool.
//
// Each is run on the pool rather than in a transaction: every one is a single
// read, and the Python routes run them on the request's session without
// committing anything.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) SnapshotByToken(ctx context.Context, token string) (*Snapshot, error) {
	row, err := New(s.Pool).PublicSnapshotByToken(ctx, token)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Snapshot{
		OutputID:  row.NotebookOutputID,
		ExpiresAt: row.ExpiresAt,
		Data:      row.SnapshotData,
	}, nil
}

func (s Store) VideoArtifact(ctx context.Context, artifactID int64) (*VideoArtifact, error) {
	row, err := New(s.Pool).SharedVideoArtifact(ctx, artifactID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &VideoArtifact{ID: row.ID, Title: row.DocumentTitle, Metadata: row.Metadata}, nil
}

func (s Store) ArtifactGeneration(ctx context.Context, artifactID int64) (int64, bool, error) {
	generation, err := New(s.Pool).SharedArtifactGeneration(ctx, artifactID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return generation, true, nil
}

func (s Store) ArtifactFiles(ctx context.Context, artifactID int64) ([]StoredFile, error) {
	rows, err := New(s.Pool).SharedArtifactFiles(ctx, artifactID)
	if err != nil {
		return nil, err
	}
	files := make([]StoredFile, 0, len(rows))
	for _, row := range rows {
		files = append(files, StoredFile{
			ID:       row.ID,
			Role:     row.Role,
			Filename: row.OriginalFilename,
			MimeType: row.MimeType,
			Size:     row.SizeBytes,
		})
	}
	return files, nil
}

func (s Store) ArtifactFormats(ctx context.Context, artifactIDs []int64) (map[int64]string, error) {
	rows, err := New(s.Pool).SharedArtifactFormats(ctx, artifactIDs)
	if err != nil {
		return nil, err
	}
	formats := make(map[int64]string, len(rows))
	for _, row := range rows {
		formats[row.ID] = row.Format
	}
	return formats, nil
}
