package modelconnections

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the package's statements over the pool: the two of the list
// here, the writes and the tests' in store_writes.go.
//
// Neither statement of the list selects `connections.api_key`. The database
// is asked whether a key is saved and answers a boolean, so the key is not
// in this process's memory on that path (queries.sql; package comment, "The
// key").
type Store struct {
	Pool *pgxpool.Pool
}

// ConnectionsOfAccount is the account's own connections, by id.
func (s Store) ConnectionsOfAccount(ctx context.Context, userID string) ([]ListedConnection, error) {
	rows, err := New(s.Pool).ConnectionsOfAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	listed := make([]ListedConnection, 0, len(rows))
	for _, row := range rows {
		listed = append(listed, ListedConnection{
			ID:        row.ID,
			Provider:  row.Provider,
			BaseURL:   row.BaseUrl,
			HasAPIKey: row.HasApiKey,
			Extra:     row.Extra,
			Scope:     row.Scope,
			UserID:    row.UserID,
			Enabled:   row.Enabled,
			CreatedAt: row.CreatedAt,
		})
	}
	return listed, nil
}

// ModelsOfAccount is every model of the account's own connections, by id.
func (s Store) ModelsOfAccount(ctx context.Context, userID string) ([]ListedModel, error) {
	rows, err := New(s.Pool).ModelsOfAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	listed := make([]ListedModel, 0, len(rows))
	for _, row := range rows {
		listed = append(listed, ListedModel{
			ID:                      row.ID,
			ConnectionID:            row.ConnectionID,
			ModelID:                 row.ModelID,
			DisplayName:             row.DisplayName,
			Source:                  string(row.Source),
			SupportsChat:            row.SupportsChat,
			MaxInputTokens:          row.MaxInputTokens,
			SupportsImageInput:      row.SupportsImageInput,
			SupportsTools:           row.SupportsTools,
			SupportsImageGeneration: row.SupportsImageGeneration,
			CapabilitiesOverride:    row.CapabilitiesOverride,
			Enabled:                 row.Enabled,
			Catalog:                 row.Catalog,
			CreatedAt:               row.CreatedAt,
		})
	}
	return listed, nil
}
