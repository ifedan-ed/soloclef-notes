package microsoftmail

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/mailaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailtools"
)

// Rows is an account's Microsoft mailboxes in the database. Every method
// takes the owner and every statement names it (queries.sql).
type Rows interface {
	// Mailbox is one of the owner's Microsoft mailbox records, or nil when
	// it is not the owner's, is a password mailbox, or is not there.
	Mailbox(ctx context.Context, ownerID, accountID string) ([]byte, error)
	// Save rewrites the owner's list of mailboxes, making the mail
	// connection if the account has none. build is given the list as it
	// stands (empty for a new connection) and returns the one to store;
	// it runs with the connection locked.
	Save(ctx context.Context, ownerID string, build func(accounts []json.RawMessage) ([]json.RawMessage, error)) error
	// Merge writes keys into one of the owner's Microsoft mailboxes and
	// reports whether there was one.
	Merge(ctx context.Context, ownerID, accountID string, fields []byte) (bool, error)
}

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) Mailbox(ctx context.Context, ownerID, accountID string) ([]byte, error) {
	found, err := New(s.Pool).MicrosoftMailboxOfOwner(ctx, MicrosoftMailboxOfOwnerParams{UserID: ownerID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return found, err
}

// errAccountGone is a connect finishing for an account removed meanwhile.
var errAccountGone = errors.New("microsoftmail: the account is no longer there")

func (s Store) Save(ctx context.Context, ownerID string, build func([]json.RawMessage) ([]json.RawMessage, error)) error {
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
			held, err := queries.MailConnectionOfOwner(ctx, ownerID)
			absent := errors.Is(err, pgx.ErrNoRows)
			if err != nil && !absent {
				return struct{}{}, err
			}
			var accounts []json.RawMessage
			if !absent {
				if err := json.Unmarshal(held.Accounts, &accounts); err != nil {
					return struct{}{}, err
				}
			}
			built, err := build(accounts)
			if err != nil {
				return struct{}{}, err
			}
			encoded, err := json.Marshal(built)
			if err != nil {
				return struct{}{}, err
			}
			if absent {
				// The one name both kinds of mailbox make the connection
				// under, so they share a row.
				return struct{}{}, queries.InsertMailConnection(ctx, InsertMailConnectionParams{
					UserID: ownerID, Name: mailaccounts.ConnectionName, Accounts: encoded,
				})
			}
			_, err = queries.ReplaceMailboxes(ctx, ReplaceMailboxesParams{Accounts: encoded, ID: held.ID, UserID: ownerID})
			return struct{}{}, err
		})
	return err
}

// Merge is soloclef_assistant/mailtools' statement: the chat's mail tools
// refresh a mailbox's token from the worker with the same write, so there
// is one of it.
func (s Store) Merge(ctx context.Context, ownerID, accountID string, fields []byte) (bool, error) {
	return mailtools.Store{Pool: s.Pool}.Merge(ctx, ownerID, accountID, fields)
}
