package vectorindex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// The collection's shape. These are constants and not settings because they
// are one measured design (custom/docs/vector-store.md, and the Python app's
// app/vector_store/collection.py, which built the live collection the same
// way): changing one means rebuilding the collection, and
// a collection is rebuilt by a reindex, not by editing an environment.
const (
	// Cosine, because the embedding models used are trained for it.
	distance = "Cosine"
	// No global graph (m = 0) and one graph per account (payload_m = 16).
	// Every search is "nearest within one account", so a graph across
	// accounts would be built and never walked.
	hnswM           = 0
	hnswPayloadM    = 16
	hnswEfConstruct = 64
	// An int8 copy of every vector is kept in memory (four times smaller, and
	// under one percent error by Qdrant's own measure); the full-precision
	// originals stay on disk and are read to rescore.
	quantile = 0.99
	// Declared and left empty. A sparse vector cannot be added to a
	// collection later (measured against v1.19.2: "Not existing vector name
	// error"), so server-side keyword search can be tried one day without a
	// rebuild only if the name exists from the start.
	sparseVector = "content_bm25"
)

// TenantField is the payload field a collection is divided by: the account
// that owns the chunk, the uuid in `chunks.user_id`, as a keyword. Its index
// is declared `is_tenant`, which is what makes Qdrant keep one account's
// vectors together and build one graph per account. A search names this
// field first and unconditionally.
//
// The collection is created here, at the indexer's start-up, and not by the
// migrate command: its vector size is the embedding profile's dimension,
// which only this process is configured with, and it is this process that
// must refuse to write when the collection is the wrong shape. Which
// collection is a setting (indexconfig.QdrantCollection); nothing here
// names one.
const TenantField = "user_id"

// Ensure builds the collection, its payload indexes and its alias when they
// are missing, and verifies them when they are not.
//
// A collection that already holds points and has no account index is
// refused, not given one: whatever filled it did not write this payload, so
// its points name no account and every search of it by account would find
// nothing and say nothing was wrong. An empty collection is what goes with
// a new database.
//
// A collection that exists with another vector size or distance is refused.
// Writing this profile's vectors into a collection built by another model is
// the one mistake here that produces an index that looks fine and returns
// noise, so it stops the process at start-up.
func (c *Client) Ensure(ctx context.Context) error {
	collection := "/collections/" + url.PathEscape(c.config.Collection)

	var info collectionInfo
	err := c.call(ctx, http.MethodGet, collection, nil, &info)
	var failure *Failure
	switch {
	case errors.As(err, &failure) && failure.Status == http.StatusNotFound:
		if err := c.call(ctx, http.MethodPut, collection, c.definition(), nil); err != nil {
			return fmt.Errorf("creating collection %s: %w", c.config.Collection, err)
		}
		if err := c.call(ctx, http.MethodGet, collection, nil, &info); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	if err := c.verify(info); err != nil {
		return err
	}
	if _, byAccount := info.PayloadSchema[TenantField]; !byAccount && info.PointsCount > 0 {
		return fmt.Errorf("vectorindex: collection %s holds %d points and has no %s index, so they were not written by this code "+
			"and name no account; give this process a collection name that does not exist yet and it will be created divided by %s",
			c.config.Collection, info.PointsCount, TenantField, TenantField)
	}

	for field, schema := range map[string]any{
		// is_tenant is what makes Qdrant store one account's vectors
		// together, so a search reads one stretch of disk.
		TenantField:         map[string]any{"type": "keyword", "is_tenant": true},
		PayloadDocumentType: "keyword",
		PayloadDocumentID:   "integer",
	} {
		if _, indexed := info.PayloadSchema[field]; indexed {
			continue
		}
		body := map[string]any{"field_name": field, "field_schema": schema}
		if err := c.call(ctx, http.MethodPut, collection+"/index?wait=true", body, nil); err != nil {
			return fmt.Errorf("indexing the %s payload field: %w", field, err)
		}
	}
	return c.ensureAlias(ctx, collection)
}

type collectionInfo struct {
	Config struct {
		Params struct {
			// Vectors is {"size":..,"distance":..} for one unnamed vector and
			// a map of names for named ones; decoding the first shape is the
			// check that it is the first shape.
			Vectors struct {
				Size     int    `json:"size"`
				Distance string `json:"distance"`
			} `json:"vectors"`
		} `json:"params"`
	} `json:"config"`
	PayloadSchema map[string]any `json:"payload_schema"`
	// PointsCount is Qdrant's own count of the collection's points.
	PointsCount int `json:"points_count"`
}

func (c *Client) verify(info collectionInfo) error {
	vectors := info.Config.Params.Vectors
	switch {
	case vectors.Size == 0:
		return fmt.Errorf("vectorindex: collection %s does not hold one unnamed vector per point, which is what this code writes", c.config.Collection)
	case vectors.Size != c.config.Dimension:
		return fmt.Errorf("vectorindex: collection %s holds %d-dimension vectors and the embedding profile is %d; a new embedding choice needs a new collection and a reindex",
			c.config.Collection, vectors.Size, c.config.Dimension)
	case vectors.Distance != distance:
		return fmt.Errorf("vectorindex: collection %s measures %s, not %s", c.config.Collection, vectors.Distance, distance)
	}
	return nil
}

func (c *Client) definition() map[string]any {
	return map[string]any{
		"vectors": map[string]any{
			"size":     c.config.Dimension,
			"distance": distance,
			// The originals are read only to rescore, so they stay on disk.
			"on_disk":     true,
			"hnsw_config": map[string]any{"m": hnswM, "payload_m": hnswPayloadM, "ef_construct": hnswEfConstruct},
		},
		"quantization_config": map[string]any{
			"scalar": map[string]any{"type": "int8", "quantile": quantile, "always_ram": true},
		},
		"sparse_vectors": map[string]any{sparseVector: map[string]any{"modifier": "idf"}},
		// Nothing reads the payload for content, so it need not be resident.
		"on_disk_payload": true,
	}
}

// ensureAlias points the alias at the collection when no alias of that name
// exists. An alias of that name on ANOTHER collection is refused, not moved:
// moving it is the last step of a reindex, and is somebody's decision.
func (c *Client) ensureAlias(ctx context.Context, collection string) error {
	var all struct {
		Aliases []struct {
			Alias      string `json:"alias_name"`
			Collection string `json:"collection_name"`
		} `json:"aliases"`
	}
	if err := c.call(ctx, http.MethodGet, "/aliases", nil, &all); err != nil {
		return err
	}
	for _, alias := range all.Aliases {
		if alias.Alias != c.config.Alias {
			continue
		}
		if alias.Collection != c.config.Collection {
			return fmt.Errorf("vectorindex: the alias %s points at collection %s, and this process was told its collection is %s",
				c.config.Alias, alias.Collection, c.config.Collection)
		}
		return nil
	}
	body := map[string]any{"actions": []any{map[string]any{
		"create_alias": map[string]any{"collection_name": c.config.Collection, "alias_name": c.config.Alias},
	}}}
	if err := c.call(ctx, http.MethodPost, "/collections/aliases", body, nil); err != nil {
		return fmt.Errorf("pointing the alias %s at %s: %w", c.config.Alias, c.config.Collection, err)
	}
	return nil
}
