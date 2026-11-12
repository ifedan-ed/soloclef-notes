package documents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Qdrant is the vector store, spoken to over its HTTP API.
//
// The collection is the one the indexer writes: one collection for every
// account, a point per chunk whose id is the chunk's Postgres id and whose
// payload carries the owning account's uuid as a keyword (tenantField, the
// collection's tenant key). A search is always filtered to one account;
// there is no call here that is not.
//
// HTTP rather than gRPC: a query is one request with a 1024-number body, and
// the standard library's client is one dependency fewer than a generated
// gRPC stub and its runtime. BenchmarkNearest measures the encoding.
type Qdrant struct {
	// URL is the server's HTTP address, e.g. http://qdrant:6333.
	URL string
	// APIKey is sent when set. A server without one takes none.
	APIKey string
	// Collection is the collection or alias the indexer writes to.
	Collection string
	// HNSWEf is the search-time breadth of the graph walk: higher finds
	// more of the true neighbours and costs more.
	HNSWEf int
	// Quantized says the collection stores quantized vectors, in which case
	// Rescore and Oversampling say how a search uses them. They are the
	// collection's settings, so they are stated, not guessed.
	Quantized    bool
	Rescore      bool
	Oversampling float64
	HTTP         *http.Client
}

// Check reports what is missing. Called once at start-up.
func (q Qdrant) Check() error {
	switch {
	case strings.TrimSpace(q.URL) == "":
		return errors.New("documents: the vector store's address is not set")
	case q.Collection == "":
		return errors.New("documents: the vector store's collection is not set")
	case q.HNSWEf < 1:
		return fmt.Errorf("documents: the vector store's hnsw_ef must be positive, got %d", q.HNSWEf)
	case q.Quantized && q.Oversampling < 1:
		return fmt.Errorf("documents: the vector store's oversampling must be at least 1 when vectors are quantized, got %v", q.Oversampling)
	case q.HTTP == nil:
		return errors.New("documents: the vector store's HTTP client is not set")
	}
	return nil
}

// tenantField is the payload field the collection is divided by: the uuid in
// `chunks.user_id`, as text. It is the indexer's name for it
// (soloclef_worker/internal/vectorindex.TenantField); the two are different
// modules, so the name is repeated here and a test holds the request to it.
const tenantField = "user_id"

type qdrantQuery struct {
	Query       []float32    `json:"query"`
	Filter      qdrantFilter `json:"filter"`
	Limit       int          `json:"limit"`
	WithPayload bool         `json:"with_payload"`
	WithVector  bool         `json:"with_vector"`
	Params      qdrantParams `json:"params"`
}

type qdrantFilter struct {
	Must []any `json:"must"`
}

type qdrantParams struct {
	HNSWEf       int                 `json:"hnsw_ef"`
	Quantization *qdrantQuantization `json:"quantization,omitempty"`
}

type qdrantQuantization struct {
	Ignore       bool    `json:"ignore"`
	Rescore      bool    `json:"rescore"`
	Oversampling float64 `json:"oversampling"`
}

func (q Qdrant) Nearest(ctx context.Context, userID string, vector []float32, chunkIDs []int64, limit int) ([]Neighbor, error) {
	// The tenant condition is built first and unconditionally. An empty
	// account id is refused here rather than sent: a keyword match on ""
	// finds nothing today, and "nothing" must not depend on that.
	if userID == "" {
		return nil, errors.New("the search names no account")
	}
	// The value is lowered because the indexer writes the uuid as Postgres
	// prints it, in lower case, and a keyword match is exact: a token that
	// carried the id in capitals would otherwise match no point and the
	// search would quietly find nothing.
	must := []any{map[string]any{
		"key":   tenantField,
		"match": map[string]string{"value": strings.ToLower(userID)},
	}}
	if chunkIDs != nil {
		if len(chunkIDs) == 0 {
			return nil, nil
		}
		must = append(must, map[string]any{"has_id": chunkIDs})
	}
	query := qdrantQuery{Query: vector, Filter: qdrantFilter{Must: must}, Limit: limit, Params: qdrantParams{HNSWEf: q.HNSWEf}}
	if q.Quantized {
		query.Params.Quantization = &qdrantQuantization{Rescore: q.Rescore, Oversampling: q.Oversampling}
	}
	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}
	address := strings.TrimRight(q.URL, "/") + "/collections/" + url.PathEscape(q.Collection) + "/points/query"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if q.APIKey != "" {
		request.Header.Set("api-key", q.APIKey)
	}
	response, err := q.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, errorBodyLimit))
		return nil, fmt.Errorf("answered %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	var answer struct {
		Result struct {
			Points []struct {
				// A point id may be a number or a UUID string in Qdrant.
				// The indexer writes numbers; anything else is not a chunk.
				ID    json.Number `json:"id"`
				Score float64     `json:"score"`
			} `json:"points"`
		} `json:"result"`
	}
	decoder := json.NewDecoder(response.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&answer); err != nil {
		return nil, fmt.Errorf("its answer is not readable: %w", err)
	}
	out := make([]Neighbor, 0, len(answer.Result.Points))
	for _, point := range answer.Result.Points {
		id, err := point.ID.Int64()
		if err != nil {
			return nil, fmt.Errorf("it answered point id %q, which is not a chunk id", point.ID.String())
		}
		out = append(out, Neighbor{ChunkID: id, Score: point.Score})
	}
	return out, nil
}
