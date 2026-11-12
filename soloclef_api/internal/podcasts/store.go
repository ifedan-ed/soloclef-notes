package podcasts

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// Store is Database over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

// Read hands the work the pool: each statement is its own transaction, which
// is all a route that writes nothing needs.
func (s Store) Read(ctx context.Context, work func(context.Context, Reading) error) error {
	return work(ctx, statements{queries: New(s.Pool)})
}

// Write hands the work one transaction, through the seam every write of this
// tier uses (internal/database).
func (s Store) Write(ctx context.Context, work func(context.Context, Writing) error) error {
	_, err := database.Write(ctx, s.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
			return struct{}{}, work(ctx, statements{queries: New(tx)})
		})
	return err
}

// statements is the generated queries behind the names the routes use, over
// whichever of the pool and a transaction it was given. Every method takes
// the owner's id, which is always the signed-in caller's.
type statements struct {
	queries *Queries
}

func (s statements) Podcast(ctx context.Context, userID string, podcastID int64) (*Stored, error) {
	row, err := s.queries.PodcastOfOwner(ctx, PodcastOfOwnerParams{ID: podcastID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return newStored(row)
}

func (s statements) Move(ctx context.Context, userID string, podcastID int64, current, target string) (bool, error) {
	matched, err := s.queries.MovePodcast(ctx, MovePodcastParams{
		ID: podcastID, UserID: userID, Current: current, Target: target,
	})
	return matched == 1, err
}

func (s statements) Delete(ctx context.Context, userID string, podcastID int64) (bool, error) {
	matched, err := s.queries.DeletePodcast(ctx, DeletePodcastParams{ID: podcastID, UserID: userID})
	return matched == 1, err
}

func (s statements) LatestBrief(ctx context.Context, userID string) (any, error) {
	column, err := s.queries.LatestBrief(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeColumn(column)
}

func (s statements) OwnsThread(ctx context.Context, userID string, threadID int64) (bool, error) {
	return s.queries.ThreadOfOwner(ctx, ThreadOfOwnerParams{ThreadID: threadID, UserID: userID})
}

func (s statements) Create(ctx context.Context, userID string, podcast NewPodcast) (*Stored, error) {
	row, err := s.queries.CreatePodcast(ctx, CreatePodcastParams{
		Title: podcast.Title, SourceContent: &podcast.SourceContent, Spec: podcast.Spec,
		UserID: userID, ThreadID: podcast.ThreadID,
	})
	if err != nil {
		return nil, err
	}
	// The two rows are the same columns in the same order, by construction
	// (queries.sql); the conversion stops compiling if they ever are not.
	return newStored(PodcastOfOwnerRow(row))
}

func (s statements) ReplaceBrief(ctx context.Context, userID string, podcastID, expectedVersion int64, spec []byte) (bool, error) {
	matched, err := s.queries.ReplaceBrief(ctx, ReplaceBriefParams{
		Spec: spec, ID: podcastID, UserID: userID, ExpectedVersion: expectedVersion,
	})
	return matched == 1, err
}
