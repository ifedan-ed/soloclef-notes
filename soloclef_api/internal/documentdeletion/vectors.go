package documentdeletion

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

// Vectors is the vector store as a removal uses it.
type Vectors interface {
	// Delete removes these chunks' points and returns once the store has
	// applied it. An id with no point is not an error.
	Delete(ctx context.Context, chunkIDs []int64) error
}

// Qdrant is Vectors over Qdrant's HTTP API.
//
// The collection is the one the indexer writes: a point per chunk, whose id
// is the chunk's Postgres id (soloclef_worker/internal/vectorindex). So the
// points of a document are found from its chunk rows, which is why the
// points are removed before the rows are.
type Qdrant struct {
	// URL is the server's HTTP address, e.g. http://qdrant:6333.
	URL string
	// APIKey is sent when set. A server without one takes none.
	APIKey string
	// Collection is the collection or alias the indexer writes to.
	Collection string
	HTTP       *http.Client
}

// Check reports what is missing. Called once at start-up.
func (q Qdrant) Check() error {
	switch {
	case strings.TrimSpace(q.URL) == "":
		return errors.New("documentdeletion: the vector store's address is not set")
	case q.Collection == "":
		return errors.New("documentdeletion: the vector store's collection is not set")
	case q.HTTP == nil:
		return errors.New("documentdeletion: the vector store's HTTP client is not set")
	}
	return nil
}

// detailLimit bounds how much of an error body is kept for the log.
const detailLimit = 2 << 10

// Delete removes the points and waits for it (wait=true): the rows are
// deleted next, and "the points are gone" must be true by then, not merely
// promised.
func (q Qdrant) Delete(ctx context.Context, chunkIDs []int64) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	body, err := json.Marshal(struct {
		Points []int64 `json:"points"`
	}{chunkIDs})
	if err != nil {
		return fmt.Errorf("documentdeletion: encoding the points to remove: %w", err)
	}
	address := strings.TrimRight(q.URL, "/") + "/collections/" + url.PathEscape(q.Collection) + "/points/delete?wait=true"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("documentdeletion: building the request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if q.APIKey != "" {
		request.Header.Set("api-key", q.APIKey)
	}
	response, err := q.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("documentdeletion: the vector store did not answer: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, detailLimit))
		return fmt.Errorf("documentdeletion: the vector store answered %d removing %d points: %s",
			response.StatusCode, len(chunkIDs), strings.TrimSpace(string(detail)))
	}
	// Drained so the connection can be reused for the next batch.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, detailLimit))
	return nil
}
