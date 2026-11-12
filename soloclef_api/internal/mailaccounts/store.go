package mailaccounts

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// Rows is an account's mailboxes in the database. Every method takes the
// owner, and every statement names it (queries.sql).
type Rows interface {
	// Mailboxes is every stored record of the owner, oldest first.
	Mailboxes(ctx context.Context, ownerID string) ([][]byte, error)
	// Mailbox is one stored record, or nil when it is not the owner's or
	// is not there.
	Mailbox(ctx context.Context, ownerID, accountID string) ([]byte, error)
	// Add appends a record to the owner's list, making the mail connection
	// if the account has none.
	Add(ctx context.Context, ownerID string, record []byte) error
	// Remove takes a mailbox out and reports whether there was one.
	Remove(ctx context.Context, ownerID, accountID string) (bool, error)
}

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) Mailboxes(ctx context.Context, ownerID string) ([][]byte, error) {
	return New(s.Pool).MailboxesOfOwner(ctx, ownerID)
}

func (s Store) Mailbox(ctx context.Context, ownerID, accountID string) ([]byte, error) {
	record, err := New(s.Pool).MailboxOfOwner(ctx, MailboxOfOwnerParams{UserID: ownerID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return record, err
}

// errAccountGone is an add for an account removed meanwhile.
var errAccountGone = errors.New("mailaccounts: the account is no longer there")

func (s Store) Add(ctx context.Context, ownerID string, record []byte) error {
	_, err := database.Write(ctx, s.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
			queries := New(tx)
			// The account's row is the lock: see queries.sql, LockAccount.
			if _, err := queries.LockAccount(ctx, ownerID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return struct{}{}, errAccountGone
				}
				return struct{}{}, err
			}
			id, err := queries.MailConnectionOfOwner(ctx, ownerID)
			if errors.Is(err, pgx.ErrNoRows) {
				return struct{}{}, queries.InsertMailConnection(ctx, InsertMailConnectionParams{
					UserID: ownerID, Name: ConnectionName, Record: record,
				})
			}
			if err != nil {
				return struct{}{}, err
			}
			_, err = queries.AppendMailbox(ctx, AppendMailboxParams{Record: record, ID: id, UserID: ownerID})
			return struct{}{}, err
		})
	return err
}

func (s Store) Remove(ctx context.Context, ownerID, accountID string) (bool, error) {
	removed, err := New(s.Pool).RemoveMailbox(ctx, RemoveMailboxParams{AccountID: accountID, UserID: ownerID})
	return removed > 0, err
}
