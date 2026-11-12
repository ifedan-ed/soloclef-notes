package searchconnectors

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNameTaken is an edit that would give a connection the name of another
// of the same type the account already has.
var ErrNameTaken = errors.New("searchconnectors: the account already has a connection of this type with this name")

// uniqueViolation is Postgres's code for a row a unique constraint refuses.
const uniqueViolation = "23505"

func nameTakenOr(err error) error {
	var refused *pgconn.PgError
	if errors.As(err, &refused) && refused.Code == uniqueViolation {
		return ErrNameTaken
	}
	return err
}

// IsAdministrator reports the account's own `is_superuser` flag. An account
// that is no longer there administers nothing.
func (s Store) IsAdministrator(ctx context.Context, userID string) (bool, error) {
	administers, err := New(s.Pool).OwnerIsAdministrator(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return administers, err
}

func (s Store) SiteAllowedHosts(ctx context.Context) ([]byte, error) {
	stored, err := New(s.Pool).SiteAllowedHosts(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return stored, err
}

// NewConnection is what a create writes. The owner is a separate argument
// of Create so that no caller can forget it.
type NewConnection struct {
	Name                     string
	Type                     string
	IsIndexable              bool
	Config                   []byte
	EnableVisionLLM          bool
	PeriodicIndexingEnabled  bool
	IndexingFrequencyMinutes *int64
	NextScheduledAt          *time.Time
}

// Create inserts the owner's connection. When the owner already has one of
// that type and name, nothing is written and existing is that one.
func (s Store) Create(ctx context.Context, ownerID string, connection NewConnection) (created *Connection, existing *Connection, err error) {
	queries := New(s.Pool)
	row, err := queries.InsertConnection(ctx, InsertConnectionParams{
		UserID: ownerID, Name: connection.Name, ConnectorType: Searchsourceconnectortype(connection.Type),
		IsIndexable: connection.IsIndexable, Config: connection.Config, EnableVisionLlm: connection.EnableVisionLLM,
		PeriodicIndexingEnabled:  connection.PeriodicIndexingEnabled,
		IndexingFrequencyMinutes: connection.IndexingFrequencyMinutes,
		NextScheduledAt:          connection.NextScheduledAt,
	})
	if created, err = one(ConnectionOfOwnerRow(row), err); err != nil || created != nil {
		return created, nil, err
	}
	held, err := queries.ConnectionOfOwnerByName(ctx, ConnectionOfOwnerByNameParams{
		UserID: ownerID, ConnectorType: Searchsourceconnectortype(connection.Type), Name: connection.Name,
	})
	// No row here means the other one was removed between the two
	// statements; the caller is told the name was taken, which it was.
	existing, err = one(ConnectionOfOwnerRow(held), err)
	if err == nil && existing == nil {
		err = ErrNameTaken
	}
	return nil, existing, err
}

// UpdateServer edits the owner's MCP server: its name, its settings, or
// both; nil leaves one alone. No row back means it is not the owner's, is
// not an MCP server, or holds a config that cannot take a key.
func (s Store) UpdateServer(ctx context.Context, ownerID string, connectorID int64, name *string, serverConfig []byte) (*Connection, error) {
	row, err := New(s.Pool).UpdateServer(ctx, UpdateServerParams{
		Name: name, ServerConfig: serverConfig, ID: connectorID, UserID: ownerID,
	})
	return one(ConnectionOfOwnerRow(row), nameTakenOr(err))
}

// ConnectionEdit is an edit of a connection that is not an MCP server. The
// three schedule fields are written as given; the rest only when set.
type ConnectionEdit struct {
	Name *string
	// Settings is a JSON object merged over the stored config, or nil.
	Settings                 []byte
	EnableVisionLLM          *bool
	PeriodicIndexingEnabled  bool
	IndexingFrequencyMinutes *int64
	NextScheduledAt          *time.Time
}

func (s Store) UpdateConnection(ctx context.Context, ownerID string, connectorID int64, edit ConnectionEdit) (*Connection, error) {
	row, err := New(s.Pool).UpdateConnection(ctx, UpdateConnectionParams{
		Name: edit.Name, Settings: edit.Settings, EnableVisionLlm: edit.EnableVisionLLM,
		PeriodicIndexingEnabled:  edit.PeriodicIndexingEnabled,
		IndexingFrequencyMinutes: edit.IndexingFrequencyMinutes,
		NextScheduledAt:          edit.NextScheduledAt,
		ID:                       connectorID, UserID: ownerID,
	})
	return one(ConnectionOfOwnerRow(row), nameTakenOr(err))
}
