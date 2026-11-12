package nextcloudconnect

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// connection is the account's Nextcloud row: its id and its stored config,
// app password sealed.
type connection struct {
	ID     int64
	Config []byte
}

// Rows is the account's Nextcloud connection in the database. Every method
// takes the owner and every statement names it (queries.sql).
type Rows interface {
	// Connection is the owner's connection, or nil.
	Connection(ctx context.Context, ownerID string) (*connection, error)
	// Save writes a new sign-in into the owner's connection, making the
	// row if there is none. previous is the config it replaced, or nil.
	Save(ctx context.Context, ownerID string, config []byte) (id int64, previous []byte, err error)
	// Delete removes the owner's connection and returns the config it
	// held. found is false when it is not the owner's or is not there.
	Delete(ctx context.Context, ownerID string, connectorID int64) (config []byte, found bool, err error)
}

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) Connection(ctx context.Context, ownerID string) (*connection, error) {
	row, err := New(s.Pool).ConnectionOfOwner(ctx, ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &connection{ID: row.ID, Config: row.Config}, nil
}

// errAccountGone is a sign-in finishing for an account removed meanwhile.
var errAccountGone = errors.New("nextcloudconnect: the account is no longer there")

func (s Store) Save(ctx context.Context, ownerID string, config []byte) (int64, []byte, error) {
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
			held, err := queries.ConnectionOfOwner(ctx, ownerID)
			if errors.Is(err, pgx.ErrNoRows) {
				id, err := queries.InsertConnection(ctx, InsertConnectionParams{
					UserID: ownerID, Name: connectionName, Config: config,
				})
				return saved{id: id}, err
			}
			if err != nil {
				return saved{}, err
			}
			if _, err := queries.ReplaceLogin(ctx, ReplaceLoginParams{Config: config, ID: held.ID, UserID: ownerID}); err != nil {
				return saved{}, err
			}
			return saved{id: held.ID, previous: held.Config}, nil
		})
	return result.id, result.previous, err
}

func (s Store) Delete(ctx context.Context, ownerID string, connectorID int64) ([]byte, bool, error) {
	config, err := New(s.Pool).DeleteConnection(ctx, DeleteConnectionParams{ID: connectorID, UserID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return config, err == nil, err
}
