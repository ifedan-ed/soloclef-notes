package accesstokens

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store reads the caller's own keys. The revoke is a free function taking the
// transaction, as the role write's statements are, so a write's statements
// cannot reach the database any other way.
type Store struct {
	Pool *pgxpool.Pool
}

// Tokens is list_personal_access_tokens' query.
func (s Store) Tokens(ctx context.Context, userID string) ([]Token, error) {
	rows, err := New(s.Pool).TokensOfAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	tokens := make([]Token, 0, len(rows))
	for _, row := range rows {
		tokens = append(tokens, Token{
			ID:         row.ID,
			Label:      row.Label,
			Prefix:     row.TokenPrefix,
			ExpiresAt:  row.ExpiresAt,
			LastUsedAt: row.LastUsedAt,
			CreatedAt:  row.CreatedAt,
		})
	}
	return tokens, nil
}

// Revoke deletes one of the caller's own keys. It reports nothing about whether
// a row matched, because the Python route does not look: it answers 204 either
// way, so one person cannot learn another's key ids from it.
func Revoke(ctx context.Context, tx pgx.Tx, userID string, tokenID int64) error {
	return New(tx).RevokeToken(ctx, RevokeTokenParams{ID: tokenID, UserID: userID})
}
