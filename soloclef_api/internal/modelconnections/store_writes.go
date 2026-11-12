package modelconnections

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// StoredCredentials is a connection as a test needs it: where it points
// and what it authenticates with, the key still sealed.
type StoredCredentials struct {
	Provider  string
	BaseURL   string
	SealedKey string
	Extra     []byte
}

// IsAdministrator is whether the account administers the deployment.
func (s Store) IsAdministrator(ctx context.Context, userID string) (bool, error) {
	administrator, err := New(s.Pool).AccountIsAdministrator(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return administrator, err
}

// Create stores a connection and the models picked with it, both or
// neither, and answers the new connection's id. sealedKey is "" for none;
// headers is the sealed headers' object to merge into `extra`, or nil.
func (s Store) Create(ctx context.Context, userID string, fields change, sealedKey string, headers json.RawMessage, picked []pickedModel) (int64, error) {
	extra, err := mergedExtra(fields.extra, headers)
	if err != nil {
		return 0, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	// A rollback after a commit does nothing, so this covers every return.
	defer func() { _ = tx.Rollback(ctx) }()
	queries := New(tx)
	id, err := queries.CreateConnection(ctx, CreateConnectionParams{
		Provider: fields.provider, BaseUrl: orNull(fields.baseURL), SealedKey: orNull(sealedKey),
		Extra: extra, Enabled: fields.enabled, UserID: userID,
	})
	if err != nil {
		return 0, err
	}
	if len(picked) > 0 {
		encoded, err := json.Marshal(picked)
		if err != nil {
			return 0, err
		}
		if err := queries.AddModels(ctx, AddModelsParams{Picked: encoded, ConnectionID: id, UserID: userID}); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit(ctx)
}

// Update changes the fields a body named on the caller's own connection.
// found is false for an id that is not the caller's. headers replaces the
// connection's headers when fields named them.
func (s Store) Update(ctx context.Context, userID string, id int64, fields change, sealedKey string, headers json.RawMessage) (found bool, err error) {
	extra := fields.extra
	if !fields.setExtra {
		// The statement casts the argument whether or not it uses it.
		extra = []byte(`{}`)
	}
	if !fields.setHeaders || headers == nil {
		headers = json.RawMessage(`{}`)
	}
	changed, err := New(s.Pool).UpdateConnection(ctx, UpdateConnectionParams{
		SetProvider: fields.setProvider, Provider: fields.provider,
		SetBaseUrl: fields.setBaseURL, BaseUrl: orNull(fields.baseURL),
		SetKey: fields.setKey, SealedKey: orNull(sealedKey),
		SetExtra: fields.setExtra, Extra: extra,
		SetHeaders: fields.setHeaders, Headers: headers,
		SetEnabled: fields.setEnabled, Enabled: fields.enabled,
		ID: id, UserID: userID,
	})
	return changed > 0, err
}

// mergedExtra is a new connection's `extra` with its sealed headers in it.
func mergedExtra(extra []byte, headers json.RawMessage) ([]byte, error) {
	if len(headers) == 0 || string(headers) == `{}` {
		return extra, nil
	}
	var fields, sealed map[string]json.RawMessage
	if err := json.Unmarshal(extra, &fields); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(headers, &sealed); err != nil {
		return nil, err
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	for key, value := range sealed {
		fields[key] = value
	}
	return json.Marshal(fields)
}

// Delete removes the caller's own connection and its models.
func (s Store) Delete(ctx context.Context, userID string, id int64) (found bool, err error) {
	removed, err := New(s.Pool).DeleteConnection(ctx, DeleteConnectionParams{ID: id, UserID: userID})
	return removed > 0, err
}

// One is the caller's own connection with its models, as the list holds
// one.
func (s Store) One(ctx context.Context, userID string, id int64) (ListedConnection, bool, error) {
	queries := New(s.Pool)
	row, err := queries.ConnectionOfAccount(ctx, ConnectionOfAccountParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ListedConnection{}, false, nil
	}
	if err != nil {
		return ListedConnection{}, false, err
	}
	connection := ListedConnection{
		ID: row.ID, Provider: row.Provider, BaseURL: row.BaseUrl, HasAPIKey: row.HasApiKey,
		Extra: row.Extra, Scope: row.Scope, UserID: row.UserID, Enabled: row.Enabled, CreatedAt: row.CreatedAt,
	}
	models, err := queries.ModelsOfConnection(ctx, ModelsOfConnectionParams{ConnectionID: id, UserID: userID})
	if err != nil {
		return ListedConnection{}, false, err
	}
	for _, model := range models {
		connection.Models = append(connection.Models, ListedModel{
			ID: model.ID, ConnectionID: model.ConnectionID, ModelID: model.ModelID, DisplayName: model.DisplayName,
			Source: string(model.Source), SupportsChat: model.SupportsChat, MaxInputTokens: model.MaxInputTokens,
			SupportsImageInput: model.SupportsImageInput, SupportsTools: model.SupportsTools,
			SupportsImageGeneration: model.SupportsImageGeneration, CapabilitiesOverride: model.CapabilitiesOverride,
			Enabled: model.Enabled, Catalog: model.Catalog, CreatedAt: model.CreatedAt,
		})
	}
	return connection, true, nil
}

// Credentials is what the caller's own connection authenticates with.
func (s Store) Credentials(ctx context.Context, userID string, id int64) (StoredCredentials, bool, error) {
	row, err := New(s.Pool).ConnectionForTest(ctx, ConnectionForTestParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredCredentials{}, false, nil
	}
	if err != nil {
		return StoredCredentials{}, false, err
	}
	return StoredCredentials{Provider: row.Provider, BaseURL: orEmpty(row.BaseUrl), SealedKey: orEmpty(row.SealedKey), Extra: row.Extra}, true, nil
}

// ModelCredentials is a stored model of one of the caller's own
// connections: the provider's name for it and what its connection
// authenticates with.
func (s Store) ModelCredentials(ctx context.Context, userID string, modelID int64) (string, StoredCredentials, bool, error) {
	row, err := New(s.Pool).ModelForTest(ctx, ModelForTestParams{ID: modelID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", StoredCredentials{}, false, nil
	}
	if err != nil {
		return "", StoredCredentials{}, false, err
	}
	return row.ModelID, StoredCredentials{Provider: row.Provider, BaseURL: orEmpty(row.BaseUrl), SealedKey: orEmpty(row.SealedKey), Extra: row.Extra}, true, nil
}

// orNull is the column value of an optional text: NULL for none.
func orNull(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func orEmpty(column *string) string {
	if column == nil {
		return ""
	}
	return *column
}
