package modelconnections

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
)

// The statements of a connection's models. Each names the caller as the
// connection's owner (queries.sql), so "not found" covers another
// account's and the operator's alike.

// Provider is which provider the caller's own connection is to.
func (s Store) Provider(ctx context.Context, userID string, id int64) (string, bool, error) {
	provider, err := New(s.Pool).ConnectionProvider(ctx, ConnectionProviderParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return provider, err == nil, err
}

// ModelProvider is which provider a model of the caller's own connection is
// reached through.
func (s Store) ModelProvider(ctx context.Context, userID string, modelID int64) (string, bool, error) {
	provider, err := New(s.Pool).ModelProvider(ctx, ModelProviderParams{ID: modelID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return provider, err == nil, err
}

// ModelToVerify is the model the caller's own connection is tried with;
// found is false when it has none.
func (s Store) ModelToVerify(ctx context.Context, userID string, id int64) (string, bool, error) {
	model, err := New(s.Pool).ModelToVerify(ctx, ModelToVerifyParams{ConnectionID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return model, err == nil, err
}

// StoreDiscovered writes what the catalogue lists onto the caller's own
// connection and answers all of its models, by id.
func (s Store) StoreDiscovered(ctx context.Context, userID string, id int64, listed []pickedModel) ([]ListedModel, error) {
	queries := New(s.Pool)
	if len(listed) > 0 {
		encoded, err := json.Marshal(listed)
		if err != nil {
			return nil, err
		}
		if err := queries.StoreDiscoveredModels(ctx, StoreDiscoveredModelsParams{Listed: encoded, ConnectionID: id, UserID: userID}); err != nil {
			return nil, err
		}
	}
	rows, err := queries.ModelsOfConnection(ctx, ModelsOfConnectionParams{ConnectionID: id, UserID: userID})
	if err != nil {
		return nil, err
	}
	models := make([]ListedModel, 0, len(rows))
	for _, row := range rows {
		models = append(models, listedModel(row))
	}
	return models, nil
}

// SetModelsEnabled switches the named models of the caller's own
// connection and answers the ones it changed, by id.
func (s Store) SetModelsEnabled(ctx context.Context, userID string, id int64, modelIDs []int64, enabled bool) ([]ListedModel, error) {
	rows, err := New(s.Pool).SetModelsEnabled(ctx, SetModelsEnabledParams{
		Enabled: enabled, ConnectionID: id, UserID: userID, ModelIds: modelIDs,
	})
	if err != nil {
		return nil, err
	}
	models := make([]ListedModel, 0, len(rows))
	for _, row := range rows {
		models = append(models, listedModel(ModelsOfConnectionRow(row)))
	}
	// RETURNING promises no order.
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

// UpdateModel changes the fields a body named on a model of one of the
// caller's own connections.
func (s Store) UpdateModel(ctx context.Context, userID string, modelID int64, fields modelChange) (ListedModel, bool, error) {
	override := fields.capabilitiesOverride
	if !fields.setCapabilitiesOverride {
		// The statement casts the argument whether or not it uses it.
		override = []byte(`{}`)
	}
	row, err := New(s.Pool).UpdateModel(ctx, UpdateModelParams{
		SetDisplayName: fields.setDisplayName, DisplayName: fields.displayName,
		SetEnabled: fields.setEnabled, Enabled: fields.enabled,
		SetSupportsChat: fields.setSupportsChat, SupportsChat: fields.supportsChat,
		SetMaxInputTokens: fields.setMaxInputTokens, MaxInputTokens: fields.maxInputTokens,
		SetSupportsImageInput: fields.setSupportsImageInput, SupportsImageInput: fields.supportsImageInput,
		SetSupportsTools: fields.setSupportsTools, SupportsTools: fields.supportsTools,
		SetSupportsImageGeneration: fields.setSupportsImageGeneration, SupportsImageGeneration: fields.supportsImageGeneration,
		SetCapabilitiesOverride: fields.setCapabilitiesOverride, CapabilitiesOverride: override,
		ID: modelID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ListedModel{}, false, nil
	}
	if err != nil {
		return ListedModel{}, false, err
	}
	return listedModel(ModelsOfConnectionRow(row)), true, nil
}

// listedModel is a model row as the routes hold one. Every statement that
// returns a model returns these columns, so each one's row converts to
// this one's.
func listedModel(row ModelsOfConnectionRow) ListedModel {
	return ListedModel{
		ID: row.ID, ConnectionID: row.ConnectionID, ModelID: row.ModelID, DisplayName: row.DisplayName,
		Source: string(row.Source), SupportsChat: row.SupportsChat, MaxInputTokens: row.MaxInputTokens,
		SupportsImageInput: row.SupportsImageInput, SupportsTools: row.SupportsTools,
		SupportsImageGeneration: row.SupportsImageGeneration, CapabilitiesOverride: row.CapabilitiesOverride,
		Enabled: row.Enabled, Catalog: row.Catalog, CreatedAt: row.CreatedAt,
	}
}

// GlobalConnections is the operator's connections with their models, by
// id, as any signed-in account may see them: no address, no `extra`.
func (s Store) GlobalConnections(ctx context.Context) ([]ListedConnection, error) {
	queries := New(s.Pool)
	rows, err := queries.GlobalConnections(ctx)
	if err != nil || len(rows) == 0 {
		return []ListedConnection{}, err
	}
	connections := make([]ListedConnection, 0, len(rows))
	for _, row := range rows {
		connections = append(connections, ListedConnection{
			ID: row.ID, Provider: row.Provider, HasAPIKey: row.HasApiKey, Extra: []byte(`{}`),
			Scope: "global", Enabled: row.Enabled, CreatedAt: row.CreatedAt,
		})
	}
	models, err := queries.GlobalModels(ctx)
	if err != nil {
		return nil, err
	}
	listed := make([]ListedModel, 0, len(models))
	for _, row := range models {
		listed = append(listed, listedModel(ModelsOfConnectionRow(row)))
	}
	AttachModels(connections, listed)
	return connections, nil
}

// GlobalConnectionExists is whether the operator has given the site a
// connection that is switched on.
func (s Store) GlobalConnectionExists(ctx context.Context) (bool, error) {
	return New(s.Pool).GlobalConnectionExists(ctx)
}
