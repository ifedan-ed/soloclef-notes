// Package postgres keeps checkpoints in one Postgres table.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
)

// Schema creates the table. It is exported so that an application with its
// own migration tool can run this exact text there; EnsureSchema runs it for
// applications that have none.
//
// One row per thread, replaced on every save: the table's size is the number
// of live threads, not the number of steps ever taken.
//
// state is bytea, not jsonb, although it holds JSON. jsonb refuses a string
// containing U+0000, and tool output (a scraped page, a remote server's
// reply) contains one often enough that a jsonb column would turn "a tool
// returned a NUL" into "the turn cannot be saved". bytea stores the bytes it
// is given. It is also cheaper: nothing is parsed on the way in.
const Schema = `
CREATE TABLE IF NOT EXISTS soloclef_agent_checkpoints (
    thread_id  text        PRIMARY KEY,
    seq        bigint      NOT NULL,
    status     text        NOT NULL,
    node       text        NOT NULL,
    state      bytea       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
)`

// TreeIndex is what makes DeleteTree a range scan. The primary key's index
// is ordered by the database's collation, which a prefix comparison cannot
// use unless that collation is "C"; this one is ordered byte by byte
// (text_pattern_ops), which is the order "every name that starts with this"
// is a range in. It is a second statement, and not part of Schema, so that
// each is one statement an application's migration tool can run as it is.
const TreeIndex = `
CREATE INDEX IF NOT EXISTS soloclef_agent_checkpoints_tree
    ON soloclef_agent_checkpoints (thread_id text_pattern_ops)`

// DB is the part of a pgx pool or connection the store uses. Taking the
// interface lets a caller pass a *pgxpool.Pool, a *pgx.Conn or a pgx.Tx, so
// an application can save a checkpoint inside its own transaction.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store is a checkpoint.Store over one table.
type Store struct {
	db DB
}

// New returns a store that uses db. It does not create the table.
func New(db DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("checkpoint/postgres: New needs a database handle")
	}
	return &Store{db: db}, nil
}

// EnsureSchema creates the table and its index if they do not exist.
func (s *Store) EnsureSchema(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, Schema); err != nil {
		return fmt.Errorf("checkpoint/postgres: create table: %w", err)
	}
	if _, err := s.db.Exec(ctx, TreeIndex); err != nil {
		return fmt.Errorf("checkpoint/postgres: create index: %w", err)
	}
	return nil
}

func (s *Store) Load(ctx context.Context, thread string) (checkpoint.Checkpoint, error) {
	cp := checkpoint.Checkpoint{Thread: thread}
	var status string
	err := s.db.QueryRow(ctx,
		`SELECT seq, status, node, state FROM soloclef_agent_checkpoints WHERE thread_id = $1`,
		thread,
	).Scan(&cp.Seq, &status, &cp.Node, &cp.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return checkpoint.Checkpoint{}, checkpoint.ErrNotFound
	}
	if err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("checkpoint/postgres: load %q: %w", thread, err)
	}
	cp.Status = checkpoint.Status(status)
	return cp, nil
}

// The sequence rule is enforced inside each statement, so there is no window
// between "check the sequence" and "write the row" for a second writer to
// slip through, and no transaction is needed around a save.
//
// A first save (seq 1) may only insert: if the thread already has a row the
// insert does nothing. A later save may only update the row whose seq is
// exactly one behind. Either way, zero rows affected means somebody else
// saved first.
const insertSQL = `
INSERT INTO soloclef_agent_checkpoints (thread_id, seq, status, node, state)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (thread_id) DO NOTHING`

const updateSQL = `
UPDATE soloclef_agent_checkpoints
   SET seq = $2, status = $3, node = $4, state = $5, updated_at = now()
 WHERE thread_id = $1 AND seq = $2 - 1`

func (s *Store) Save(ctx context.Context, cp checkpoint.Checkpoint) error {
	// Which statement runs is decided by the caller's Seq, never by looking
	// at the table first: a look followed by a write is the race the rule
	// exists to close.
	sql := updateSQL
	if cp.Seq == 1 {
		sql = insertSQL
	}
	// A nil slice would be stored as NULL and break the NOT NULL column; an
	// empty state is still a state.
	state := cp.State
	if state == nil {
		state = []byte{}
	}
	tag, err := s.db.Exec(ctx, sql, cp.Thread, cp.Seq, string(cp.Status), cp.Node, state)
	if err != nil {
		return fmt.Errorf("checkpoint/postgres: save %q: %w", cp.Thread, err)
	}
	if tag.RowsAffected() != 1 {
		return checkpoint.ErrConflict
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, thread string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM soloclef_agent_checkpoints WHERE thread_id = $1`, thread); err != nil {
		return fmt.Errorf("checkpoint/postgres: delete %q: %w", thread, err)
	}
	return nil
}

// deleteTreeSQL removes a thread and everything nested under it.
//
// The nested threads are the names from thread+"/" up to, and not
// including, thread+"0": "0" is the character after "/", so that range is
// exactly the names beginning thread+"/". It is written as a range with the
// byte-order operators (~>=~, ~<~), and not as LIKE, for two reasons: a
// thread name may contain "%" or "_" and a range needs no escaping, and a
// range over parameters can use TreeIndex whatever plan the statement is
// given, where LIKE with a parameter for its pattern can only when the plan
// is made for that one value.
const deleteTreeSQL = `
DELETE FROM soloclef_agent_checkpoints
 WHERE thread_id = $1
    OR (thread_id ~>=~ $2 AND thread_id ~<~ $3)`

func (s *Store) DeleteTree(ctx context.Context, thread string) error {
	if _, err := s.db.Exec(ctx, deleteTreeSQL, thread, thread+"/", thread+"0"); err != nil {
		return fmt.Errorf("checkpoint/postgres: delete tree %q: %w", thread, err)
	}
	return nil
}
