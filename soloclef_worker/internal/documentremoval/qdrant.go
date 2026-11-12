package documentremoval

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

// Qdrant is Vectors over Qdrant's HTTP API: the one call a removal makes.
//
// The collection is the one the indexer writes, addressed by the alias
// searches use (internal/vectorindex): a point per chunk, whose id is the
// chunk's row id.
type Qdrant struct {
	// URL is Qdrant's HTTP address, for example http://qdrant:6333.
	URL string
	// APIKey is sent when set; Qdrant on the stack's own network runs
	// without one.
	APIKey string
	// Alias is the collection's name as searches address it.
	Alias string
	HTTP  *http.Client
}

// Check reports what is missing. Called once at start-up.
func (q Qdrant) Check() error {
	parsed, err := url.Parse(q.URL)
	switch {
	case err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https"):
		return fmt.Errorf("documentremoval: the Qdrant address %q must be http:// or https:// followed by a host", q.URL)
	case q.Alias == "":
		return errors.New("documentremoval: the Qdrant alias is not named")
	case q.HTTP == nil:
		return errors.New("documentremoval: no HTTP client")
	}
	return nil
}

// Delete removes the points and waits until Qdrant has applied it.
func (q Qdrant) Delete(ctx context.Context, chunkIDs []int64) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	body, err := json.Marshal(map[string][]int64{"points": chunkIDs})
	if err != nil {
		return err
	}
	address := strings.TrimRight(q.URL, "/") + "/collections/" + url.PathEscape(q.Alias) + "/points/delete?wait=true"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if q.APIKey != "" {
		request.Header.Set("api-key", q.APIKey)
	}
	response, err := q.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("qdrant: %w", err)
	}
	defer response.Body.Close()
	detail, _ := io.ReadAll(io.LimitReader(response.Body, 2<<10))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("qdrant: deleting %d points answered %d: %s", len(chunkIDs), response.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}
