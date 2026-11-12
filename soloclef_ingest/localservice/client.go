// Package localservice is the Local parser: an HTTP client for
// soloclef_parse_service, which runs pymupdf4llm on this server.
//
// The service is called, not spawned: it has its own memory limit and its
// own restart, and a PDF that crashes MuPDF costs one request there and
// nothing here.
package localservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/hostedapi"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// Config is everything the client needs. Every field is required.
type Config struct {
	// BaseURL is the service's address, e.g. http://parse-service:8080.
	BaseURL string
	// Timeout bounds one parse end to end: the upload, the wait for a free
	// worker and the parse. Set it above the service's own
	// PARSE_SERVICE_QUEUE_WAIT_SECONDS + PARSE_SERVICE_PARSE_TIMEOUT_SECONDS,
	// so the service's typed answer arrives before this gives up.
	Timeout time.Duration
	// MaxResponseBytes bounds the Markdown accepted back.
	MaxResponseBytes int64
}

// Client is the Local parser.
type Client struct {
	parseURL  string
	healthURL string
	config    Config
	http      *http.Client
}

// extensions are the formats the service reads (its SUPPORTED_EXTENSIONS).
// Kept here as well so a file is refused before it is uploaded, and so the
// upload dialog can be told without asking the service.
var extensions = map[string]bool{".pdf": true, ".docx": true, ".pptx": true, ".epub": true}

// Extensions lists what the Local parser reads.
func Extensions() []string { return []string{".docx", ".epub", ".pdf", ".pptx"} }

// New checks the configuration and returns a Client.
func New(config Config) (*Client, error) {
	base, err := url.Parse(config.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, fmt.Errorf("localservice: BaseURL %q is not an http(s) address", config.BaseURL)
	}
	if config.Timeout <= 0 {
		return nil, errors.New("localservice: Timeout is required")
	}
	if config.MaxResponseBytes <= 0 {
		return nil, errors.New("localservice: MaxResponseBytes is required")
	}
	return &Client{
		parseURL:  base.JoinPath("v1", "parse").String(),
		healthURL: base.JoinPath("health").String(),
		config:    config,
		// No client-wide timeout: each call carries its own deadline in its
		// context, so a caller's cancellation and the configured limit are
		// the same mechanism.
		http: &http.Client{},
	}, nil
}

func (*Client) ID() parse.ID { return parse.Local }

func (*Client) Reads(extension string) bool { return extensions[extension] }

// Parse streams the file to the service and reads its pages back.
//
// It does not retry. A refusal is about the file. A 503 or a refused
// connection is typed Unavailable and Retryable, and the job queue redelivers
// the job; a second loop here would only hide how often that happens.
func (c *Client) Parse(ctx context.Context, source parse.Source) (parse.Document, error) {
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()

	file, err := source.Open(ctx)
	if err != nil {
		return parse.Document{}, &parse.Error{Kind: parse.Unavailable, Parser: parse.Local, Message: "the file could not be opened", Err: err}
	}
	// The raw file is the body: net/http copies it from storage to the
	// socket, and the service copies it from the socket to disk.
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.parseURL+"?filename="+url.QueryEscape(source.Name), file)
	if err != nil {
		file.Close()
		return parse.Document{}, err
	}
	// The service checks the size before reading and refuses a body without
	// one, so the length storage reports is sent.
	request.ContentLength = source.Size
	request.Header.Set("Content-Type", "application/octet-stream")

	response, err := c.http.Do(request)
	if err != nil {
		return parse.Document{}, hostedapi.TransportError(ctx, parse.Local, "the parse", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return parse.Document{}, refusal(response)
	}
	document, err := readPages(response.Body, c.config.MaxResponseBytes)
	if err != nil && ctx.Err() != nil {
		return parse.Document{}, hostedapi.TransportError(ctx, parse.Local, "the parse", err)
	}
	return document, err
}

// Health asks the service whether every worker is alive.
func (c *Client) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.healthURL, nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return hostedapi.TransportError(ctx, parse.Local, "the health check", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return hostedapi.StatusError(parse.Local, "the health check", response)
	}
	return nil
}

// kinds maps the service's error kinds (parse_service/refusal.py and
// http_api.py) to this module's. A kind missing here falls to its status.
var kinds = map[string]parse.Kind{
	"no_text_layer":      parse.NoText,
	"unsupported_format": parse.UnsupportedFormat,
	"parse_error":        parse.ParseFailed,
	"too_large":          parse.TooLarge,
	"busy":               parse.Unavailable,
}

func refusal(response *http.Response) error {
	var reply struct {
		Error struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		} `json:"error"`
	}
	// Decoded leniently: when the body is not the service's (a proxy's 502
	// page), the status alone types the error.
	_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&reply)
	if kind, known := kinds[reply.Error.Kind]; known {
		return &parse.Error{Kind: kind, Parser: parse.Local, Message: reply.Error.Message, Status: response.StatusCode}
	}
	failure := hostedapi.StatusError(parse.Local, "the parse", response)
	if reply.Error.Message != "" {
		failure.Message = fmt.Sprintf("the parse was answered %d: %s", response.StatusCode, reply.Error.Message)
	}
	return failure
}

// readPages reads {"pages": [{"number", "markdown"}]} from the stream, one
// page at a time, straight into the document. Decoding the reply into a
// struct first would hold every page twice for a moment; a 1,000-page PDF's
// Markdown is the largest thing this tier ever holds.
func readPages(body io.Reader, limit int64) (parse.Document, error) {
	limited := &io.LimitedReader{R: body, N: limit + 1}
	decoder := json.NewDecoder(limited)
	bad := func(err error) (parse.Document, error) {
		if limited.N <= 0 {
			return parse.Document{}, &parse.Error{
				Kind: parse.BadResponse, Parser: parse.Local,
				Message: fmt.Sprintf("the Markdown is larger than the %d bytes allowed back", limit),
			}
		}
		return parse.Document{}, &parse.Error{Kind: parse.BadResponse, Parser: parse.Local, Message: "the reply is not the JSON expected", Err: err}
	}

	if err := expect(decoder, json.Delim('{')); err != nil {
		return bad(err)
	}
	// The service's pages concatenate to the document with nothing between.
	var builder parse.Builder
	sawPages := false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return bad(err)
		}
		if key != "pages" {
			// parser, parser_version, and whatever is added later.
			var ignored json.RawMessage
			if err := decoder.Decode(&ignored); err != nil {
				return bad(err)
			}
			continue
		}
		sawPages = true
		if err := expect(decoder, json.Delim('[')); err != nil {
			return bad(err)
		}
		for decoder.More() {
			var page struct {
				Number   int    `json:"number"`
				Markdown string `json:"markdown"`
			}
			if err := decoder.Decode(&page); err != nil {
				return bad(err)
			}
			builder.Page(page.Number, page.Markdown)
		}
		if err := expect(decoder, json.Delim(']')); err != nil {
			return bad(err)
		}
	}
	if !sawPages {
		return bad(errors.New(`no "pages"`))
	}
	return builder.Document(parse.Local)
}

func expect(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != want {
		return fmt.Errorf("expected %q, found %v", strings.TrimSpace(want.String()), token)
	}
	return nil
}
