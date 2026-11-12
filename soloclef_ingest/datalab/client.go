// Package datalab is the Datalab parser: a client for Datalab's hosted
// Marker API (documentation.datalab.to, "Convert Document" and "Convert
// Result Check", read 2026-11-11).
//
// The protocol: POST the file to /api/v1/convert, which answers with a
// request_id at once, then GET /api/v1/convert/{request_id} until its status
// is "complete". The Markdown comes back with a marker line before each
// page, which is how the page offsets are known.
package datalab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/hostedapi"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/multipartfile"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// Endpoint is Datalab's convert endpoint. Datalab has one; it is a constant
// of the service, not a setting of this deployment.
const Endpoint = "https://www.datalab.to/api/v1/convert"

// submitAttempts is how many times the upload is sent while Datalab answers
// 429. Three, with Config.RateLimitWait between them, is the retry Datalab's
// own "Rate Limits" page shows for raw API calls.
const submitAttempts = 3

// pageLimitMessage begins Datalab's 429 on a poll when the team's pages in
// flight are over its limit. Unlike a request-rate 429 it is final for this
// request: "a rejected request is not queued", and polling it again keeps
// answering 429.
const pageLimitMessage = "Page rate limit exceeded"

// Config is everything the client needs. Every field is required.
type Config struct {
	APIKey string
	// Endpoint is the convert URL: the Endpoint constant, except in tests.
	Endpoint string
	// Mode is Datalab's quality tier: lite, fast, balanced or accurate. It
	// sets the price per page, so it is chosen, not defaulted.
	Mode string
	// PollInterval is the pause between two status checks.
	PollInterval time.Duration
	// RateLimitWait is the pause before the upload is sent again after a
	// 429. Datalab's limits are per minute and its example waits 60 s.
	RateLimitWait time.Duration
	// Timeout bounds one parse end to end. When it passes, the job is left
	// to finish unobserved and the parse fails as TimedOut; it is not sent
	// again, because a second submission is billed again.
	Timeout time.Duration
	// MaxResponseBytes bounds the result reply.
	MaxResponseBytes int64
}

var modes = map[string]bool{"lite": true, "fast": true, "balanced": true, "accurate": true}

// extensions are Datalab's documented input types, less the ones read in Go
// (.csv, .html), which never reach a document parser.
var extensions = map[string]bool{
	".pdf": true, ".doc": true, ".docx": true, ".odt": true, ".ppt": true, ".pptx": true,
	".odp": true, ".xls": true, ".xlsx": true, ".xlsm": true, ".xltx": true, ".ods": true,
	".epub": true, ".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true,
	".tiff": true,
}

// Extensions lists what Datalab reads, sorted.
func Extensions() []string {
	return []string{
		".doc", ".docx", ".epub", ".gif", ".jpeg", ".jpg", ".odp", ".ods", ".odt", ".pdf",
		".png", ".ppt", ".pptx", ".tiff", ".webp", ".xls", ".xlsm", ".xlsx", ".xltx",
	}
}

// Client is the Datalab parser.
type Client struct {
	config Config
	http   *http.Client
}

// New checks the configuration and returns a Client.
func New(config Config) (*Client, error) {
	endpoint, err := url.Parse(config.Endpoint)
	switch {
	case config.APIKey == "":
		return nil, errors.New("datalab: APIKey is required")
	case err != nil || endpoint.Host == "":
		return nil, fmt.Errorf("datalab: Endpoint %q is not a URL", config.Endpoint)
	case !modes[config.Mode]:
		return nil, fmt.Errorf("datalab: Mode %q is not lite, fast, balanced or accurate", config.Mode)
	case config.PollInterval <= 0:
		return nil, errors.New("datalab: PollInterval is required")
	case config.RateLimitWait <= 0:
		return nil, errors.New("datalab: RateLimitWait is required")
	case config.Timeout <= 0:
		return nil, errors.New("datalab: Timeout is required")
	case config.MaxResponseBytes <= 0:
		return nil, errors.New("datalab: MaxResponseBytes is required")
	}
	config.Endpoint = strings.TrimRight(config.Endpoint, "/")
	return &Client{config: config, http: &http.Client{}}, nil
}

func (*Client) ID() parse.ID { return parse.Datalab }

func (*Client) Reads(extension string) bool { return extensions[extension] }

func (c *Client) Parse(ctx context.Context, source parse.Source) (parse.Document, error) {
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()

	requestID, err := c.submit(ctx, source)
	if err != nil {
		return parse.Document{}, err
	}
	markdown, err := c.result(ctx, requestID)
	if err != nil {
		return parse.Document{}, err
	}
	return paged(markdown)
}

// submit uploads the file and returns the request's id.
//
// It is sent again only after a 429, which says the request was not taken:
// that is the one retry Datalab documents. A network error or a 5xx leaves
// it unknown whether a conversion was started, and a second one is billed,
// so those fail as Unavailable and the decision goes to the job queue.
func (c *Client) submit(ctx context.Context, source parse.Source) (string, error) {
	fields := []multipartfile.Field{
		{Name: "output_format", Value: "markdown"},
		{Name: "mode", Value: c.config.Mode},
		// A marker line before each page; see splitPages.
		{Name: "paginate", Value: "true"},
		// Images would come back as base64 in the reply, megabytes that are
		// dropped on arrival: the Markdown is all that is indexed.
		{Name: "disable_image_extraction", Value: "true"},
	}
	for attempt := 1; ; attempt++ {
		// A new body every attempt: the file is read from storage again,
		// never kept in memory for the sake of a retry.
		body, err := multipartfile.New(ctx, fields, "file", source)
		if err != nil {
			return "", &parse.Error{Kind: parse.Unavailable, Parser: parse.Datalab, Message: "the file could not be opened", Err: err}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.Endpoint, body.Reader)
		if err != nil {
			body.Reader.Close()
			return "", err
		}
		request.ContentLength = body.Length
		request.Header.Set("Content-Type", body.ContentType)
		request.Header.Set("X-API-Key", c.config.APIKey)

		response, err := c.http.Do(request)
		if err != nil {
			return "", hostedapi.TransportError(ctx, parse.Datalab, "the upload", err)
		}
		if response.StatusCode == http.StatusTooManyRequests && attempt < submitAttempts {
			response.Body.Close()
			if err := hostedapi.Wait(ctx, c.config.RateLimitWait); err != nil {
				return "", hostedapi.TransportError(ctx, parse.Datalab, "the upload", err)
			}
			continue
		}
		requestID, err := readSubmission(response)
		response.Body.Close()
		return requestID, err
	}
}

func readSubmission(response *http.Response) (string, error) {
	if response.StatusCode != http.StatusOK {
		return "", hostedapi.StatusError(parse.Datalab, "the upload", response)
	}
	var submitted struct {
		// A pointer: the field's documented default is true, so only an
		// explicit false is a refusal.
		Success   *bool  `json:"success"`
		Error     string `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := hostedapi.DecodeJSON(parse.Datalab, "the upload", response.Body, 64<<10, &submitted); err != nil {
		return "", err
	}
	if submitted.Success != nil && !*submitted.Success {
		return "", &parse.Error{Kind: parse.Rejected, Parser: parse.Datalab, Message: "Datalab refused the file: " + submitted.Error}
	}
	if submitted.RequestID == "" {
		return "", &parse.Error{Kind: parse.BadResponse, Parser: parse.Datalab, Message: "the upload was accepted without a request_id"}
	}
	return submitted.RequestID, nil
}

// result polls the request until it is complete and returns its Markdown.
//
// The check URL is built from the request id and the configured endpoint
// (the documented GET /api/v1/convert/{request_id}). The reply also carries
// a request_check_url; it is not followed, because following it would send
// the API key to whatever address a reply names.
func (c *Client) result(ctx context.Context, requestID string) (string, error) {
	checkURL := c.config.Endpoint + "/" + url.PathEscape(requestID)
	for {
		if err := hostedapi.Wait(ctx, c.config.PollInterval); err != nil {
			return "", hostedapi.TransportError(ctx, parse.Datalab, "the conversion", err)
		}
		done, markdown, err := c.check(ctx, checkURL)
		if err != nil {
			return "", err
		}
		if done {
			return markdown, nil
		}
	}
}

// check asks once. A poll is a GET and changes nothing, so anything that may
// pass (a dropped connection, a 5xx, a request-rate 429) is simply asked
// again at the next interval until the parse's own deadline; the job itself
// is not lost by a failed question about it.
func (c *Client) check(ctx context.Context, checkURL string) (done bool, markdown string, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		return false, "", err
	}
	request.Header.Set("X-API-Key", c.config.APIKey)
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return false, "", hostedapi.TransportError(ctx, parse.Datalab, "the conversion", err)
		}
		return false, "", nil
	}
	defer response.Body.Close()

	switch {
	case response.StatusCode == http.StatusTooManyRequests:
		failure := hostedapi.StatusError(parse.Datalab, "the status check", response)
		if strings.Contains(failure.Message, pageLimitMessage) {
			return false, "", failure
		}
		return false, "", nil
	case response.StatusCode >= 500:
		return false, "", nil
	case response.StatusCode != http.StatusOK:
		// 404 is a result that expired or was deleted; any other 4xx is as
		// final.
		return false, "", hostedapi.StatusError(parse.Datalab, "the status check", response)
	}

	var reply struct {
		Status   string `json:"status"`
		Success  *bool  `json:"success"`
		Error    string `json:"error"`
		Markdown string `json:"markdown"`
	}
	if err := hostedapi.DecodeJSON(parse.Datalab, "the status check", response.Body, c.config.MaxResponseBytes, &reply); err != nil {
		return false, "", err
	}
	failed := reply.Status == "failed" || (reply.Status == "complete" && reply.Success != nil && !*reply.Success)
	if failed {
		// Datalab tried and could not convert this file. Not sent again:
		// the same file fails the same way, and is billed.
		return false, "", &parse.Error{Kind: parse.ParseFailed, Parser: parse.Datalab, Message: "Datalab could not convert the file: " + reply.Error}
	}
	return reply.Status == "complete", reply.Markdown, nil
}
