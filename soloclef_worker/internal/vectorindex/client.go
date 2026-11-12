// Package vectorindex writes chunk vectors to Qdrant, the only vector store.
//
// # What a point is
//
// One point per chunk. The point's id is the chunk's Postgres id, so there is
// no mapping table and an upsert of the same chunk is idempotent. The payload
// carries only what has to filter a search inside Qdrant:
//
//	user_id        the tenant: the account that owns the chunk, its uuid in
//	               the canonical lower-case form, as a keyword (TenantField)
//	document_type  the type filter
//	document_id    an integer, for narrowing a search to some documents
//
// Chunk text, line ranges and page ranges are never written here. A search
// gets ids back and re-reads the rows from Postgres with the owner applied
// again, so a wrong payload can cost recall and cannot show one account
// another's text.
//
// # Spoken over HTTP
//
// Qdrant's REST API, with the standard library. The official Go client is
// gRPC and brings its dependency tree for the six calls made here.
package vectorindex

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

// The payload's indexed field names beside the tenant's, which is
// TenantField (collection.go).
const (
	PayloadDocumentType = "document_type"
	PayloadDocumentID   = "document_id"
)

// Failure is a Qdrant call that did not succeed.
type Failure struct {
	// Status is Qdrant's HTTP status, or 0 when no answer arrived.
	Status int
	// Retryable is true when Qdrant did not answer or answered 5xx or 429.
	Retryable bool
	Detail    string
	Err       error
}

func (f *Failure) Error() string {
	text := "vectorindex: the vector store"
	if f.Status == 0 {
		text += " did not answer"
	} else {
		text += fmt.Sprintf(" answered %d", f.Status)
	}
	if f.Detail != "" {
		text += ": " + f.Detail
	}
	if f.Err != nil {
		text += ": " + f.Err.Error()
	}
	return text
}

func (f *Failure) Unwrap() error { return f.Err }

// Config is where Qdrant is and what the chunk collection is called.
type Config struct {
	// URL is Qdrant's HTTP address, for example http://qdrant:6333.
	URL string
	// APIKey is sent when Qdrant is run with one, and may be empty: Qdrant
	// inside the stack's own network is run without.
	APIKey string
	// Alias is the name every read and write addresses. A rebuilt collection
	// is swapped in under it, so nothing that searches has to change.
	Alias string
	// Collection is what the alias points at, and what Ensure creates.
	Collection string
	// Dimension is the embedding profile's vector length.
	Dimension int
	HTTP      *http.Client
}

// Client reads and writes the chunk collection.
type Client struct {
	config Config
}

// New refuses a configuration that is missing a part.
func New(config Config) (*Client, error) {
	config.URL = strings.TrimRight(strings.TrimSpace(config.URL), "/")
	parsed, err := url.Parse(config.URL)
	switch {
	case err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https"):
		return nil, fmt.Errorf("vectorindex: the address %q must be http:// or https:// followed by a host", config.URL)
	case config.Alias == "" || config.Collection == "":
		return nil, errors.New("vectorindex: the alias and the collection must both be named")
	case config.Alias == config.Collection:
		// The alias is what makes a rebuild an atomic swap; it cannot be the
		// collection it points at.
		return nil, fmt.Errorf("vectorindex: the alias and the collection are both %q", config.Alias)
	case config.Dimension < 1:
		return nil, fmt.Errorf("vectorindex: the dimension must be positive, got %d", config.Dimension)
	case config.HTTP == nil:
		return nil, errors.New("vectorindex: no HTTP client")
	}
	return &Client{config: config}, nil
}

// Point is one chunk as Qdrant holds it.
type Point struct {
	ChunkID int64
	Vector  []float32
	// UserID is the owner's uuid as Postgres prints it (`user_id::text`:
	// lower case, hyphenated). A search filters on the same text, so the
	// two spellings must be the one Postgres gives.
	UserID       string
	DocumentType string
	DocumentID   int64
}

type pointJSON struct {
	ID      int64        `json:"id"`
	Vector  []float32    `json:"vector"`
	Payload pointPayload `json:"payload"`
}

type pointPayload struct {
	UserID       string `json:"user_id"`
	DocumentType string `json:"document_type"`
	DocumentID   int64  `json:"document_id"`
}

// upsertBatch is how many points go in one request. A 1024-dimension point
// is about 12 KB of JSON, so a request is about 3 MB: large enough that a
// long document is a handful of requests, small enough to stay far under
// Qdrant's 32 MB request limit.
const upsertBatch = 256

// Upsert writes the points and returns once Qdrant has applied them.
//
// It waits (wait=true) on purpose. The caller commits the chunk rows only
// after this returns, and "ready" means the vectors are searchable; an
// unacknowledged write that is then lost would be a chunk that keyword
// search finds and meaning search never does, with nothing saying so.
func (c *Client) Upsert(ctx context.Context, points []Point) error {
	for start := 0; start < len(points); start += upsertBatch {
		batch := points[start:min(start+upsertBatch, len(points))]
		body := struct {
			Points []pointJSON `json:"points"`
		}{Points: make([]pointJSON, len(batch))}
		for index, point := range batch {
			if point.UserID == "" {
				// A point with no owner would be in no account's part of
				// the collection: stored, counted and never found.
				return fmt.Errorf("vectorindex: chunk %d has no owner", point.ChunkID)
			}
			if len(point.Vector) != c.config.Dimension {
				return fmt.Errorf("vectorindex: chunk %d has a vector of %d numbers and the collection holds %d",
					point.ChunkID, len(point.Vector), c.config.Dimension)
			}
			body.Points[index] = pointJSON{
				ID:     point.ChunkID,
				Vector: point.Vector,
				Payload: pointPayload{
					UserID:       point.UserID,
					DocumentType: point.DocumentType,
					DocumentID:   point.DocumentID,
				},
			}
		}
		if err := c.call(ctx, http.MethodPut, c.points()+"?wait=true", body, nil); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes these chunks' points and waits for it. An id with no point
// is not an error, so a delete that is retried succeeds.
func (c *Client) Delete(ctx context.Context, chunkIDs []int64) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	body := struct {
		Points []int64 `json:"points"`
	}{chunkIDs}
	return c.call(ctx, http.MethodPost, c.points()+"/delete?wait=true", body, nil)
}

// Present reports which of these chunks have a point. The indexer asks before
// it keeps an unchanged chunk's row: a row kept without its point would be
// silently unsearchable by meaning, so a missing point is embedded again.
func (c *Client) Present(ctx context.Context, chunkIDs []int64) (map[int64]bool, error) {
	present := make(map[int64]bool, len(chunkIDs))
	if len(chunkIDs) == 0 {
		return present, nil
	}
	body := struct {
		IDs         []int64 `json:"ids"`
		WithPayload bool    `json:"with_payload"`
		WithVector  bool    `json:"with_vector"`
	}{IDs: chunkIDs}
	var found []struct {
		ID int64 `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, c.points(), body, &found); err != nil {
		return nil, err
	}
	for _, point := range found {
		present[point.ID] = true
	}
	return present, nil
}

func (c *Client) points() string {
	return "/collections/" + url.PathEscape(c.config.Alias) + "/points"
}

// detailLimit bounds how much of an error body is kept for the log.
const detailLimit = 2 << 10

// call makes one request and decodes the `result` of Qdrant's envelope into
// `into` when it is given.
func (c *Client) call(ctx context.Context, method, path string, body, into any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("vectorindex: encoding a request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.config.URL+path, payload)
	if err != nil {
		return fmt.Errorf("vectorindex: building a request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.config.APIKey != "" {
		request.Header.Set("api-key", c.config.APIKey)
	}
	response, err := c.config.HTTP.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Failure{Retryable: true, Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, detailLimit))
		return &Failure{
			Status:    response.StatusCode,
			Retryable: response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests,
			Detail:    strings.TrimSpace(string(detail)),
		}
	}
	if into == nil {
		// Read to the end so the connection goes back to the pool.
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	envelope := struct {
		Result any `json:"result"`
	}{Result: into}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Failure{Status: response.StatusCode, Retryable: true, Detail: "the answer could not be read", Err: err}
	}
	return nil
}
