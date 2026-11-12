package accountprofile

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrAccountGone is the caller's own row not being there.
//
// It is unreachable behind the gate — internal/principal has just read that
// row to decide the account is active — so it is a failure and not a 404,
// which is also what the Python route would do: `auth.user` is the object the
// dependency loaded, and there is no branch for it being absent.
var ErrAccountGone = errors.New("accountprofile: the caller's own account row is gone")

// Store reads the caller's own profile. The writes are free functions taking
// the transaction, as every other slice's are.
type Store struct {
	Pool *pgxpool.Pool
}

// Profile is get_current_user_profile's row.
func (s Store) Profile(ctx context.Context, userID string) (Profile, error) {
	row, err := New(s.Pool).AccountProfile(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrAccountGone
	}
	if err != nil {
		return Profile{}, err
	}
	return Profile{
		ID:          row.ID,
		Email:       row.Email,
		Active:      row.IsActive,
		Superuser:   row.IsSuperuser,
		Verified:    row.IsVerified,
		DisplayName: row.DisplayName,
		AvatarURL:   row.AvatarUrl,
	}, nil
}

// ProfileIn reads the same row on a write's own transaction, which is what the
// update needs: the Python route renders the object it has just updated, so
// the read and the write are one session there and one transaction here.
func ProfileIn(ctx context.Context, tx pgx.Tx, userID string) (Profile, error) {
	row, err := New(tx).AccountProfile(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrAccountGone
	}
	if err != nil {
		return Profile{}, err
	}
	return Profile{
		ID:          row.ID,
		Email:       row.Email,
		Active:      row.IsActive,
		Superuser:   row.IsSuperuser,
		Verified:    row.IsVerified,
		DisplayName: row.DisplayName,
		AvatarURL:   row.AvatarUrl,
	}, nil
}

// Update writes the columns the body mentioned and nothing else, and answers
// with the row as it then stands.
func Update(ctx context.Context, tx pgx.Tx, userID string, changes Changes) (Profile, error) {
	row, err := New(tx).UpdateAccountProfile(ctx, UpdateAccountProfileParams{
		SetDisplayName: changes.DisplayName.Present,
		DisplayName:    changes.DisplayName.Value,
		SetAvatarUrl:   changes.AvatarURL.Present,
		AvatarUrl:      changes.AvatarURL.Value,
		ID:             userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrAccountGone
	}
	if err != nil {
		return Profile{}, err
	}
	return Profile{
		ID:          row.ID,
		Email:       row.Email,
		Active:      row.IsActive,
		Superuser:   row.IsSuperuser,
		Verified:    row.IsVerified,
		DisplayName: row.DisplayName,
		AvatarURL:   row.AvatarUrl,
	}, nil
}

// LockOut is the delete's first commit: deactivate the account and end every
// sign-in it has.
//
// Three statements in one transaction where the Python route runs two
// transactions — `session.commit()` for the deactivation and
// `revoke_all_user_tokens`' own session after it. One transaction is the safe
// direction: the Python route can leave an account deactivated with its
// refresh tokens still live if the second session fails, and nothing reads
// that difference because an inactive account is turned away on every
// credential path anyway. It is recorded because "mirror the question, not the
// intent" is otherwise the rule, and because a row diff of `refresh_tokens`
// after a provoked failure would see it.
func LockOut(ctx context.Context, tx pgx.Tx, userID string, at time.Time) error {
	queries := New(tx)
	if err := queries.DeactivateAccount(ctx, userID); err != nil {
		return err
	}
	if err := queries.RevokeEveryRefreshToken(ctx, RevokeEveryRefreshTokenParams{
		UserID:    userID,
		RevokedAt: &at,
	}); err != nil {
		return err
	}
	return queries.ForgetPushDevices(ctx, userID)
}

// Reactivate is the compensation for an erasure that could not be queued. It
// puts back only `is_active`, as the Python route's own `except` does: the
// refresh tokens stay revoked and the push registrations stay deleted, so the
// account is signed in nowhere and can sign in again.
func Reactivate(ctx context.Context, tx pgx.Tx, userID string) error {
	return New(tx).ReactivateAccount(ctx, userID)
}
