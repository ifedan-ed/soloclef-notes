package nextcloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Remote is one account's files, as the import and the chat's tools read
// them (Client). It is an interface so a caller can be tested without a
// server; the client itself is tested against one.
type Remote interface {
	// Stat is the file or folder at a normalized path.
	Stat(ctx context.Context, path string) (Entry, error)
	// List is a folder's direct children, folders first.
	List(ctx context.Context, folder string) ([]Entry, error)
	// Search is the files and folders anywhere in the account whose name
	// contains the text, folders first, at most limit of them.
	Search(ctx context.Context, text string, limit int) ([]Entry, error)
	// Walk is every file under a folder, breadth first, and whether a limit
	// cut the walk short.
	Walk(ctx context.Context, folder string, maxFiles int) ([]Entry, bool, error)
	// Open streams a file. The caller closes it.
	Open(ctx context.Context, path string) (io.ReadCloser, error)
}

// Timing is how long this tier waits for Nextcloud. None has a default.
type Timing struct {
	// Listing bounds one PROPFIND, start to finish.
	Listing time.Duration
	// Stall is how long a download may deliver nothing before it is
	// abandoned. It bounds silence, not the download: a large file on a
	// slow server takes as long as it takes, as long as bytes keep coming.
	Stall time.Duration
	// MaxRetryWait caps how long one "slow down" answer is obeyed for.
	MaxRetryWait time.Duration
}

// The limits of a walk and of retrying, which are properties of the
// protocol and of politeness, not of a deployment.
const (
	// maxWalkFolders is how many folders one picked folder may hold before
	// the walk stops and calls the pick too big.
	maxWalkFolders = 500
	// maxAttempts is how often a request is tried when the server answers
	// 429 (its brute-force and rate protection) or a gateway's 502-504.
	maxAttempts = 3
	// listingLimit bounds a PROPFIND reply held in memory. A folder of ten
	// thousand entries is about four megabytes.
	listingLimit = 32 << 20
)

// Client is a read-only WebDAV client for one account. It sends PROPFIND,
// SEARCH and GET and has no code path that writes to Nextcloud.
type Client struct {
	account Account
	http    *http.Client
	timing  Timing
	// sleep waits between attempts; a test replaces it.
	sleep func(context.Context, time.Duration) error
}

// NewClient builds a client over a transport the caller owns.
//
// The client never follows a redirect. A redirect to another host would
// carry the app password there (Go strips Authorization across hosts, but
// not across a downgrade to http on the same one), and a correct Nextcloud
// never redirects a WebDAV request, so a 3xx is reported as the server
// being unusable.
func NewClient(account Account, transport http.RoundTripper, timing Timing) *Client {
	return &Client{
		account: account,
		timing:  timing,
		http: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		sleep: func(ctx context.Context, wait time.Duration) error {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

// send makes one request, retrying the answers that mean "later". On
// success the caller owns the response body.
func (c *Client) send(ctx context.Context, method, path string, header http.Header, body string) (*http.Response, error) {
	return c.sendTo(ctx, method, davURL(c.account.BaseURL, c.account.Username, path), path, header, body)
}

// sendTo is send for an address that is not a path in the user's files
// (the SEARCH endpoint). named is what a "not found" says was not found.
func (c *Client) sendTo(ctx context.Context, method, address, named string, header http.Header, body string) (*http.Response, error) {
	return c.exchange(ctx, exchange{method: method, address: address, named: named, header: header, body: body,
		succeeded: func(status int) bool { return status == http.StatusOK || status == http.StatusMultiStatus },
		later:     retryable})
}

// exchange is one request as send makes it: what counts as success, and
// which answers mean "try again later". A read is retried on every "later"
// answer; a write (writer.go) only on the ones that say nothing was done.
type exchange struct {
	method, address, named string
	header                 http.Header
	body                   string
	succeeded, later       func(status int) bool
	// refused words the statuses a write tells apart (an existing file, a
	// missing folder); nil for a read.
	refused func(status int) error
}

func (c *Client) exchange(ctx context.Context, e exchange) (*http.Response, error) {
	method, address, named, header, body := e.method, e.address, e.named, e.header, e.body
	for attempt := 1; ; attempt++ {
		var payload io.Reader
		if body != "" {
			payload = strings.NewReader(body)
		}
		request, err := http.NewRequestWithContext(ctx, method, address, payload)
		if err != nil {
			return nil, fmt.Errorf("%w: building the request: %v", ErrUnavailable, err)
		}
		for name, values := range header {
			request.Header[name] = values
		}
		request.SetBasicAuth(c.account.LoginName, c.account.AppPassword)
		request.Header.Set("OCS-APIRequest", "true")
		request.Header.Set("User-Agent", "Soloclef Notes")

		response, err := c.http.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// The error's text may hold the address; it is wrapped for the
			// log and never shown.
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		switch status := response.StatusCode; {
		case e.succeeded(status):
			return response, nil
		case e.refused != nil && e.refused(status) != nil:
			drain(response)
			return nil, e.refused(status)
		case e.later(status) && attempt < maxAttempts:
			wait := c.retryWait(response, attempt)
			drain(response)
			if err := c.sleep(ctx, wait); err != nil {
				return nil, err
			}
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			drain(response)
			return nil, ErrAuth
		case status == http.StatusNotFound:
			drain(response)
			return nil, fmt.Errorf("%w: %s", ErrNotFound, named)
		default:
			drain(response)
			return nil, fmt.Errorf("%w: it answered %d", ErrUnavailable, status)
		}
	}
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

// retryWait obeys Retry-After up to the cap, and backs off by doubling when
// the server names no wait.
func (c *Client) retryWait(response *http.Response, attempt int) time.Duration {
	wait := time.Duration(1<<attempt) * time.Second
	if seconds, err := strconv.ParseFloat(strings.TrimSpace(response.Header.Get("Retry-After")), 64); err == nil && seconds >= 0 {
		wait = time.Duration(seconds * float64(time.Second))
	}
	return min(wait, c.timing.MaxRetryWait)
}

// drain empties a small error body so the connection can be reused.
func drain(response *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
}

func (c *Client) propfind(ctx context.Context, path, depth string) ([]Entry, error) {
	ctx, done := context.WithTimeout(ctx, c.timing.Listing)
	defer done()
	response, err := c.send(ctx, "PROPFIND", path,
		http.Header{"Depth": {depth}, "Content-Type": {"application/xml"}}, propfindBody)
	if err != nil {
		return nil, err
	}
	return c.listing(response, path)
}

// listing reads a multistatus reply, bounded, into entries. named is what
// the listing is of, for the error.
func (c *Client) listing(response *http.Response, named string) ([]Entry, error) {
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, listingLimit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: reading the listing: %v", ErrUnavailable, err)
	}
	if len(raw) > listingLimit {
		return nil, fmt.Errorf("%w: the listing of %s is larger than %d bytes", ErrUnavailable, named, listingLimit)
	}
	entries, err := parseMultistatus(raw, c.account.Username)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return entries, nil
}

// Stat is the file or folder at a normalized path.
func (c *Client) Stat(ctx context.Context, path string) (Entry, error) {
	entries, err := c.propfind(ctx, path, "0")
	if err != nil {
		return Entry{}, err
	}
	for _, entry := range entries {
		if entry.Path == path {
			return entry, nil
		}
	}
	return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, path)
}

// List is a folder's direct children, folders first.
func (c *Client) List(ctx context.Context, folder string) ([]Entry, error) {
	entries, err := c.propfind(ctx, folder, "1")
	if err != nil {
		return nil, err
	}
	return children(entries, folder), nil
}

// Walk is every file under a folder, breadth first.
//
// It asks for one level at a time. "Depth: infinity" would be one request,
// and Nextcloud usually refuses it, and where it does not the whole tree
// arrives in one reply of unbounded size. A walk that meets either limit
// stops and says so; the caller reports the pick as too big instead of
// importing an arbitrary part of it.
func (c *Client) Walk(ctx context.Context, folder string, maxFiles int) ([]Entry, bool, error) {
	pending := []string{folder}
	var files []Entry
	for visited := 0; len(pending) > 0; visited++ {
		if visited >= maxWalkFolders {
			return files, true, nil
		}
		current := pending[0]
		pending = pending[1:]
		listed, err := c.List(ctx, current)
		if err != nil {
			return nil, false, err
		}
		for _, entry := range listed {
			if entry.IsFolder {
				pending = append(pending, entry.Path)
				continue
			}
			if len(files) >= maxFiles {
				return files, true, nil
			}
			files = append(files, entry)
		}
	}
	return files, false, nil
}

// Open streams a file's bytes.
//
// Nothing is buffered here: the body returned is the connection's, and the
// caller copies it straight into the file store. A retry happens only
// before the first byte, on the answers that mean "later"; once bytes flow,
// a failure is the caller's to report, because a stream cannot be rewound.
func (c *Client) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	// The context is cancelled when the body stalls or is closed, which is
	// what ends the request.
	ctx, cancel := context.WithCancel(ctx)
	response, err := c.send(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		cancel()
		return nil, err
	}
	body := &stallingBody{body: response.Body, cancel: cancel, stall: c.timing.Stall}
	body.watch = time.AfterFunc(c.timing.Stall, body.timedOut)
	return body, nil
}

// stallingBody is a download that gives up when nothing arrives for the
// stall time. net/http has a timeout for a whole response and none for
// silence, and a whole-response limit is wrong for a file of any size.
type stallingBody struct {
	body   io.ReadCloser
	cancel context.CancelFunc
	stall  time.Duration
	watch  *time.Timer
	// stalled is set by the timer before it cancels the request, so the
	// read that then fails can say why.
	stalled atomic.Bool
}

func (s *stallingBody) timedOut() {
	s.stalled.Store(true)
	s.cancel()
}

func (s *stallingBody) Read(buffer []byte) (int, error) {
	read, err := s.body.Read(buffer)
	if read > 0 {
		s.watch.Reset(s.stall)
	}
	if err != nil && err != io.EOF {
		if s.stalled.Load() {
			return read, fmt.Errorf("%w: it sent nothing for %s", ErrUnavailable, s.stall)
		}
		return read, fmt.Errorf("%w: the download broke off: %v", ErrUnavailable, err)
	}
	return read, err
}

func (s *stallingBody) Close() error {
	s.watch.Stop()
	s.cancel()
	return s.body.Close()
}

// NewConnect is a Client per account over one shared transport, so
// connections to the one Nextcloud server are reused across imports and
// tool calls. Every time must be positive: a zero would be "never wait"
// for one of them and "wait for ever" for another.
func NewConnect(transport http.RoundTripper, timing Timing) (func(Account) Remote, error) {
	switch {
	case transport == nil:
		return nil, errors.New("nextcloud: no HTTP transport to reach Nextcloud with")
	case timing.Listing <= 0 || timing.Stall <= 0 || timing.MaxRetryWait <= 0:
		return nil, fmt.Errorf("nextcloud: the listing timeout, the stall timeout and the retry cap must all be positive, got %v, %v and %v",
			timing.Listing, timing.Stall, timing.MaxRetryWait)
	}
	return func(account Account) Remote { return NewClient(account, transport, timing) }, nil
}
