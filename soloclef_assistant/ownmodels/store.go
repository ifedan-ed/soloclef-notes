package ownmodels

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Option is one model of an account's own connection as a model menu
// offers it.
type Option struct {
	// ID is the name a menu offers and a choice stores (Choice).
	ID string
	// RowID is the models row.
	RowID int64
	// ModelID is the endpoint's own name for the model, the one a call
	// sends.
	ModelID string
	// Label is what the menu shows: the name the person gave the model, or
	// the endpoint's.
	Label string
	// Connection is the host of the connection's address, so the menu can
	// say whose model it is.
	Connection string
	// Chat says the model answers a conversation: not stated otherwise.
	// Vision says it may be shown pictures (not stated otherwise), Images
	// that it draws (stated so). The menus filter on these as the proxy's
	// lists are filtered on the proxy's modes.
	Chat   bool
	Vision bool
	Images bool
}

// Endpoint is what calling one model of an account's own connection needs.
// The key and the headers are still sealed.
type Endpoint struct {
	ModelID string
	Label   string
	BaseURL string
	// Enabled is false when the model or its connection is switched off.
	Enabled       bool
	SealedKey     string
	SealedHeaders string
}

// Store is the package's statements over the pool (queries.sql).
type Store struct {
	Pool *pgxpool.Pool
}

// Menu is the account's own models a menu may offer, grouped by connection.
func (s Store) Menu(ctx context.Context, userID string) ([]Option, error) {
	rows, err := New(s.Pool).MenuOfAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	options := make([]Option, 0, len(rows))
	for _, row := range rows {
		label := row.ModelID
		if row.DisplayName != nil && *row.DisplayName != "" {
			label = *row.DisplayName
		}
		base := ""
		if row.BaseUrl != nil {
			base = *row.BaseUrl
		}
		// Unknown is "not said otherwise" for chat and pictures in, as the
		// proxy's unstated modes are; drawing must be stated, because a
		// chat model sent a drawing request fails.
		chat := row.SupportsChat == nil || *row.SupportsChat
		options = append(options, Option{
			ID: Choice(row.ID), RowID: row.ID, ModelID: row.ModelID, Label: label, Connection: HostOf(base),
			Chat:   chat,
			Vision: chat && (row.SupportsImageInput == nil || *row.SupportsImageInput),
			Images: row.SupportsImageGeneration != nil && *row.SupportsImageGeneration,
		})
	}
	return options, nil
}

// Endpoint is one model of the account's own connection; found is false for
// a row that is not the account's, not an OpenAI-compatible connection's, or
// not there.
func (s Store) Endpoint(ctx context.Context, userID string, id int64) (Endpoint, bool, error) {
	row, err := New(s.Pool).EndpointOfModel(ctx, EndpointOfModelParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Endpoint{}, false, nil
	}
	if err != nil {
		return Endpoint{}, false, err
	}
	endpoint := Endpoint{
		ModelID: row.ModelID, Label: row.ModelID, Enabled: row.ModelEnabled && row.ConnectionEnabled,
		SealedHeaders: row.SealedHeaders,
	}
	if row.DisplayName != nil && *row.DisplayName != "" {
		endpoint.Label = *row.DisplayName
	}
	if row.BaseUrl != nil {
		endpoint.BaseURL = *row.BaseUrl
	}
	if row.SealedKey != nil {
		endpoint.SealedKey = *row.SealedKey
	}
	return endpoint, true, nil
}

// AllowedHosts is the operator's list (site_settings
// `model_connection_allowed_hosts`): a JSON array of host names or
// addresses. No row is an empty list, which means what it says: no host
// may be reached inside this server's network. A row that is not a list of
// text is an error, so a typo cannot quietly allow nothing or everything.
func (s Store) AllowedHosts(ctx context.Context) ([]string, error) {
	raw, err := New(s.Pool).AllowedHosts(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var hosts []string
	if err := json.Unmarshal(raw, &hosts); err != nil {
		return nil, errors.New("ownmodels: the site setting model_connection_allowed_hosts must be a JSON list of host names")
	}
	return hosts, nil
}
