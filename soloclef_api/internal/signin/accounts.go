package signin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// What a password may be. The upper bound is not a policy about passwords:
// Argon2id's cost grows with its input, so a megabyte "password" is a way to
// make the server work, and it is refused before any hashing.
const (
	minPasswordLength = 8
	maxPasswordLength = 256
)

// ErrWeakPassword is a new password that is too short, too long, or the
// address itself. Its text says which, for the person choosing one.
var ErrWeakPassword = errors.New("password refused")

// checkNewPassword says why a new password is refused, or nothing.
func checkNewPassword(address, password string) error {
	switch length := utf8.RuneCountInString(password); {
	case length < minPasswordLength:
		return fmt.Errorf("%w: it should be at least %d characters", ErrWeakPassword, minPasswordLength)
	case length > maxPasswordLength:
		return fmt.Errorf("%w: it should be at most %d characters", ErrWeakPassword, maxPasswordLength)
	}
	if strings.EqualFold(strings.TrimSpace(password), strings.TrimSpace(address)) {
		return fmt.Errorf("%w: it should not be the address", ErrWeakPassword)
	}
	return nil
}

// SetPassword makes an account for the address, or gives the account it
// already has a new password, and ends every sign-in that account had.
//
// Accounts are the operator's to make: the Go stack starts with an empty
// database and has no open sign-up, so this is how the first account, and
// any other, comes to exist (`soloclef-api set-password`, cmd/api). A
// password changed here ends every device's sign-in, as a changed password
// should.
func SetPassword(ctx context.Context, pool *pgxpool.Pool, hasher *Hasher, address, password string, now time.Time) (userID string, created bool, err error) {
	address = strings.TrimSpace(address)
	if !strings.Contains(address, "@") || strings.ContainsAny(address, " \t\r\n") {
		return "", false, fmt.Errorf("%q is not an email address", address)
	}
	if err := checkNewPassword(address, password); err != nil {
		return "", false, err
	}
	hash, err := hasher.Hash(ctx, password)
	if err != nil {
		return "", false, err
	}
	row, err := database.Write(ctx, pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (SetAccountPasswordRow, error) {
		q := New(tx)
		row, err := q.SetAccountPassword(ctx, SetAccountPasswordParams{Email: address, HashedPassword: &hash})
		if err != nil {
			return row, err
		}
		if err := q.EndEverySignIn(ctx, EndEverySignInParams{EndedAt: now, UserID: row.ID}); err != nil {
			return row, err
		}
		return row, q.ForgetEveryPushDevice(ctx, row.ID)
	})
	if err != nil {
		return "", false, err
	}
	return row.ID, row.Created, nil
}

// ErrNoSuchAccount is an address no account has.
var ErrNoSuchAccount = errors.New("no account has that address")

// SetAdministrator says whether the account with this address administers
// the deployment. The flag on the account is the whole of it: the web app
// shows the admin pages by it and every admin check in this tier reads it,
// so there is no list of addresses in a setting to keep beside it. Like
// accounts themselves it is the operator's to set (`soloclef-api
// set-admin`), never a route's.
func SetAdministrator(ctx context.Context, pool *pgxpool.Pool, address string, administrator bool) (userID string, err error) {
	userID, err = New(pool).SetAdministrator(ctx, SetAdministratorParams{
		Administrator: administrator, Email: strings.TrimSpace(address)})
	if isNoRows(err) {
		return "", ErrNoSuchAccount
	}
	return userID, err
}
