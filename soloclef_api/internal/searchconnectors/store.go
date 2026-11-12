package searchconnectors

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
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

func (s Store) DeleteServer(ctx context.Context, ownerID string, connectorID int64) (bool, error) {
	deleted, err := New(s.Pool).DeleteServer(ctx, DeleteServerParams{ID: connectorID, UserID: ownerID})
	return deleted > 0, err
}

// errConfigNotAnObject is a server whose config cannot take a key. Nothing
// this application writes makes one; it is refused and not repaired, because
// replacing the config would throw the server's address and key away.
var errConfigNotAnObject = errors.New("searchconnectors: the server's config is not an object")

// EditTrustedTools is the read, the decision and the write of one edit, in
// one transaction with the row locked from the read on. Two edits made at
// once therefore queue, and the second decides from the list the first
// wrote. Reading the whole config, changing it in memory and writing it back
// (what this used to do) lost one of two such edits.
func (s Store) EditTrustedTools(ctx context.Context, ownerID string, connectorID int64,
	edit func(trusted []string) ([]string, bool)) (trusted []string, stored, found bool, err error) {
	type outcome struct {
		trusted       []string
		stored, found bool
	}
	result, err := database.Write(ctx, s.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (outcome, error) {
			queries := New(tx)
			raw, err := queries.LockTrustedTools(ctx, LockTrustedToolsParams{ID: connectorID, UserID: ownerID})
			if errors.Is(err, pgx.ErrNoRows) {
				return outcome{}, nil
			}
			if err != nil {
				return outcome{}, err
			}
			next, changed := edit(storedToolNames(raw))
			if !changed {
				return outcome{trusted: next, found: true}, nil
			}
			text, err := json.Marshal(next)
			if err != nil {
				return outcome{}, err
			}
			written, err := queries.StoreTrustedTools(ctx, StoreTrustedToolsParams{
				TrustedTools: text, ID: connectorID, UserID: ownerID,
			})
			if err != nil {
				return outcome{}, err
			}
			if written == 0 {
				return outcome{}, errConfigNotAnObject
			}
			return outcome{trusted: next, stored: true, found: true}, nil
		})
	return result.trusted, result.stored, result.found, err
}

// storedToolNames reads the stored list leniently: a list that is absent,
// null or not a list is no names, and an entry that is not a string is not a
// name. The edit then writes a clean list in its place.
func storedToolNames(raw []byte) []string {
	names := []string{}
	var listed []any
	if json.Unmarshal(raw, &listed) != nil {
		return names
	}
	for _, item := range listed {
		if name, isText := item.(string); isText {
			names = append(names, name)
		}
	}
	return names
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
