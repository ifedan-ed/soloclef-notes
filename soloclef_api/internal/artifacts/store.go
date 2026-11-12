package artifacts

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// Store is the routes' statements over the pool. Every method that reads or
// writes an artifact takes the owner's id, and it is always the caller's.
type Store struct {
	Pool *pgxpool.Pool
}

// ArtifactsIn is the list's query: one of two statements, chosen by whether
// the request named a thread. Both read only the caller's own artifacts.
func (s Store) ArtifactsIn(ctx context.Context, userID string, threadID *int64) ([]Listed, error) {
	if threadID == nil {
		rows, err := New(s.Pool).ArtifactsOfOwner(ctx, userID)
		if err != nil {
			return nil, err
		}
		listed := make([]Listed, 0, len(rows))
		for _, row := range rows {
			updatedAt := row.UpdatedAt
			listed = append(listed, Listed{
				ID: row.ID, DocumentID: row.DocumentID, Title: row.Title, Format: row.Format,
				Generation: row.Generation, Status: row.Status, ThreadID: row.ThreadID,
				CreatedAt: row.CreatedAt, UpdatedAt: &updatedAt, Metadata: row.Metadata,
			})
		}
		return listed, nil
	}

	rows, err := New(s.Pool).ArtifactsOfOwnerInThread(ctx, ArtifactsOfOwnerInThreadParams{
		UserID:   userID,
		ThreadID: threadID,
	})
	if err != nil {
		return nil, err
	}
	listed := make([]Listed, 0, len(rows))
	for _, row := range rows {
		updatedAt := row.UpdatedAt
		listed = append(listed, Listed{
			ID: row.ID, DocumentID: row.DocumentID, Title: row.Title, Format: row.Format,
			Generation: row.Generation, Status: row.Status, ThreadID: row.ThreadID,
			CreatedAt: row.CreatedAt, UpdatedAt: &updatedAt, Metadata: row.Metadata,
		})
	}
	return listed, nil
}

// VideoIn is one artifact for the video route, or nil when the caller has
// no such artifact -- which the route reports as "Artifact not found"
// whether the id is unknown or is another account's.
func (s Store) VideoIn(ctx context.Context, userID string, artifactID int64) (*Video, error) {
	row, err := New(s.Pool).VideoArtifactOfOwner(ctx, VideoArtifactOfOwnerParams{
		ID:     artifactID,
		UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Video{
		ID:       row.ID,
		Title:    row.Title,
		Format:   row.Format,
		Metadata: row.Metadata,
		ThreadID: row.ThreadID,
	}, nil
}

// DocumentOf is the delete's read: the document the artifact stands on, with
// its status, or nil when the caller has no such artifact.
func (s Store) DocumentOf(ctx context.Context, userID string, artifactID int64) (*Document, error) {
	row, err := New(s.Pool).DocumentOfArtifactOfOwner(ctx, DocumentOfArtifactOfOwnerParams{
		ID:     artifactID,
		UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Document{ID: row.ID, Status: row.Status}, nil
}

// MarkDeleting claims the document for a delete: the marker, committed
// before anything of the document is removed, and written only if the
// document still has the status the caller read. It goes through the
// transaction seam every write of this tier uses and registers no effect:
// the removal that follows is the route's own next step, and it must be able
// to fail the request, which an effect cannot (internal/database).
func (s Store) MarkDeleting(ctx context.Context, userID string, document Document) (bool, error) {
	return database.Write(ctx, s.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (bool, error) {
			matched, err := New(tx).MarkArtifactDocumentDeleting(ctx, MarkArtifactDocumentDeletingParams{
				ID: document.ID, UserID: userID, Was: document.Status,
			})
			return matched == 1, err
		})
}
