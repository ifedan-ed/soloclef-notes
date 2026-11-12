// Package unstructured is the Unstructured parser: a client for
// Unstructured's hosted Partition Endpoint (docs.unstructured.io, "Partition
// Endpoint": POST requests, API parameters, document elements; read
// 2026-11-11).
//
// The protocol is one request: POST the file, and the reply is a JSON array
// of elements (a title, a paragraph, a list item, a table), each with the
// page it came from. This package renders them as Markdown.
//
// Unstructured's documentation now labels this endpoint "legacy" and points
// to its workflow jobs for production. It is used here because it is the one
// documented call that takes a file and returns its elements; the jobs API
// is a pipeline of sources and destinations, which is the indexing this
// tier does itself.
//
// # Chunking is not asked for
//
// The endpoint can chunk (chunking_strategy=by_title and others). It is left
// off, and soloclef_chunker cuts this parser's Markdown like every other
// parser's, for four reasons:
//
//   - Size. Unstructured counts characters. A stored chunk is sized in the
//     embedding model's tokens (the profile's tokenizer), so its chunks
//     would be the wrong size for whatever model is configured.
//   - Tables. tablesafe keeps a table's rows with their header, caption and
//     notes. Unstructured splits a long table into TableChunk pieces with
//     no header on the later ones.
//   - Reindexing. A new embedding profile re-chunks from the Markdown kept
//     in Postgres. If the chunks came from the API, a reindex would mean
//     sending every document back and paying for it again.
//   - One set of boundaries. Line and page ranges, and the reconciler that
//     keeps unchanged chunks on an edit, are arithmetic on the spans one
//     chunker reports. Two chunkers is two sets of rules for that.
package unstructured

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/hostedapi"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/multipartfile"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// Config is everything the client needs. Every field is required.
type Config struct {
	APIKey string
	// URL is the partition endpoint itself. Unstructured says to "always use
	// the API URL that was provided to you when your account was created",
	// so there is no default (its public one is
	// https://api.unstructuredapp.io/general/v0/general).
	URL string
	// Strategy is how PDFs and images are read: auto, fast, hi_res, ocr_only
	// or vlm. It decides quality and price, so it is chosen, not defaulted.
	Strategy string
	// Timeout bounds the request. The call is synchronous: the reply comes
	// when the whole file is partitioned, which can take minutes.
	Timeout time.Duration
	// MaxResponseBytes bounds the reply.
	MaxResponseBytes int64
}

var strategies = map[string]bool{"auto": true, "fast": true, "hi_res": true, "ocr_only": true, "vlm": true}

// extensions are the types Unstructured's "Supported file types" page lists,
// less the ones read in Go (.csv, .tsv, .htm, .html, .md, .org, .rst, .txt,
// .xml), which never reach a document parser.
var extensions = map[string]bool{
	".abw": true, ".bmp": true, ".cwk": true, ".dbf": true, ".dif": true, ".doc": true,
	".docx": true, ".dot": true, ".dotm": true, ".dotx": true, ".eml": true, ".epub": true,
	".et": true, ".eth": true, ".fods": true, ".heic": true, ".hwp": true, ".jpeg": true,
	".jpg": true, ".mcw": true, ".msg": true, ".mw": true, ".odt": true, ".p7s": true,
	".pbd": true, ".pdf": true, ".png": true, ".pot": true, ".ppt": true, ".pptm": true,
	".pptx": true, ".prn": true, ".rtf": true, ".sdp": true, ".sxg": true, ".tif": true,
	".tiff": true, ".xls": true, ".xlsx": true, ".zabw": true,
}

// Extensions lists what Unstructured reads, sorted.
func Extensions() []string {
	return []string{
		".abw", ".bmp", ".cwk", ".dbf", ".dif", ".doc", ".docx", ".dot", ".dotm", ".dotx",
		".eml", ".epub", ".et", ".eth", ".fods", ".heic", ".hwp", ".jpeg", ".jpg", ".mcw",
		".msg", ".mw", ".odt", ".p7s", ".pbd", ".pdf", ".png", ".pot", ".ppt", ".pptm",
		".pptx", ".prn", ".rtf", ".sdp", ".sxg", ".tif", ".tiff", ".xls", ".xlsx", ".zabw",
	}
}

// Client is the Unstructured parser.
type Client struct {
	config Config
	http   *http.Client
}

// New checks the configuration and returns a Client.
func New(config Config) (*Client, error) {
	endpoint, err := url.Parse(config.URL)
	switch {
	case config.APIKey == "":
		return nil, errors.New("unstructured: APIKey is required")
	case err != nil || endpoint.Host == "":
		return nil, fmt.Errorf("unstructured: URL %q is not a URL", config.URL)
	case !strategies[config.Strategy]:
		return nil, fmt.Errorf("unstructured: Strategy %q is not auto, fast, hi_res, ocr_only or vlm", config.Strategy)
	case config.Timeout <= 0:
		return nil, errors.New("unstructured: Timeout is required")
	case config.MaxResponseBytes <= 0:
		return nil, errors.New("unstructured: MaxResponseBytes is required")
	}
	return &Client{config: config, http: &http.Client{}}, nil
}

func (*Client) ID() parse.ID { return parse.Unstructured }

func (*Client) Reads(extension string) bool { return extensions[extension] }

// Parse sends the file once. The endpoint's documentation says nothing about
// which failures are safe to repeat, and each accepted request is billed by
// the page, so every failure is typed and returned; the job queue decides
// about the transient kinds.
func (c *Client) Parse(ctx context.Context, source parse.Source) (parse.Document, error) {
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()

	body, err := multipartfile.New(ctx, []multipartfile.Field{
		{Name: "strategy", Value: c.config.Strategy},
		{Name: "output_format", Value: "application/json"},
		// No chunking_strategy: see the package comment.
	}, "files", source)
	if err != nil {
		return parse.Document{}, &parse.Error{Kind: parse.Unavailable, Parser: parse.Unstructured, Message: "the file could not be opened", Err: err}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.URL, body.Reader)
	if err != nil {
		body.Reader.Close()
		return parse.Document{}, err
	}
	request.ContentLength = body.Length
	request.Header.Set("Content-Type", body.ContentType)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("unstructured-api-key", c.config.APIKey)

	response, err := c.http.Do(request)
	if err != nil {
		return parse.Document{}, hostedapi.TransportError(ctx, parse.Unstructured, "the partition request", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return parse.Document{}, hostedapi.StatusError(parse.Unstructured, "the partition request", response)
	}
	document, err := render(response.Body, c.config.MaxResponseBytes)
	if err != nil && ctx.Err() != nil {
		return parse.Document{}, hostedapi.TransportError(ctx, parse.Unstructured, "the partition request", err)
	}
	return document, err
}
