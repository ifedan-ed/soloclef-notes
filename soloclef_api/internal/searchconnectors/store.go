package searchconnectors

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is this package's statements over the pool (queries.sql). Every
// method takes the owner and every statement names it.
type Store struct {
	Pool *pgxpool.Pool
}

// fromRow decodes the config and names the columns. The five statements that
// return a connection return the same columns, so each converts to this one
// row type.
func fromRow(row ConnectionOfOwnerRow) (Connection, error) {
	config, err := readColumn(row.Config)
	if err != nil {
		return Connection{}, err
	}
	return Connection{
		ID:                       row.ID,
		Name:                     row.Name,
		Type:                     string(row.ConnectorType),
		Enabled:                  row.Enabled,
		IsIndexable:              row.IsIndexable,
		LastIndexedAt:            row.LastIndexedAt,
		Config:                   config,
		EnableVisionLLM:          row.EnableVisionLlm,
		PeriodicIndexingEnabled:  row.PeriodicIndexingEnabled,
		IndexingFrequencyMinutes: row.IndexingFrequencyMinutes,
		NextScheduledAt:          row.NextScheduledAt,
		UserID:                   row.UserID,
		CreatedAt:                row.CreatedAt,
		UpdatedAt:                row.UpdatedAt,
	}, nil
}

// one is fromRow for a statement that returns at most one row: no row is nil.
func one(row ConnectionOfOwnerRow, err error) (*Connection, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	connection, err := fromRow(row)
	if err != nil {
		return nil, err
	}
	return &connection, nil
}

func (s Store) Connection(ctx context.Context, ownerID string, connectorID int64) (*Connection, error) {
	return one(New(s.Pool).ConnectionOfOwner(ctx, ConnectionOfOwnerParams{ID: connectorID, UserID: ownerID}))
}

func (s Store) Server(ctx context.Context, ownerID string, connectorID int64) (*Connection, error) {
	row, err := New(s.Pool).ServerOfOwner(ctx, ServerOfOwnerParams{ID: connectorID, UserID: ownerID})
	return one(ConnectionOfOwnerRow(row), err)
}

func (s Store) Connections(ctx context.Context, ownerID string, skip, limit int32) ([]Connection, error) {
	rows, err := New(s.Pool).ConnectionsOfOwner(ctx, ConnectionsOfOwnerParams{UserID: ownerID, Skip: skip, PageSize: limit})
	if err != nil {
		return nil, err
	}
	connections := make([]Connection, 0, len(rows))
	for _, row := range rows {
		connection, err := fromRow(ConnectionOfOwnerRow(row))
		if err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, nil
}

func (s Store) Servers(ctx context.Context, ownerID string) ([]Connection, error) {
	rows, err := New(s.Pool).ServersOfOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	connections := make([]Connection, 0, len(rows))
	for _, row := range rows {
		connection, err := fromRow(ConnectionOfOwnerRow(row))
		if err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, nil
}

func (s Store) SetEnabled(ctx context.Context, ownerID string, connectorID int64, enabled bool) (*Connection, error) {
	row, err := New(s.Pool).SetConnectionEnabled(ctx, SetConnectionEnabledParams{
		Enabled: enabled, ID: connectorID, UserID: ownerID,
	})
	return one(ConnectionOfOwnerRow(row), err)
}

// DeleteServer removes the owner's MCP server and answers the config it
// held; nil is "not found".
func (s Store) DeleteServer(ctx context.Context, ownerID string, connectorID int64) ([]byte, error) {
	config, err := New(s.Pool).DeleteServer(ctx, DeleteServerParams{ID: connectorID, UserID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return config, err
}

// StoreSignIn is SignInWrites': a finished OAuth sign-in.
func (s Store) StoreSignIn(ctx context.Context, ownerID string, connectorID int64, serverURL string, oauth []byte) (bool, error) {
	written, err := New(s.Pool).StoreServerSignIn(ctx, StoreServerSignInParams{
		Oauth: oauth, ID: connectorID, UserID: ownerID, ServerUrl: serverURL,
	})
	return written == 1, err
}

func (s Store) MarkDocumentsDeleting(ctx context.Context, ownerID string, connectorID int64) error {
	_, err := New(s.Pool).MarkConnectionDocumentsDeleting(ctx, MarkConnectionDocumentsDeletingParams{
		ConnectorID: &connectorID, UserID: ownerID,
	})
	return err
}

func (s Store) DocumentIDs(ctx context.Context, ownerID string, connectorID int64, batch int) ([]int64, error) {
	return New(s.Pool).ConnectionDocumentIDs(ctx, ConnectionDocumentIDsParams{
		ConnectorID: &connectorID, UserID: ownerID, Batch: int32(batch),
	})
}

func (s Store) DeleteConnection(ctx context.Context, ownerID string, connectorID int64) (bool, error) {
	deleted, err := New(s.Pool).DeleteConnection(ctx, DeleteConnectionParams{ID: connectorID, UserID: ownerID})
	return deleted == 1, err
}
