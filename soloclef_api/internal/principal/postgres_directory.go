package principal

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresDirectory reads account state from the same tables the Python tier
// reads: fastapi-users' "user" table and app.db's personal_access_tokens.
type PostgresDirectory struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
}

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

// PersonalAccessTokenOwner is resolve_pat's query: the hash must match, the key
// must not have expired, and the account must be active.
func (d PostgresDirectory) PersonalAccessTokenOwner(
	ctx context.Context, tokenHash string, now time.Time,
) (PersonalAccessTokenOwner, bool, error) {
	var (
		owner      PersonalAccessTokenOwner
		lastUsedAt *time.Time
	)
	err := d.Pool.QueryRow(ctx, `
		SELECT token.id, token.user_id::text, token.last_used_at
		FROM personal_access_tokens AS token
		JOIN "user" AS account ON account.id = token.user_id
		WHERE token.token_hash = $1
		  AND (token.expires_at IS NULL OR token.expires_at > $2)
		  AND account.is_active = true
		ORDER BY token.id
		LIMIT 1`, tokenHash, now,
	).Scan(&owner.TokenID, &owner.UserID, &lastUsedAt)
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
