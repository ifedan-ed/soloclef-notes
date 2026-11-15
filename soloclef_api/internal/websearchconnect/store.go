package websearchconnect

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) Connections(ctx context.Context, ownerID string) ([]Connected, error) {
	rows, err := New(s.Pool).ConnectionsOfOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	connected := make([]Connected, len(rows))
	for index, row := range rows {
		connected[index] = Connected{Provider: row.Provider, KeyMask: row.KeyMask, ConnectedAt: row.UpdatedAt}
	}
	return connected, nil
}

func (s Store) Connect(ctx context.Context, ownerID, provider, sealedKey, keyMask string) (Connected, error) {
	row, err := New(s.Pool).Connect(ctx, ConnectParams{UserID: ownerID, Provider: provider, ApiKey: sealedKey, KeyMask: keyMask})
	if err != nil {
		return Connected{}, err
	}
	return Connected{Provider: row.Provider, KeyMask: row.KeyMask, ConnectedAt: row.UpdatedAt}, nil
}

func (s Store) Disconnect(ctx context.Context, ownerID, provider string) (bool, error) {
	removed, err := New(s.Pool).Disconnect(ctx, DisconnectParams{UserID: ownerID, Provider: provider})
	return removed > 0, err
}
