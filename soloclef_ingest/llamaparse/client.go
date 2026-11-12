// Package llamaparse is the LlamaParse parser: a client for LlamaIndex's
// hosted v2 Parse API (developers.llamaindex.ai, "Parse API v2 guide" and
// the parsing reference, read 2026-11-11).
//
// The protocol: POST the file to /api/v2/parse/upload, which answers with a
// job id, then GET /api/v2/parse/{id} until job.status is COMPLETED, then
// GET it once more with expand=markdown for the pages.
package llamaparse

import (
	"context"
	"encoding/json"
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

// Config is everything the client needs. Every field is required.
type Config struct {
	APIKey string
	// BaseURL is the region's API address: https://api.cloud.llamaindex.ai
	// for the US, https://api.cloud.eu.llamaindex.ai for the EU. A key works
	// in one region only, and where documents are sent is not something to
	// assume, so there is no default.
	BaseURL string
	// Tier is LlamaParse's quality tier: fast, cost_effective, agentic or
	// agentic_plus. It sets the credits billed per page.
	Tier string
	// Version is the tier's release: "latest", or a dated version from
	// GET /api/v2/parse/versions to freeze the output.
	Version string
	// PollInterval is the pause between two status checks. LlamaIndex's own
	// examples wait two seconds.
	PollInterval time.Duration
	// Timeout bounds one parse end to end. When it passes the parse fails as
	// TimedOut and is not sent again: a second job is billed again.
	Timeout time.Duration
	// MaxResponseBytes bounds the result reply.
	MaxResponseBytes int64
}

var tiers = map[string]bool{"fast": true, "cost_effective": true, "agentic": true, "agentic_plus": true}

// extensions are LlamaParse's supported document types, as the Python tier
// listed them from its documentation, less the formats read in Go.
var extensions = map[string]bool{
	".pdf": true, ".docx": true, ".doc": true, ".xlsx": true, ".xls": true, ".pptx": true,
	".ppt": true, ".docm": true, ".dot": true, ".dotm": true, ".pptm": true, ".pot": true,
	".potx": true, ".xlsm": true, ".xlsb": true, ".xlw": true, ".rtf": true, ".epub": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".tiff": true,
	".tif": true, ".webp": true, ".svg": true, ".odt": true, ".ods": true, ".odp": true,
	".hwp": true, ".hwpx": true,
}

// Extensions lists what LlamaParse reads, sorted.
func Extensions() []string {
	return []string{
		".bmp", ".doc", ".docm", ".docx", ".dot", ".dotm", ".epub", ".gif", ".hwp", ".hwpx",
		".jpeg", ".jpg", ".odp", ".ods", ".odt", ".pdf", ".png", ".pot", ".potx", ".ppt",
		".pptm", ".pptx", ".rtf", ".svg", ".tif", ".tiff", ".webp", ".xls", ".xlsb", ".xlsm",
		".xlsx", ".xlw",
	}
}

// Client is the LlamaParse parser.
type Client struct {
	config    Config
	uploadURL string
	jobURL    *url.URL
	// configuration is the upload's "configuration" form field: the same
	// JSON for every file, so it is encoded once.
	configuration string
	http          *http.Client
}

// New checks the configuration and returns a Client.
func New(config Config) (*Client, error) {
	base, err := url.Parse(config.BaseURL)
	switch {
	case config.APIKey == "":
		return nil, errors.New("llamaparse: APIKey is required")
	case err != nil || base.Host == "":
		return nil, fmt.Errorf("llamaparse: BaseURL %q is not a URL", config.BaseURL)
	case !tiers[config.Tier]:
		return nil, fmt.Errorf("llamaparse: Tier %q is not fast, cost_effective, agentic or agentic_plus", config.Tier)
	case config.Version == "":
		return nil, errors.New("llamaparse: Version is required")
	case config.PollInterval <= 0:
		return nil, errors.New("llamaparse: PollInterval is required")
	case config.Timeout <= 0:
		return nil, errors.New("llamaparse: Timeout is required")
	case config.MaxResponseBytes <= 0:
		return nil, errors.New("llamaparse: MaxResponseBytes is required")
	}
	// Tier and version only. The Python tier also sent OCR languages
	// ["en"], which told the service a French or Yoruba scan was English.
	configuration, err := json.Marshal(map[string]string{"tier": config.Tier, "version": config.Version})
	if err != nil {
		return nil, err
	}
	return &Client{
		config:        config,
		uploadURL:     base.JoinPath("api", "v2", "parse", "upload").String(),
		jobURL:        base.JoinPath("api", "v2", "parse"),
		configuration: string(configuration),
		http:          &http.Client{},
	}, nil
}

func (*Client) ID() parse.ID { return parse.LlamaParse }

func (*Client) Reads(extension string) bool { return extensions[extension] }

func (c *Client) Parse(ctx context.Context, source parse.Source) (parse.Document, error) {
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()

	jobID, err := c.upload(ctx, source)
	if err != nil {
		return parse.Document{}, err
	}
	if err := c.waitUntilCompleted(ctx, jobID); err != nil {
		return parse.Document{}, err
	}
	var result jobReply
	if err := c.get(ctx, jobID, "markdown", &result); err != nil {
		return parse.Document{}, err
	}
	return document(result)
}

// upload sends the file once. LlamaParse's guides document no status after
// which an upload is safe to repeat, and a repeat that did start a job is
// billed, so every failure here is typed and returned. The caller's queue
// decides about Unavailable and RateLimited.
func (c *Client) upload(ctx context.Context, source parse.Source) (string, error) {
	body, err := multipartfile.New(ctx,
		[]multipartfile.Field{{Name: "configuration", Value: c.configuration}}, "file", source)
	if err != nil {
		return "", &parse.Error{Kind: parse.Unavailable, Parser: parse.LlamaParse, Message: "the file could not be opened", Err: err}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.uploadURL, body.Reader)
	if err != nil {
		body.Reader.Close()
		return "", err
	}
	request.ContentLength = body.Length
	request.Header.Set("Content-Type", body.ContentType)
	request.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	response, err := c.http.Do(request)
	if err != nil {
		return "", hostedapi.TransportError(ctx, parse.LlamaParse, "the upload", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", hostedapi.StatusError(parse.LlamaParse, "the upload", response)
	}
	// The create reply has id and status at the top level; a later GET
	// nests the same two under "job".
	var created struct {
		ID string `json:"id"`
	}
	if err := hostedapi.DecodeJSON(parse.LlamaParse, "the upload", response.Body, 64<<10, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", &parse.Error{Kind: parse.BadResponse, Parser: parse.LlamaParse, Message: "the upload was accepted without a job id"}
	}
	return created.ID, nil
}

// jobReply is GET /api/v2/parse/{id}, with the fields this client reads.
type jobReply struct {
	Job struct {
		Status       string `json:"status"`
		ErrorMessage string `json:"error_message"`
	} `json:"job"`
	Markdown struct {
		Pages []struct {
			PageNumber int    `json:"page_number"`
			Success    bool   `json:"success"`
			Markdown   string `json:"markdown"`
			Error      string `json:"error"`
		} `json:"pages"`
	} `json:"markdown"`
}

// waitUntilCompleted polls the job's status alone. The pages are asked for
// once, afterwards: the Python tier asked for them on every poll.
func (c *Client) waitUntilCompleted(ctx context.Context, jobID string) error {
	for {
		var reply jobReply
		if err := c.get(ctx, jobID, "", &reply); err != nil {
			return err
		}
		switch reply.Job.Status {
		case "COMPLETED":
			return nil
		case "FAILED", "CANCELLED":
			return &parse.Error{
				Kind: parse.ParseFailed, Parser: parse.LlamaParse,
				Message: fmt.Sprintf("LlamaParse ended the job %s: %s", reply.Job.Status, reply.Job.ErrorMessage),
			}
		}
		// PENDING, RUNNING: ask again after the interval.
		if err := hostedapi.Wait(ctx, c.config.PollInterval); err != nil {
			return hostedapi.TransportError(ctx, parse.LlamaParse, "the parse", err)
		}
	}
}

// get reads the job, with expand when it is not empty. A GET changes
// nothing, so what may pass (a dropped connection, a 429, a 5xx) is asked
// again after the poll interval until the parse's deadline. Anything else
// is final.
func (c *Client) get(ctx context.Context, jobID, expand string, reply *jobReply) error {
	address := c.jobURL.JoinPath(jobID)
	if expand != "" {
		address.RawQuery = url.Values{"expand": {expand}}.Encode()
	}
	for {
		transient, err := c.getOnce(ctx, address.String(), reply)
		if err != nil {
			return err
		}
		if !transient {
			return nil
		}
		if err := hostedapi.Wait(ctx, c.config.PollInterval); err != nil {
			return hostedapi.TransportError(ctx, parse.LlamaParse, "the parse", err)
		}
	}
}

func (c *Client) getOnce(ctx context.Context, address string, reply *jobReply) (transient bool, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return false, hostedapi.TransportError(ctx, parse.LlamaParse, "the parse", err)
		}
		return true, nil
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusTooManyRequests, response.StatusCode >= 500:
		return true, nil
	case response.StatusCode != http.StatusOK:
		return false, hostedapi.StatusError(parse.LlamaParse, "the job check", response)
	}
	return false, hostedapi.DecodeJSON(parse.LlamaParse, "the job check", response.Body, c.config.MaxResponseBytes, reply)
}

// document joins the pages LlamaParse read and names the ones it did not.
func document(result jobReply) (parse.Document, error) {
	builder := parse.Builder{Separator: "\n\n"}
	var firstFailure string
	read := 0
	for _, page := range result.Markdown.Pages {
		if !page.Success {
			// A failed page carries an error and no content. It is recorded
			// rather than dropped silently, so the document can say which
			// pages are missing from it.
			builder.Skipped(page.PageNumber)
			if firstFailure == "" {
				firstFailure = page.Error
			}
			continue
		}
		read++
		builder.Page(page.PageNumber, strings.Trim(page.Markdown, "\n"))
	}
	if read == 0 && firstFailure != "" {
		// Every page failed: that is a failed parse, not a file with no text.
		return parse.Document{}, &parse.Error{
			Kind: parse.ParseFailed, Parser: parse.LlamaParse,
			Message: "LlamaParse could not read any page: " + firstFailure,
		}
	}
	return builder.Document(parse.LlamaParse)
}
