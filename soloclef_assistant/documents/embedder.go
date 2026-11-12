package documents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ProxyEmbedder embeds a query through the model proxy's OpenAI-compatible
// /embeddings route. The proxy is the only way this tier reaches a model: it
// holds the provider's credentials, and which provider serves the name is
// its business.
type ProxyEmbedder struct {
	// BaseURL is the proxy's OpenAI-compatible root, ending in /v1.
	BaseURL string
	// Key is the proxy key.
	Key string
	// Model is the proxy's name for the embedding model. It must be the
	// model the chunks were indexed with: a query embedded by another model
	// is a point in a different space, and the search would return noise
	// with no error.
	Model string
	// Dimension is the length the index's vectors have. An answer of any
	// other length is refused, which is the one check that catches a proxy
	// alias repointed at a different model.
	Dimension int
	HTTP      *http.Client
}

// Check reports what is missing. Called once at start-up.
func (e ProxyEmbedder) Check() error {
	switch {
	case strings.TrimSpace(e.BaseURL) == "":
		return errors.New("documents: the embedder's proxy address is not set")
	case e.Key == "":
		return errors.New("documents: the embedder's proxy key is not set")
	case e.Model == "":
		return errors.New("documents: the embedding model is not set")
	case e.Dimension < 1:
		return fmt.Errorf("documents: the embedding dimension must be positive, got %d", e.Dimension)
	case e.HTTP == nil:
		return errors.New("documents: the embedder's HTTP client is not set")
	}
	return nil
}

// errorBodyLimit bounds how much of an upstream's error body is read into
// an error message.
const errorBodyLimit = 2 << 10

func (e ProxyEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{e.Model, []string{text}})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(e.BaseURL, "/")+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+e.Key)
	response, err := e.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, errorBodyLimit))
		return nil, fmt.Errorf("the embedding service answered %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	var answer struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		return nil, fmt.Errorf("the embedding service's answer is not readable: %w", err)
	}
	if len(answer.Data) != 1 {
		return nil, fmt.Errorf("the embedding service answered %d vectors for one text", len(answer.Data))
	}
	if got := len(answer.Data[0].Embedding); got != e.Dimension {
		return nil, fmt.Errorf("the embedding service answered a vector of %d numbers and the index holds %d: the query model is not the one the documents were indexed with", got, e.Dimension)
	}
	return answer.Data[0].Embedding, nil
}
