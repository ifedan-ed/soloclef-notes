package websearch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// StoredKeys reads a provider's key from the account's web search
// connection (web_search_connections) and opens it.
//
// The key is the one the person entered when they connected Exa or Tavily
// in their connections; the API tier checked it against the provider and
// sealed it before storing it (internal/websearchconnect). It is read and
// opened at each search, and kept nowhere, so a key removed in settings is
// gone from the next search in every process.
type StoredKeys struct {
	Pool *pgxpool.Pool
	// Seal opens the key: sealedsecret under the web search key purpose,
	// from the deployment's secret.
	Seal sealedsecret.Seal
}

func (s StoredKeys) Key(ctx context.Context, userID string, provider Provider) (string, bool, error) {
	if !s.Seal.Ready() {
		return "", false, errors.New("websearch: the key seal is not set")
	}
	sealed, err := New(s.Pool).SealedKey(ctx, SealedKeyParams{UserID: userID, Provider: string(provider)})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	// Bound to the owner's id in lower case, the one spelling the API
	// tier seals under, whatever case this request's token used.
	key, err := s.Seal.Open(strings.ToLower(userID), sealed)
	if err != nil {
		// Not this deployment's secret, or a damaged row: the person must
		// enter the key again, and nothing else will do.
		return "", false, fmt.Errorf("websearch: the stored %s key cannot be opened: %w", provider.label(), err)
	}
	return key, true, nil
}
