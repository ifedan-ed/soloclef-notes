package driveconnect

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// connection is the account's Drive row: its id and its stored config,
// tokens sealed.
type connection struct {
	ID     int64
	Config []byte
}

// Rows is the account's Drive connection in the database. Every method
// takes the owner and every statement names it (queries.sql).
type Rows interface {
	// Connection is the owner's connection, or nil.
	Connection(ctx context.Context, ownerID string) (*connection, error)
	// ConnectionByID is the same by id; another account's is nil.
	ConnectionByID(ctx context.Context, ownerID string, connectorID int64) (*connection, error)
	// Save writes a grant into the owner's connection, making the row if
	// there is none. build is given the config being replaced (nil for a
	// new connection) and returns the one to store; it runs with the
	// account locked. previous is what was replaced.
	Save(ctx context.Context, ownerID string, name string, nextRun time.Time,
		build func(previous []byte) ([]byte, error)) (id int64, previous []byte, err error)
	// MarkExpired flags the owner's connection as needing a new consent.
	MarkExpired(ctx context.Context, ownerID string, connectorID int64) error
	// Delete removes the owner's connection and returns the config it
	// held. found is false when it is not the owner's or is not there.
	Delete(ctx context.Context, ownerID string, connectorID int64) (config []byte, found bool, err error)
}

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

func held(row ConnectionOfOwnerRow, err error) (*connection, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &connection{ID: row.ID, Config: row.Config}, nil
}

func (s Store) Connection(ctx context.Context, ownerID string) (*connection, error) {
	return held(New(s.Pool).ConnectionOfOwner(ctx, ownerID))
}

func (s Store) ConnectionByID(ctx context.Context, ownerID string, connectorID int64) (*connection, error) {
	row, err := New(s.Pool).ConnectionOfOwnerByID(ctx, ConnectionOfOwnerByIDParams{ID: connectorID, UserID: ownerID})
	return held(ConnectionOfOwnerRow(row), err)
}

// errAccountGone is a consent finishing for an account removed meanwhile.
var errAccountGone = errors.New("driveconnect: the account is no longer there")

func (s Store) Save(ctx context.Context, ownerID, name string, nextRun time.Time,
	build func(previous []byte) ([]byte, error)) (int64, []byte, error) {
	type saved struct {
		id       int64
		previous []byte
	}
	result, err := database.Write(ctx, s.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (saved, error) {
			queries := New(tx)
			// The account's row is the lock: see queries.sql, LockAccount.
			if _, err := queries.LockAccount(ctx, ownerID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return saved{}, errAccountGone
				}
				return saved{}, err
			}
			existing, err := held(queries.ConnectionOfOwner(ctx, ownerID))
			if err != nil {
				return saved{}, err
			}
			if existing == nil {
				config, err := build(nil)
				if err != nil {
					return saved{}, err
				}
				pace := int64(resyncMinutes)
				id, err := queries.InsertConnection(ctx, InsertConnectionParams{
					UserID: ownerID, Name: name, Config: config,
					IndexingFrequencyMinutes: &pace, NextScheduledAt: &nextRun,
				})
				return saved{id: id}, err
			}
			config, err := build(existing.Config)
			if err != nil {
				return saved{}, err
			}
			if _, err := queries.ReplaceGrant(ctx, ReplaceGrantParams{Config: config, ID: existing.ID, UserID: ownerID}); err != nil {
				return saved{}, err
			}
			return saved{id: existing.ID, previous: existing.Config}, nil
		})
	return result.id, result.previous, err
}

func (s Store) MarkExpired(ctx context.Context, ownerID string, connectorID int64) error {
	return New(s.Pool).MarkExpired(ctx, MarkExpiredParams{ID: connectorID, UserID: ownerID})
}

func (s Store) Delete(ctx context.Context, ownerID string, connectorID int64) ([]byte, bool, error) {
	config, err := New(s.Pool).DeleteConnection(ctx, DeleteConnectionParams{ID: connectorID, UserID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return config, err == nil, err
}
