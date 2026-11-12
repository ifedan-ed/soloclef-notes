package postgres_test

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// perCall is a postgres.DB that opens a connection for each statement. It
// stands in for a pool in the tests, without making the module depend on the
// pool package's own dependencies for the sake of a test.
type perCall struct {
	url string
}

func (p *perCall) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	conn, err := pgx.Connect(ctx, p.url)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	return conn.Exec(ctx, sql, args...)
}

func (p *perCall) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	conn, err := pgx.Connect(ctx, p.url)
	if err != nil {
		return failedRow{err}
	}
	return closingRow{Row: conn.QueryRow(ctx, sql, args...), conn: conn, ctx: ctx}
}

// closingRow closes its connection once the row has been read.
type closingRow struct {
	pgx.Row
	conn *pgx.Conn
	ctx  context.Context
}

func (r closingRow) Scan(dest ...any) error {
	defer r.conn.Close(context.WithoutCancel(r.ctx))
	return r.Row.Scan(dest...)
}

type failedRow struct{ err error }

func (r failedRow) Scan(...any) error { return r.err }
