package principal

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresDirectory reads account state: the "user" table, the sign-ins in
// refresh_tokens, and the API keys in personal_access_tokens.
type PostgresDirectory struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
}

// UserIsActive reports whether the account exists and is active. The gate
// asks SessionIsLive instead; this is for internal/apphandoff, whose one-time
// codes and tickets act for an account with no session behind them.
func (d PostgresDirectory) UserIsActive(ctx context.Context, userID string) (bool, error) {
	var isActive bool
	err := d.Pool.QueryRow(ctx,
		`SELECT is_active FROM "user" WHERE id = $1::uuid`, userID,
	).Scan(&isActive)
	if errors.Is(err, pgx.ErrNoRows) {
		// user_manager.get raises UserNotExists, which get_auth_context treats
		// as no principal rather than as an error.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return isActive, nil
}

// SessionIsLive is the gate's question about an access token, asked on every
// request a session makes: is the account active, and does the sign-in the
// token names still hold a usable refresh token?
//
// Why this and not a revoked list in Valkey or a per-account "valid after"
// time: the gate already made one Postgres round trip per session request,
// to read is_active. The sign-in's state is answered by the same statement,
// so ending sign-ins at once costs no extra round trip, and the answer is
// the refresh_tokens rows themselves -- the very rows every way of ending a
// sign-in already writes, in the transaction that ends it. There is no
// second store to write after the commit, no write that can be lost between
// the two, and no second service the gate depends on; the operator's
// `set-password`, which opens only the database, ends sign-ins like the
// routes do. Signing out everywhere and a new password need no per-account
// time either, because both end every family of the account.
//
// A family is live while one of its tokens is unspent and unexpired: a
// rotation spends the old token and inserts its successor in one
// transaction, so a sign-in is never seen without one; ending a sign-in
// (EndFamily, EndEverySignIn, EndTokenByHash, the account deletion's revoke)
// spends them all. It is the definition the devices list uses
// (internal/signin, SignedInDevices), so a device is listed exactly while
// its access tokens work. The expiry clauses also end an access token at
// its sign-in's cap, rather than up to an hour after it.
//
// It costs a primary-key probe and one probe of the partial index on a
// family's unspent tokens (ix_refresh_tokens_live_family): a phone that
// refreshes hourly for a year has thousands of spent rows and one unspent.
//
// It fails closed: a database that cannot answer is the gate's 500 (gate.go),
// never a session let in unchecked -- the same answer the is_active read
// always gave, and with no Postgres no route would work anyway.
func (d PostgresDirectory) SessionIsLive(ctx context.Context, userID, sessionID string, now time.Time) (bool, error) {
	var live bool
	err := d.Pool.QueryRow(ctx, sessionIsLiveSQL, userID, sessionID, now).Scan(&live)
	return live, err
}

// sessionIsLiveSQL is SessionIsLive's statement: $1 the account, $2 the
// sign-in, $3 now. A constant so the test that reads its plan reads this one.
const sessionIsLiveSQL = `
	SELECT EXISTS (
		SELECT 1
		FROM "user" AS account
		JOIN refresh_tokens AS token ON token.user_id = account.id
		WHERE account.id = $1::uuid
		  AND account.is_active
		  AND token.family_id = $2::uuid
		  AND token.revoked_at IS NULL
		  AND token.expires_at > $3
		  AND (token.absolute_expiry IS NULL OR token.absolute_expiry > $3)
	)`

// PersonalAccessTokenOwner finds a key by its hash: the key must not have
// expired and the account must be active. The account's API-access switch
// comes back on the same row, which is already joined, so the gate's check of
// it costs nothing.
func (d PostgresDirectory) PersonalAccessTokenOwner(
	ctx context.Context, tokenHash string, now time.Time,
) (PersonalAccessTokenOwner, bool, error) {
	var (
		owner      PersonalAccessTokenOwner
		lastUsedAt *time.Time
	)
	err := d.Pool.QueryRow(ctx, `
		SELECT token.id, token.user_id::text, token.last_used_at, account.api_access_enabled
		FROM personal_access_tokens AS token
		JOIN "user" AS account ON account.id = token.user_id
		WHERE token.token_hash = $1
		  AND (token.expires_at IS NULL OR token.expires_at > $2)
		  AND account.is_active = true
		ORDER BY token.id
		LIMIT 1`, tokenHash, now,
	).Scan(&owner.TokenID, &owner.UserID, &lastUsedAt, &owner.APIAccessEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return PersonalAccessTokenOwner{}, false, nil
	}
	if err != nil {
		return PersonalAccessTokenOwner{}, false, err
	}
	if lastUsedAt != nil {
		owner.LastUsedAt = *lastUsedAt
	}
	return owner, true, nil
}

// TouchPersonalAccessToken writes last_used_at away from the request, as
// _touch_last_used does: the caller's response never waits for it and never
// fails because of it.
func (d PostgresDirectory) TouchPersonalAccessToken(tokenID int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 5*time.Second)
		defer cancel()
		_, err := d.Pool.Exec(ctx,
			`UPDATE personal_access_tokens SET last_used_at = now() WHERE id = $1`, tokenID)
		if err != nil {
			d.Logger.Warn("could not record when an API key was last used",
				slog.Int64("token_id", tokenID), slog.String("error", err.Error()))
		}
	}()
}
