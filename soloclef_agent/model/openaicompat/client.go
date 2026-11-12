// Package openaicompat calls a model through an OpenAI-compatible
// chat-completions endpoint, which is what the project's model proxy speaks
// for every model behind it.
//
// It is the only place in the library that knows HTTP. It talks to one
// address for one model name, both given by the caller; it has no built-in
// address, so it cannot reach a provider by accident, and no list of models
// to fall back through.
package openaicompat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// Config says where the model is and how to ask for it. Every field but
// Params must be set.
type Config struct {
	// BaseURL is the endpoint's prefix, up to and not including
	// "/chat/completions": for a proxy on the compose network, something
	// like "http://proxy:4000/v1".
	BaseURL string

	// APIKey is sent as a bearer token.
	APIKey string

	// Model is the model name the endpoint knows.
	Model string

	// HTTPClient makes the request. It is required rather than defaulted
	// because its timeouts are a decision: an answer can stream for minutes,
	// so a total timeout that suits other calls would cut answers short, and
	// only the caller knows what it wants instead (a dial timeout, a
	// response-header timeout, cancellation through the context).
	HTTPClient *http.Client

	// Params are extra fields for the request body, such as "temperature" or
	// "max_tokens". Optional. They may not name a field this package sets
	// itself.
	Params map[string]any

	// Headers are extra request headers, sent on every request. Optional.
	// They exist for an endpoint a person connected themselves, which may
	// want a header of its own beside the bearer key (an organisation id, a
	// gateway's routing header). They may not name a header this package
	// sets itself (ownedHeaders): a second way to set the key or the body's
	// type would be a second, invisible way to configure the same thing.
	Headers map[string]string
}

// ownedHeaders are the request headers this package sets, in canonical
// form. Host and Content-Length are the transport's.
var ownedHeaders = map[string]bool{
	"Authorization": true, "Content-Type": true, "Accept": true, "Host": true, "Content-Length": true,
}

// OwnsHeader reports whether name is a header this package sets itself and
// Config.Headers may therefore not name. It is exported so that a form
// that collects headers can refuse one before it is stored, with the same
// rule New applies.
func OwnsHeader(name string) bool {
	return ownedHeaders[http.CanonicalHeaderKey(strings.TrimSpace(name))]
}

// reserved are the body fields this package owns. A caller that could set
// "stream" to false or swap "model" through Params would have a second,
// invisible way to configure the same thing.
var reserved = []string{"model", "messages", "tools", "stream", "stream_options"}

// Client is a model.Model over one endpoint and one model name.
type Client struct {
	url     string
	key     string
	model   string
	http    *http.Client
	params  map[string]any
	headers map[string]string
}

// New checks cfg and returns the client.
func New(cfg Config) (*Client, error) {
	base, err := url.Parse(cfg.BaseURL)
	switch {
	case cfg.BaseURL == "":
		return nil, errors.New("openaicompat: BaseURL is not set")
	case err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "":
		return nil, fmt.Errorf("openaicompat: BaseURL %q is not an http or https address", cfg.BaseURL)
	case cfg.APIKey == "":
		return nil, errors.New("openaicompat: APIKey is not set")
	case cfg.Model == "":
		return nil, errors.New("openaicompat: Model is not set")
	case cfg.HTTPClient == nil:
		return nil, errors.New("openaicompat: HTTPClient is not set")
	}
	for _, name := range reserved {
		if _, clash := cfg.Params[name]; clash {
			return nil, fmt.Errorf("openaicompat: Params may not set %q; it is set from the request", name)
		}
	}
	for name, value := range cfg.Headers {
		switch {
		case OwnsHeader(name):
			return nil, fmt.Errorf("openaicompat: Headers may not set %q; it is set from the request", name)
		case strings.TrimSpace(name) == "" || strings.ContainsAny(name, " \t\r\n:"):
			return nil, fmt.Errorf("openaicompat: header name %q is not a header name", name)
		case strings.ContainsAny(value, "\r\n"):
			// A line break would end the header and start another.
			return nil, fmt.Errorf("openaicompat: the value of header %q has a line break", name)
		}
	}
	return &Client{
		url:     strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions",
		key:     cfg.APIKey,
		model:   cfg.Model,
		http:    cfg.HTTPClient,
		params:  cfg.Params,
		headers: cfg.Headers,
	}, nil
}

// Generate sends one streaming request and returns the assembled answer.
func (c *Client) Generate(ctx context.Context, req model.Request, stream func(model.Chunk)) (model.Response, error) {
	body, err := encodeRequest(c.model, c.params, req)
	if err != nil {
		return model.Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return model.Response{}, fmt.Errorf("openaicompat: build request: %w", err)
	}
	// The caller's headers first, so that nothing below can be overridden
	// by one (New refuses those names anyway).
	for name, value := range c.headers {
		httpReq.Header.Set(name, value)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+c.key)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		// A cancelled run is not a model failure, and must not be dressed as
		// one: the loop tells them apart by the context error.
		if ctx.Err() != nil {
			return model.Response{}, ctx.Err()
		}
		return model.Response{}, &model.Error{Retryable: true, Message: err.Error(), Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return model.Response{}, failure(resp)
	}
	out, err := readStream(resp.Body, stream)
	if err != nil && ctx.Err() != nil {
		return model.Response{}, ctx.Err()
	}
	return out, err
}
