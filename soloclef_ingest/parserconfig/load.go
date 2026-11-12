// Package parserconfig builds the parser registry from the environment.
//
// The rule, for every parser: it is configured when its key variable is set
// (for Local, the service's address). Once it is, every other setting of
// that parser is required, and a missing one stops the process with the
// variable's name. A parser whose key is not set is not an error: it is
// listed as unavailable, and a source that chooses it is refused.
//
// Nothing is defaulted. Which tier of a metered API is used, which region
// documents are sent to and which parser a picker starts on are decisions,
// and a decision nobody made should be visible at start, not on an invoice.
package parserconfig

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/datalab"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/delimited"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/htmlmarkdown"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/llamaparse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/localservice"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/textfile"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/unstructured"
)

// The variables read. Each parser's first is the one that configures it.
const (
	// DefaultParser is the choice a picker starts on, and the parser for a
	// request that names none (an older client). It must be configured.
	DefaultParser = "SOLOCLEF_PARSER_DEFAULT"
	// MaxTextBytes is the largest text, CSV, TSV or HTML file read in Go.
	MaxTextBytes = "SOLOCLEF_PARSE_MAX_TEXT_BYTES"
	// MaxMarkdownBytes is the largest reply accepted from any parser.
	MaxMarkdownBytes = "SOLOCLEF_PARSE_MAX_MARKDOWN_BYTES"

	LocalURL = "SOLOCLEF_PARSE_SERVICE_URL"
	// LocalTimeoutSeconds must exceed the service's own queue wait plus
	// parse timeout (localservice.Config.Timeout says why).
	LocalTimeoutSeconds = "SOLOCLEF_PARSE_SERVICE_TIMEOUT_SECONDS"

	DatalabAPIKey = "DATALAB_API_KEY"
	DatalabMode   = "DATALAB_MODE"

	LlamaParseAPIKey  = "LLAMA_CLOUD_API_KEY"
	LlamaParseBaseURL = "LLAMA_CLOUD_BASE_URL"
	LlamaParseTier    = "LLAMA_PARSE_TIER"
	LlamaParseVersion = "LLAMA_PARSE_VERSION"

	UnstructuredAPIKey   = "UNSTRUCTURED_API_KEY"
	UnstructuredURL      = "UNSTRUCTURED_API_URL"
	UnstructuredStrategy = "UNSTRUCTURED_STRATEGY"
)

// These are properties of the hosted services and of what a job is worth
// waiting for, not of a deployment, so they are constants and not settings.
const (
	// hostedTimeout is how long a hosted parse may take end to end. A long
	// scanned book takes many minutes; past this the job is abandoned and
	// reported, never resubmitted (it would be billed again).
	hostedTimeout = 30 * time.Minute
	// pollInterval is LlamaParse's own example interval, and Datalab's
	// request limit (200 a minute on a team plan) leaves it ample room.
	pollInterval = 2 * time.Second
	// datalabRateLimitWait is the wait Datalab's rate-limit page uses: its
	// limits are counted per minute.
	datalabRateLimitWait = 60 * time.Second
)

// Load reads the settings through getenv (os.Getenv in a program) and
// returns the registry, or the first setting that is missing or unusable.
func Load(getenv func(string) string) (*selection.Registry, error) {
	env := environment{getenv}

	maxText, err := env.positive(MaxTextBytes)
	if err != nil {
		return nil, err
	}
	maxMarkdown, err := env.positive(MaxMarkdownBytes)
	if err != nil {
		return nil, err
	}

	text, err := textfile.New(maxText)
	if err != nil {
		return nil, err
	}
	tables, err := delimited.New(maxText)
	if err != nil {
		return nil, err
	}
	pages, err := htmlmarkdown.New(maxText)
	if err != nil {
		return nil, err
	}

	local, err := env.local(maxMarkdown)
	if err != nil {
		return nil, err
	}
	datalabParser, err := env.datalab(maxMarkdown)
	if err != nil {
		return nil, err
	}
	llamaParser, err := env.llamaparse(maxMarkdown)
	if err != nil {
		return nil, err
	}
	unstructuredParser, err := env.unstructured(maxMarkdown)
	if err != nil {
		return nil, err
	}

	name, err := env.required(DefaultParser)
	if err != nil {
		return nil, err
	}
	initial, err := selection.Of(name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", DefaultParser, err)
	}
	registry, err := selection.NewRegistry(
		[]parse.Parser{text, tables, pages},
		[]selection.Choice{
			{ID: parse.Local, Extensions: localservice.Extensions(), Parser: local},
			{ID: parse.Datalab, Extensions: datalab.Extensions(), Parser: datalabParser},
			{ID: parse.LlamaParse, Extensions: llamaparse.Extensions(), Parser: llamaParser},
			{ID: parse.Unstructured, Extensions: unstructured.Extensions(), Parser: unstructuredParser},
		},
		initial,
	)
	if err != nil {
		return nil, fmt.Errorf("%s=%s: %w", DefaultParser, name, err)
	}
	return registry, nil
}

type environment struct {
	getenv func(string) string
}

func (e environment) value(name string) string { return strings.TrimSpace(e.getenv(name)) }

func (e environment) required(name string) (string, error) {
	if value := e.value(name); value != "" {
		return value, nil
	}
	return "", fmt.Errorf("%s is not set", name)
}

// requiredWith is required for a setting that only matters because another
// one configured its parser; the error names both.
func (e environment) requiredWith(name, because string) (string, error) {
	if value := e.value(name); value != "" {
		return value, nil
	}
	return "", fmt.Errorf("%s is not set, and %s is, so the parser is half configured", name, because)
}

func (e environment) positive(name string) (int64, error) {
	raw, err := e.required(name)
	if err != nil {
		return 0, err
	}
	number, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || number < 1 {
		return 0, fmt.Errorf("%s=%q is not a whole number greater than zero", name, raw)
	}
	return number, nil
}

// Each of the four returns a nil parse.Parser (and no error) when its parser
// is not configured. The return type is the interface on purpose: a nil
// *Client stored in an interface would not compare equal to nil.

func (e environment) local(maxMarkdown int64) (parse.Parser, error) {
	address := e.value(LocalURL)
	if address == "" {
		return nil, nil
	}
	// Checked first for its message: "half configured" names both settings.
	if _, err := e.requiredWith(LocalTimeoutSeconds, LocalURL); err != nil {
		return nil, err
	}
	seconds, err := e.positive(LocalTimeoutSeconds)
	if err != nil {
		return nil, err
	}
	client, err := localservice.New(localservice.Config{
		BaseURL: address, Timeout: time.Duration(seconds) * time.Second, MaxResponseBytes: maxMarkdown,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", LocalURL, err)
	}
	return client, nil
}

func (e environment) datalab(maxMarkdown int64) (parse.Parser, error) {
	key := e.value(DatalabAPIKey)
	if key == "" {
		return nil, nil
	}
	mode, err := e.requiredWith(DatalabMode, DatalabAPIKey)
	if err != nil {
		return nil, err
	}
	client, err := datalab.New(datalab.Config{
		APIKey: key, Endpoint: datalab.Endpoint, Mode: mode,
		PollInterval: pollInterval, RateLimitWait: datalabRateLimitWait,
		Timeout: hostedTimeout, MaxResponseBytes: maxMarkdown,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", DatalabMode, err)
	}
	return client, nil
}

func (e environment) llamaparse(maxMarkdown int64) (parse.Parser, error) {
	key := e.value(LlamaParseAPIKey)
	if key == "" {
		return nil, nil
	}
	base, err := e.requiredWith(LlamaParseBaseURL, LlamaParseAPIKey)
	if err != nil {
		return nil, err
	}
	tier, err := e.requiredWith(LlamaParseTier, LlamaParseAPIKey)
	if err != nil {
		return nil, err
	}
	version, err := e.requiredWith(LlamaParseVersion, LlamaParseAPIKey)
	if err != nil {
		return nil, err
	}
	client, err := llamaparse.New(llamaparse.Config{
		APIKey: key, BaseURL: base, Tier: tier, Version: version,
		PollInterval: pollInterval, Timeout: hostedTimeout, MaxResponseBytes: maxMarkdown,
	})
	if err != nil {
		return nil, fmt.Errorf("%s, %s: %w", LlamaParseBaseURL, LlamaParseTier, err)
	}
	return client, nil
}

func (e environment) unstructured(maxMarkdown int64) (parse.Parser, error) {
	key := e.value(UnstructuredAPIKey)
	if key == "" {
		return nil, nil
	}
	address, err := e.requiredWith(UnstructuredURL, UnstructuredAPIKey)
	if err != nil {
		return nil, err
	}
	strategy, err := e.requiredWith(UnstructuredStrategy, UnstructuredAPIKey)
	if err != nil {
		return nil, err
	}
	client, err := unstructured.New(unstructured.Config{
		APIKey: key, URL: address, Strategy: strategy,
		Timeout: hostedTimeout, MaxResponseBytes: maxMarkdown,
	})
	if err != nil {
		return nil, fmt.Errorf("%s, %s: %w", UnstructuredURL, UnstructuredStrategy, err)
	}
	return client, nil
}
