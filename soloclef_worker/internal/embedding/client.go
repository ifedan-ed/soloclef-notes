package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Failure is an embedding request that did not produce vectors, with the one
// thing the caller has to decide on: whether asking again later could work.
type Failure struct {
	// Status is the proxy's HTTP status, or 0 when no answer arrived.
	Status int
	// Retryable is true for a proxy that did not answer, answered 5xx, or
	// said to slow down. A refused key, an unknown model or a text the model
	// will not take are not: asking again gets the same answer.
	Retryable bool
	// Detail is the proxy's own words, trimmed, for the log.
	Detail string
	Err    error
}

func (f *Failure) Error() string {
	text := "embedding: the embedding service"
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

// Config is where the proxy is. It says nothing about which model is used:
// that is the profile's.
type Config struct {
	// BaseURL is the proxy's OpenAI-compatible root, ending in /v1.
	BaseURL string
	// Key is the proxy key, already read out of its file.
	Key string
	// HTTP is the client requests are sent with. Its timeout is the caller's
	// choice, stated where the process is assembled.
	HTTP *http.Client
}

// Client embeds texts with one profile.
type Client struct {
	profile Profile
	config  Config
}

// New refuses a configuration that is missing a part.
func New(profile Profile, config Config) (*Client, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	switch {
	case strings.TrimSpace(config.BaseURL) == "":
		return nil, errors.New("embedding: the proxy's address is not set")
	case config.Key == "":
		return nil, errors.New("embedding: the proxy's key is not set")
	case config.HTTP == nil:
		return nil, errors.New("embedding: no HTTP client")
	}
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	return &Client{profile: profile, config: config}, nil
}

// Profile is the profile this client embeds with.
func (c *Client) Profile() Profile { return c.profile }

// Probe embeds one text, so a proxy that is unreachable, refuses the key,
// does not know the model, or serves a model of another dimension under this
// name stops the process at start-up with the proxy's own message, instead
// of failing the first document somebody uploads.
func (c *Client) Probe(ctx context.Context) error {
	_, err := c.EmbedDocuments(ctx, []string{"soloclef"})
	return err
}

// EmbedDocuments returns one vector per text, in order.
//
// The texts go out BatchSize at a time, Concurrency requests at once. The
// first failure cancels the requests still in flight and is the error
// returned: a document is embedded whole or not at all, so there is nothing
// to do with the vectors of the batches that did succeed.
func (c *Client) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	if len(texts) == 0 {
		return vectors, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		group   sync.WaitGroup
		once    sync.Once
		failure error
		slots   = make(chan struct{}, c.profile.Concurrency)
	)
	for start := 0; start < len(texts); start += c.profile.BatchSize {
		end := min(start+c.profile.BatchSize, len(texts))
		// Taking the slot here, not inside the goroutine, means a cancelled
		// run stops starting batches instead of queueing all of them.
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		group.Add(1)
		go func() {
			defer group.Done()
			defer func() { <-slots }()
			// Each batch writes its own stretch of vectors, so the batches
			// share the slice without a lock.
			if err := c.embedInto(ctx, texts[start:end], vectors[start:end]); err != nil {
				once.Do(func() { failure = err; cancel() })
			}
		}()
	}
	group.Wait()
	if failure != nil {
		return nil, failure
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return vectors, nil
}

// embedInto embeds one batch, halving it when the host refuses it.
//
// A 400 on a batch is retried as two halves against the same host, down to
// single texts. That is not a fallback: it is the same model at the same
// address. It exists because the hosted model was measured refusing a batch
// of 32 only while sibling batches were in flight (its token budget is
// shared across the account), so "this batch is too big" depends on load and
// cannot be settled by a smaller BatchSize. A single text that is still
// refused is a text the model will not take, and fails the document.
func (c *Client) embedInto(ctx context.Context, texts []string, into [][]float32) error {
	vectors, err := c.request(ctx, texts)
	if err == nil {
		copy(into, vectors)
		return nil
	}
	var failure *Failure
	if len(texts) == 1 || !errors.As(err, &failure) || failure.Status != http.StatusBadRequest {
		return err
	}
	half := len(texts) / 2
	if err := c.embedInto(ctx, texts[:half], into[:half]); err != nil {
		return err
	}
	return c.embedInto(ctx, texts[half:], into[half:])
}

// detailLimit bounds how much of an error body is kept for the log.
const detailLimit = 2 << 10

// request is one call to the proxy.
func (c *Client) request(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
		// Asked for by name: some hosts answer base64 unless told, and a
		// base64 string would not decode into numbers below.
		EncodingFormat string `json:"encoding_format"`
	}{c.profile.Model, texts, "float"})
	if err != nil {
		return nil, fmt.Errorf("embedding: encoding a request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embedding: building a request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.config.Key)

	response, err := c.config.HTTP.Do(request)
	if err != nil {
		// The job being cancelled is not the service failing.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Failure{Retryable: true, Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, detailLimit))
		return nil, &Failure{
			Status: response.StatusCode,
			// 408 and 429 are the host asking for another try; 5xx is the
			// host or the proxy being down. Everything else in 4xx is about
			// this request and will be the same next time.
			Retryable: response.StatusCode >= 500 ||
				response.StatusCode == http.StatusTooManyRequests ||
				response.StatusCode == http.StatusRequestTimeout,
			Detail: strings.TrimSpace(string(detail)),
		}
	}

	var answer struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A body cut off mid-way is a transport fault; try again.
		return nil, &Failure{Status: response.StatusCode, Retryable: true, Detail: "the answer could not be read", Err: err}
	}
	if len(answer.Data) != len(texts) {
		return nil, &Failure{Status: response.StatusCode,
			Detail: fmt.Sprintf("%d vectors came back for %d texts", len(answer.Data), len(texts))}
	}
	// The OpenAI shape numbers each vector; they are placed by that number
	// and not by arrival order, which the shape does not promise.
	vectors := make([][]float32, len(texts))
	for _, item := range answer.Data {
		if item.Index < 0 || item.Index >= len(texts) || vectors[item.Index] != nil {
			return nil, &Failure{Status: response.StatusCode,
				Detail: fmt.Sprintf("the answer numbers a vector %d, which is not one of the %d texts or is repeated", item.Index, len(texts))}
		}
		if len(item.Embedding) != c.profile.Dimension {
			// The proxy's name now points at another model. Not retryable,
			// and never written: vectors of two models in one collection are
			// a quietly useless index.
			return nil, &Failure{Status: response.StatusCode,
				Detail: fmt.Sprintf("a vector of %d numbers came back and profile %s is %d: the proxy's %q is not the model this profile describes",
					len(item.Embedding), c.profile.Name, c.profile.Dimension, c.profile.Model)}
		}
		vectors[item.Index] = item.Embedding
	}
	return vectors, nil
}
