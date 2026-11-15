package jmapaccounts

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Rows is an account's JMAP mailboxes in the database. Every method takes
// the owner, and every statement names it (queries.sql).
type Rows interface {
	// Accounts is the owner's mailboxes, oldest first.
	Accounts(ctx context.Context, ownerID string) ([]Account, error)
	// account is one mailbox with its sealed credentials, or ErrNoAccount.
	account(ctx context.Context, ownerID, accountID string) (stored, error)
	// refreshing locks the mailbox, hands its row to renew, and writes the
	// sealed credentials renew returns ("" writes nothing) before the lock
	// is let go. It returns the row as it stands after.
	refreshing(ctx context.Context, ownerID, accountID string, renew func(stored) (string, error)) (stored, error)
	// MarkReconnect flags a mailbox whose credential the server refused.
	MarkReconnect(ctx context.Context, ownerID, accountID string) error
	// SetCanSend records what a test found about sending.
	SetCanSend(ctx context.Context, ownerID, accountID string, canSend bool) error
	// Save connects a mailbox, or connects one again: the owner's mailbox
	// of the same user on the same server is replaced in place (its id and
	// added date kept) and its previous row returned, for its grant to be
	// revoked.
	Save(ctx context.Context, ownerID string, mailbox NewMailbox) (Account, *Removed, error)
	// Remove deletes a mailbox, its drafts with it, and returns what its
	// grant needs to be revoked; ErrNoAccount when there was none.
	Remove(ctx context.Context, ownerID, accountID string) (Removed, error)
}

// NewMailbox is a mailbox being connected, its credentials already sealed.
type NewMailbox struct {
	// Label is what the person called it; "" keeps a reconnected
	// mailbox's label, and names a new one by its username.
	Label              string
	ServerURL          string
	Username           string
	Auth               string
	OAuthClientID      string
	TokenEndpoint      string
	RevocationEndpoint string
	Sealed             string
	CanSend            bool
}

// Removed is a mailbox that was removed or replaced: what revoking its
// grant at the server needs, still sealed.
type Removed struct {
	OwnerID string
	row     stored
}

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) Accounts(ctx context.Context, ownerID string) ([]Account, error) {
	rows, err := New(s.Pool).AccountsOfOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(rows))
	for _, row := range rows {
		out = append(out, Account{ID: row.ID, Label: row.Label, ServerURL: row.ServerUrl, Username: row.Username,
			Auth: row.Auth, CanSend: row.CanSend, NeedsReconnect: row.NeedsReconnect, CreatedAt: row.CreatedAt})
	}
	return out, nil
}

// fromRow is a full row, from any of the queries that select one (sqlc
// gives each its own type with the same fields).
func fromRow(row AccountOfOwnerRow) stored {
	text := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	return stored{
		Account: Account{ID: row.ID, Label: row.Label, ServerURL: row.ServerUrl, Username: row.Username, Auth: row.Auth,
			CanSend: row.CanSend, NeedsReconnect: row.NeedsReconnect, CreatedAt: row.CreatedAt},
		OAuthClientID: text(row.OauthClientID), TokenEndpoint: text(row.TokenEndpoint),
		RevocationEndpoint: text(row.RevocationEndpoint), Sealed: row.Credentials,
	}
}

// isUUID guards an id before it reaches a uuid column: a string that is
// not one would fail the statement, and it is simply not a mailbox.
func isUUID(text string) bool {
	if len(text) != 36 {
		return false
	}
	for index, r := range text {
		switch {
		case index == 8 || index == 13 || index == 18 || index == 23:
			if r != '-' {
				return false
			}
		case !('0' <= r && r <= '9' || 'a' <= r && r <= 'f' || 'A' <= r && r <= 'F'):
			return false
		}
	}
	return true
}

func (s Store) account(ctx context.Context, ownerID, accountID string) (stored, error) {
	if !isUUID(accountID) {
		return stored{}, ErrNoAccount
	}
	row, err := New(s.Pool).AccountOfOwner(ctx, AccountOfOwnerParams{ID: accountID, UserID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return stored{}, ErrNoAccount
	}
	if err != nil {
		return stored{}, err
	}
	return fromRow(row), nil
}

func (s Store) refreshing(ctx context.Context, ownerID, accountID string, renew func(stored) (string, error)) (stored, error) {
	if !isUUID(accountID) {
		return stored{}, ErrNoAccount
	}
	var out stored
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		queries := New(tx)
		row, err := queries.LockAccountOfOwner(ctx, LockAccountOfOwnerParams{ID: accountID, UserID: ownerID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoAccount
		}
		if err != nil {
			return err
		}
		out = fromRow(AccountOfOwnerRow(row))
		sealed, err := renew(out)
		if err != nil || sealed == "" {
			return err
		}
		if _, err := queries.WriteCredentials(ctx, WriteCredentialsParams{Credentials: sealed, ID: accountID, UserID: ownerID}); err != nil {
			return err
		}
		out.Sealed, out.NeedsReconnect = sealed, false
		return nil
	})
	return out, err
}

func (s Store) MarkReconnect(ctx context.Context, ownerID, accountID string) error {
	_, err := New(s.Pool).MarkReconnect(ctx, MarkReconnectParams{ID: accountID, UserID: ownerID})
	return err
}

func (s Store) SetCanSend(ctx context.Context, ownerID, accountID string, canSend bool) error {
	_, err := New(s.Pool).SetCanSend(ctx, SetCanSendParams{CanSend: canSend, ID: accountID, UserID: ownerID})
	return err
}

// errOwnerGone is a save for an account removed meanwhile.
var errOwnerGone = errors.New("jmapaccounts: the account is no longer there")

func optional(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}

func (s Store) Save(ctx context.Context, ownerID string, mailbox NewMailbox) (Account, *Removed, error) {
	var saved Account
	var previous *Removed
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		queries := New(tx)
		// The owner's row is the lock: two connects of one mailbox at once
		// queue, and the second replaces the first instead of failing on
		// the unique constraint.
		if _, err := queries.LockOwner(ctx, ownerID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errOwnerGone
			}
			return err
		}
		held, err := queries.MailboxOfOwnerByAddress(ctx, MailboxOfOwnerByAddressParams{
			UserID: ownerID, ServerUrl: mailbox.ServerURL, Username: mailbox.Username})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			id, err := newUUID()
			if err != nil {
				return err
			}
			label := mailbox.Label
			if label == "" {
				label = mailbox.Username
			}
			created, err := queries.InsertAccount(ctx, InsertAccountParams{
				ID: id, UserID: ownerID, Label: label, ServerUrl: mailbox.ServerURL, Username: mailbox.Username,
				Auth: mailbox.Auth, OauthClientID: optional(mailbox.OAuthClientID), TokenEndpoint: optional(mailbox.TokenEndpoint),
				RevocationEndpoint: optional(mailbox.RevocationEndpoint), Credentials: mailbox.Sealed, CanSend: mailbox.CanSend,
			})
			if err != nil {
				return err
			}
			saved = Account{ID: id, Label: label, ServerURL: mailbox.ServerURL, Username: mailbox.Username,
				Auth: mailbox.Auth, CanSend: mailbox.CanSend, CreatedAt: created}
			return nil
		case err != nil:
			return err
		}
		old := fromRow(AccountOfOwnerRow(held))
		previous = &Removed{OwnerID: ownerID, row: old}
		if err := queries.ReplaceAccount(ctx, ReplaceAccountParams{
			Label: optional(mailbox.Label), Auth: mailbox.Auth, OauthClientID: optional(mailbox.OAuthClientID),
			TokenEndpoint: optional(mailbox.TokenEndpoint), RevocationEndpoint: optional(mailbox.RevocationEndpoint),
			Credentials: mailbox.Sealed, CanSend: mailbox.CanSend, ID: old.ID, UserID: ownerID,
		}); err != nil {
			return err
		}
		saved = old.Account
		if mailbox.Label != "" {
			saved.Label = mailbox.Label
		}
		saved.Auth, saved.CanSend, saved.NeedsReconnect = mailbox.Auth, mailbox.CanSend, false
		return nil
	})
	if err != nil {
		return Account{}, nil, err
	}
	return saved, previous, nil
}

func (s Store) Remove(ctx context.Context, ownerID, accountID string) (Removed, error) {
	if !isUUID(accountID) {
		return Removed{}, ErrNoAccount
	}
	row, err := New(s.Pool).DeleteAccountOfOwner(ctx, DeleteAccountOfOwnerParams{ID: accountID, UserID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Removed{}, ErrNoAccount
	}
	if err != nil {
		return Removed{}, err
	}
	return Removed{OwnerID: ownerID, row: fromRow(AccountOfOwnerRow(row))}, nil
}

// newUUID is a random UUID (version 4), made here and not by the column's
// default so the row's id is known before it is written.
func newUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}
