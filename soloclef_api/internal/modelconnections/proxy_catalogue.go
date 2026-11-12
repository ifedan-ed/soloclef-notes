package modelconnections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Discovery: which models a connection can be given.
//
// The Python tier asked each provider itself (GET /models at OpenAI, at
// Anthropic, at whatever address a person typed), from inside the API
// process and with the person's key. This tier reaches models only through
// the model proxy, so it asks the proxy, and the proxy has exactly one list
// that is about a provider and not about the proxy's own deployments: its
// catalogue (GET /public/litellm_model_cost_map), every model LiteLLM knows
// of each provider, with what each is for and what it accepts.
//
// What that changes, and it is said here because the route's answers
// depend on it:
//
//   - The list is the provider's models, not "the models this key may
//     call". No request the proxy takes lists a provider's models with a
//     caller's credentials. Whether a key reaches a model is what the test
//     says (proxy_probe.go), and connecting already runs one.
//   - A key is not needed to list, so it is never sent, by the preview or
//     by the refresh.
//   - A provider at an address of the person's own (a local server, an
//     OpenAI-compatible gateway, an Azure resource with deployments it
//     named itself) is in no catalogue. For those there is nothing to
//     discover through the proxy, the routes say so (ErrNoCatalogue), and
//     the model is added by its name.

// catalogueSource is the `discovery` word of a provider whose models the
// proxy's catalogue lists; noCatalogue is every other provider's. The
// settings screen reads the word to decide whether to offer a refresh.
const (
	catalogueSource = "proxy_catalogue"
	noCatalogue     = "none"
)

// catalogueProviders is, for each provider the catalogue covers, the
// `litellm_provider` its entries carry. Amazon Bedrock's are filed under
// two names, one per wire protocol. Vertex AI's are filed by model family
// ("vertex_ai-language-models", "vertex_ai-anthropic_models", ...), which
// providerOf reads as a prefix.
var catalogueProviders = map[string][]string{
	"openai":    {"openai"},
	"anthropic": {"anthropic"},
	"bedrock":   {"bedrock", "bedrock_converse"},
	"vertex_ai": {"vertex_ai"},
}

// vertexFamilies is the prefix of Vertex AI's per-family names.
const vertexFamilies = "vertex_ai-"

// HasCatalogue reports whether the proxy's catalogue lists the provider's
// models.
func HasCatalogue(provider string) bool {
	_, covered := catalogueProviders[provider]
	return covered
}

// ErrNoCatalogue is discovery asked for a provider the catalogue does not
// cover; ErrCatalogueUnreadable is a proxy that did not answer with one.
var (
	ErrNoCatalogue         = errors.New("modelconnections: the model proxy has no catalogue for this provider")
	ErrCatalogueUnreadable = errors.New("modelconnections: the model proxy's catalogue could not be read")
)

// Catalogue lists a provider's models. ProxyCatalogue is the one a server
// runs with.
type Catalogue interface {
	// Models is the provider's models, by model id. ErrNoCatalogue for a
	// provider it does not cover.
	Models(ctx context.Context, provider string) ([]pickedModel, error)
}

// ProxyCatalogue reads the model proxy's catalogue and remembers it.
//
// The catalogue is a few megabytes of JSON that changes when the proxy is
// upgraded, and the settings screen asks for it on every refresh click, so
// it is read once and kept for Fresh. Use it through a pointer: the
// remembered copy is in the struct.
type ProxyCatalogue struct {
	// URL is the proxy's OpenAI-compatible root, ending in /v1, as the
	// prober's is, and Key the deployment's key for it.
	URL string
	Key string
	// HTTP makes the request; Timeout bounds it.
	HTTP    *http.Client
	Timeout time.Duration
	// Fresh is how long a catalogue that was read is answered from memory.
	Fresh time.Duration
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time

	mu     sync.Mutex
	readAt time.Time
	// byProvider is nil until the first read.
	byProvider map[string][]pickedModel
}

// Check reports what is missing. Called once at start-up.
func (c *ProxyCatalogue) Check() error {
	switch {
	case c == nil:
		return errors.New("modelconnections: the model catalogue is not set")
	case strings.TrimSpace(c.URL) == "":
		return errors.New("modelconnections: the model proxy's URL is not set")
	case c.Key == "":
		return errors.New("modelconnections: the model proxy's key is not set")
	case c.HTTP == nil:
		return errors.New("modelconnections: the HTTP client is not set")
	case c.Timeout <= 0:
		return errors.New("modelconnections: the catalogue timeout must be positive")
	case c.Fresh <= 0:
		return errors.New("modelconnections: how long the catalogue is kept must be positive")
	}
	return nil
}

// maxCatalogueBytes bounds the read. The catalogue is about three
// megabytes today; this is room to grow, not a size anyone expects.
const maxCatalogueBytes = 32 << 20

// Models answers from the remembered catalogue, reading it first when
// there is none or it is older than Fresh. A read that fails leaves an
// older copy in place but is still an error: the person asked for the
// list as it is now, and is told it could not be had.
func (c *ProxyCatalogue) Models(ctx context.Context, provider string) ([]pickedModel, error) {
	if !HasCatalogue(provider) {
		return nil, ErrNoCatalogue
	}
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	// Held across the read on purpose: two refresh clicks at once make one
	// request to the proxy, and the second is answered from the first's.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byProvider == nil || now.Sub(c.readAt) >= c.Fresh {
		read, err := c.read(ctx)
		if err != nil {
			return nil, err
		}
		c.byProvider, c.readAt = read, now
	}
	return c.byProvider[provider], nil
}

// catalogueEntry is the part of one model's entry that is read. The rest
// is prices.
type catalogueEntry struct {
	Provider        string   `json:"litellm_provider"`
	Mode            string   `json:"mode"`
	MaxInputTokens  *float64 `json:"max_input_tokens"`
	MaxOutputTokens *float64 `json:"max_output_tokens"`
	Vision          *bool    `json:"supports_vision"`
	Tools           *bool    `json:"supports_function_calling"`
}

func (c *ProxyCatalogue) read(ctx context.Context) (map[string][]pickedModel, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	// The catalogue is served at the proxy's root, beside the OpenAI
	// routes' /v1 and not under it. `catalog_only` leaves out the entries
	// the proxy adds for its own deployments, which are not a provider's.
	root := strings.TrimSuffix(strings.TrimRight(c.URL, "/"), "/v1")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/public/litellm_model_cost_map?catalog_only=true", nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogueUnreadable, err)
	}
	request.Header.Set("Authorization", "Bearer "+c.Key)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogueUnreadable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: the proxy answered %d", ErrCatalogueUnreadable, response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogueBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogueUnreadable, err)
	}
	if len(raw) > maxCatalogueBytes {
		return nil, fmt.Errorf("%w: it is larger than %d bytes", ErrCatalogueUnreadable, maxCatalogueBytes)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("%w: it is not a JSON object: %v", ErrCatalogueUnreadable, err)
	}
	return readCatalogue(entries), nil
}

// readCatalogue files the catalogue's entries under the providers a
// connection can be made to, each list ordered by model id.
func readCatalogue(entries map[string]json.RawMessage) map[string][]pickedModel {
	listed := make(map[string][]pickedModel, len(catalogueProviders))
	for name, raw := range entries {
		var entry catalogueEntry
		// One entry that is not shaped like the others (the catalogue
		// carries a sample of its own format) is not a model.
		if json.Unmarshal(raw, &entry) != nil {
			continue
		}
		provider := providerOf(entry.Provider)
		if provider == "" {
			continue
		}
		// An entry is named by the model's id, sometimes behind the
		// provider's prefix ("vertex_ai/claude-…"). Any other name with a
		// slash is not a model but a price of one ("high/1024-x-1024/…").
		id := strings.TrimPrefix(name, proxyPrefix(provider)+"/")
		if id == "" || strings.Contains(id, "/") || len(id) > maxModelIDChars {
			continue
		}
		model := pickedModel{ModelID: id, Source: "DISCOVERED", SupportsImageInput: entry.Vision, SupportsTools: entry.Tools}
		chat, image := false, false
		switch entry.Mode {
		case "chat", "responses":
			chat = true
		case "image_generation":
			image = true
		default:
			// Speech, transcription, embedding, reranking and the rest: a
			// connection's models are for conversation and for pictures.
			continue
		}
		model.SupportsChat, model.SupportsImageGeneration = &chat, &image
		facts := map[string]any{"source": catalogueSource, "litellm_provider": entry.Provider, "mode": entry.Mode}
		if limit, ok := tokens(entry.MaxInputTokens); ok {
			model.MaxInputTokens = &limit
			// The settings screen shows this beside the limit a person
			// may set, as what the model itself accepts.
			facts["max_context_length"] = limit
		}
		if limit, ok := tokens(entry.MaxOutputTokens); ok {
			facts["max_output_tokens"] = limit
		}
		model.Catalog, _ = json.Marshal(facts)
		listed[provider] = append(listed[provider], model)
	}
	for provider, models := range listed {
		sort.Slice(models, func(i, j int) bool { return models[i].ModelID < models[j].ModelID })
		// Two entries can name one model once a prefix is taken off; the
		// column is unique per connection, so one is kept.
		kept := models[:0]
		for index, model := range models {
			if index == 0 || model.ModelID != models[index-1].ModelID {
				kept = append(kept, model)
			}
		}
		listed[provider] = kept
	}
	return listed
}

// providerOf is the connection provider a catalogue entry belongs to, or
// "" for one no connection can be made to.
func providerOf(catalogued string) string {
	if strings.HasPrefix(catalogued, vertexFamilies) {
		return "vertex_ai"
	}
	for provider, names := range catalogueProviders {
		for _, name := range names {
			if catalogued == name {
				return provider
			}
		}
	}
	return ""
}

// tokens is a stated token count as the column holds one: a positive whole
// number that fits an integer. Anything else is not stated.
func tokens(stated *float64) (int32, bool) {
	if stated == nil || *stated < 1 || *stated > math.MaxInt32 || *stated != math.Trunc(*stated) {
		return 0, false
	}
	return int32(*stated), true
}
