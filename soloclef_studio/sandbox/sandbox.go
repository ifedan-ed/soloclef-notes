// Package sandbox is this tier's client for the stack's sandbox service
// (soloclef_sandboxd): the place code a model wrote is run, so that a report,
// a Word document or a workbook can be built by a real library (WeasyPrint,
// python-docx, openpyxl) and rendered by LibreOffice, without any of it
// running in this process or reaching the network. A slide deck, which this
// process draws itself, comes here only to have LibreOffice draw its
// preview.
//
// The service starts each sandbox as a locked-down container of its own on
// the host (no network, read-only root, no capabilities, CPU, memory and
// process limits) and is the one place that holds the Docker socket. This
// package needs its address and key and nothing else.
//
// # One sandbox per thread
//
// A chat thread has at most one sandbox, which keeps its files between turns.
// The service finds it by the thread's id, creates it when there is none,
// and counts an account's sandboxes against its limit, all in one call
// (ForThread): there is one service per stack, so "find or create" and the
// count are decided in one place, and any replica of the API can serve any
// turn without a cache of its own.
//
// # Expiry
//
// A sandbox is removed by the service once it has been idle for its time to
// live. A call in flight is use: a command that runs for twenty minutes
// keeps its sandbox by running, so this side sends no renewals.
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound is a file that is not in the sandbox.
var ErrNotFound = errors.New("sandbox: the file does not exist")

// ErrTooLarge is a file bigger than the caller's limit.
var ErrTooLarge = errors.New("sandbox: the file is larger than the limit")

// ErrGone is a sandbox that no longer exists: it expired, was removed, or
// its container died, between being found and being used.
var ErrGone = errors.New("sandbox: the sandbox no longer exists")

// ErrAccountFull is an account that already has as many sandboxes as it
// may, and freed none within the call's wait (none for ForThread). It is
// not a failure of the service: other conversations or outputs are using
// them, and they free themselves when finished or idle.
var ErrAccountFull = errors.New("sandbox: the account has reached its limit of sandboxes")

// Settings is what a deployment says about its sandboxes. All are required:
// none has a default here, so that a missing one is a start-up error and
// never a sandbox of a size nobody chose.
type Settings struct {
	// URL is the sandbox service's root, e.g. http://soloclefgo-sandboxd-1:8080.
	URL string
	// APIKey is the service's key.
	APIKey string
	// CPU and Memory are one sandbox's hard limits, in the settings'
	// spelling ("2", "6Gi"), which the service reads.
	CPU    string
	Memory string
	// IdleTTL is how long a sandbox outlives its last use.
	IdleTTL time.Duration
	// CommandTimeout is the longest one command may run, and the bound on
	// each other call to the service.
	CommandTimeout time.Duration
	// MaxPerAccount is how many thread sandboxes one account may have at
	// once.
	MaxPerAccount int
}

func (s Settings) check() error {
	var missing []string
	need := func(name string, set bool) {
		if !set {
			missing = append(missing, name)
		}
	}
	need("URL", strings.TrimSpace(s.URL) != "")
	need("APIKey", s.APIKey != "")
	need("CPU", strings.TrimSpace(s.CPU) != "")
	need("Memory", strings.TrimSpace(s.Memory) != "")
	// The service refuses a time to live under a minute.
	need("IdleTTL (at least one minute)", s.IdleTTL >= time.Minute)
	need("CommandTimeout", s.CommandTimeout > 0)
	need("MaxPerAccount", s.MaxPerAccount >= 1)
	if len(missing) > 0 {
		return fmt.Errorf("sandbox: not set: %s", strings.Join(missing, ", "))
	}
	if address, err := url.Parse(s.URL); err != nil || address.Host == "" {
		return fmt.Errorf("sandbox: URL %q is not an address", s.URL)
	}
	return nil
}

// Client reaches the sandbox service. It is safe for concurrent use and
// holds no state of its own: see the package comment.
type Client struct {
	settings Settings
	root     string
	http     *http.Client
}

// New checks the settings and returns a client. httpClient must have no
// Timeout of its own: a render's command and a video's download take as
// long as they take, and each call is bounded by its context instead.
func New(settings Settings, httpClient *http.Client) (*Client, error) {
	if err := settings.check(); err != nil {
		return nil, err
	}
	if httpClient == nil {
		return nil, errors.New("sandbox: the HTTP client is not set")
	}
	if httpClient.Timeout != 0 {
		return nil, errors.New("sandbox: the HTTP client must have no Timeout; calls are bounded by their context")
	}
	return &Client{
		settings: settings,
		root:     strings.TrimRight(strings.TrimSpace(settings.URL), "/") + "/v1/sandboxes",
		http:     httpClient,
	}, nil
}

// Session is one sandbox, for the length of one tool call or one job
// attempt.
type Session struct {
	client *Client
	// ID is the sandbox's id on the service.
	ID string
}

// Result is how a command ended.
type Result struct {
	// Output is what the command printed, standard output and standard
	// error together, exactly as written.
	Output string `json:"output"`
	// ExitCode is the command's exit status; -1 when it was stopped at its
	// time limit.
	ExitCode int `json:"exitCode"`
	// TimedOut says the command was stopped at its time limit.
	TimedOut bool `json:"timedOut"`
	// Truncated says Output is not all of what was printed.
	Truncated bool `json:"truncated"`
}

// OK reports whether the command succeeded.
func (r Result) OK() bool { return r.ExitCode == 0 }

// ForThread returns the thread's sandbox, creating it if the thread has
// none. An account at its limit gets ErrAccountFull.
func (c *Client) ForThread(ctx context.Context, userID string, threadID int64) (*Session, error) {
	return c.acquire(ctx, acquireRequest{
		Kind: "thread", Key: strconv.FormatInt(threadID, 10), Account: strings.ToLower(userID),
		MaxPerAccount: c.settings.MaxPerAccount,
	})
}

// WaitForThread is ForThread for a caller nobody is watching (a Studio
// output, an automation's step): when the account is at its limit, the
// service holds the call until one of the account's sandboxes is removed,
// serving waiters in the order they asked, for at most wait. Only a wait
// that runs out is ErrAccountFull. The call is one request held open, not a
// loop of retries: the service wakes it the moment a place frees.
func (c *Client) WaitForThread(ctx context.Context, userID string, threadID int64, wait time.Duration) (*Session, error) {
	if wait <= 0 {
		return nil, errors.New("sandbox: a wait for a sandbox must be positive; ForThread is the call that does not wait")
	}
	return c.acquire(ctx, acquireRequest{
		Kind: "thread", Key: strconv.FormatInt(threadID, 10), Account: strings.ToLower(userID),
		MaxPerAccount: c.settings.MaxPerAccount, WaitSeconds: int64((wait + time.Second - 1) / time.Second),
	})
}

// Terminate removes the thread's sandbox, if it has one. It is what deleting
// a thread calls; a thread with no sandbox is not an error.
func (c *Client) Terminate(ctx context.Context, threadID int64) error {
	return c.release(ctx, "thread", strconv.FormatInt(threadID, 10))
}

// acquireRequest is the body of the service's find-or-create.
type acquireRequest struct {
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Account string `json:"account"`
	// The account's limit.
	MaxPerAccount int    `json:"maxPerAccount"`
	CPU           string `json:"cpu"`
	Memory        string `json:"memory"`
	IdleTTL       int64  `json:"idleTtlSeconds"`
	// WaitSeconds is how long the service may hold the call for a place
	// in the account's limit; 0 (left out) is "answer at once".
	WaitSeconds int64 `json:"waitSeconds,omitempty"`
}

func (c *Client) acquire(ctx context.Context, request acquireRequest) (*Session, error) {
	request.CPU, request.Memory = c.settings.CPU, c.settings.Memory
	request.IdleTTL = int64(c.settings.IdleTTL / time.Second)
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	// The call's own bound, plus the wait it asked for: the service holds a
	// waiting call open, and this side must not hang up before it answers.
	ctx, cancel := context.WithTimeout(ctx, c.settings.CommandTimeout+time.Duration(request.WaitSeconds)*time.Second)
	defer cancel()
	response, err := c.call(ctx, http.MethodPost, c.root, bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := decode(response, "finding the "+request.Kind+"'s sandbox", &created); err != nil {
		return nil, err
	}
	if created.ID == "" {
		return nil, errors.New("sandbox: the service answered a sandbox without an id")
	}
	return &Session{client: c, ID: created.ID}, nil
}

func (c *Client) release(ctx context.Context, kind, key string) error {
	ctx, cancel := context.WithTimeout(ctx, c.settings.CommandTimeout)
	defer cancel()
	response, err := c.call(ctx, http.MethodDelete, c.root+"/"+kind+"/"+url.PathEscape(key), nil, "")
	if err != nil {
		return err
	}
	return decode(response, "removing the "+kind+"'s sandbox", nil)
}

// call sends one request with the key. The caller reads and closes the body.
func (c *Client) call(ctx context.Context, method, address string, body io.Reader, contentType string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.settings.APIKey)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("sandbox: the sandbox service could not be reached: %w", err)
	}
	return response, nil
}

// maxErrorBytes bounds how much of a failed answer is kept for the error.
const maxErrorBytes = 2048

// decode reads an answer: a 2xx into out (when out is not nil), anything
// else into an error carrying the service's code, with the codes callers act
// on as this package's errors.
func decode(response *http.Response, doing string, out any) error {
	defer drain(response)
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if out == nil {
			return nil
		}
		if err := json.NewDecoder(response.Body).Decode(out); err != nil {
			return fmt.Errorf("sandbox: %s: the answer could not be read: %w", doing, err)
		}
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBytes))
	var failure struct {
		Code    string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &failure)
	detail := failure.Message
	if detail == "" {
		detail = strings.TrimSpace(string(raw))
	}
	err := fmt.Errorf("sandbox: %s: the service answered %d: %s", doing, response.StatusCode, detail)
	switch failure.Code {
	case "gone":
		return fmt.Errorf("%w (%v)", ErrGone, err)
	case "not_found":
		return fmt.Errorf("%w (%v)", ErrNotFound, err)
	case "account_full":
		return fmt.Errorf("%w (%v)", ErrAccountFull, err)
	}
	return err
}

// drain reads what is left of a body and closes it, so the connection goes
// back to the pool.
func drain(response *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
}

// at is the address of a route on this session's sandbox.
func (s *Session) at(route string) string {
	return s.client.root + "/" + url.PathEscape(s.ID) + route
}

// Run runs a shell command (bash) in the sandbox, in /workspace, and waits
// for it. Each call is a new process, so state is carried between calls in
// files.
//
// A command that exits non-zero is a Result, not an error: its output is
// what the caller wants. An error is the sandbox failing, not the command.
//
// It is held to CommandTimeout. If ctx ends first, the service stops the
// command and everything it started.
func (s *Session) Run(ctx context.Context, command string) (Result, error) {
	limit := s.client.settings.CommandTimeout
	if limit <= 0 {
		return Result{}, errors.New("sandbox: a command needs a time limit")
	}
	// The service stops the command at its limit (and kills it a few
	// seconds after); this side waits a little longer so that the answer
	// "stopped at its limit" arrives instead of this side hanging up first.
	ctx, cancel := context.WithTimeout(ctx, limit+transportGrace)
	defer cancel()
	payload, err := json.Marshal(map[string]any{"command": command, "timeoutMs": limit.Milliseconds()})
	if err != nil {
		return Result{}, err
	}
	response, err := s.client.call(ctx, http.MethodPost, s.at("/commands"), bytes.NewReader(payload), "application/json")
	if err != nil {
		return Result{}, err
	}
	var result Result
	if err := decode(response, "running a command", &result); err != nil {
		return Result{}, err
	}
	return result, nil
}

// transportGrace is how much longer than a command's own limit this side
// waits for the service to say how it ended: the service's grace between
// stopping and killing a command (five seconds) and some to spare.
const transportGrace = 15 * time.Second

// ReadFile reads a file out of the sandbox. A file over limit bytes is
// ErrTooLarge, refused on its size before any of it is sent: half a
// document is not one.
func (s *Session) ReadFile(ctx context.Context, path string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.client.settings.CommandTimeout)
	defer cancel()
	stream, size, err := s.open(ctx, path)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	if size > limit {
		return nil, ErrTooLarge
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(stream, data); err != nil {
		return nil, fmt.Errorf("sandbox: reading %s: %w", path, err)
	}
	return data, nil
}

// open asks for a file and answers its body and its size.
func (s *Session) open(ctx context.Context, path string) (io.ReadCloser, int64, error) {
	response, err := s.client.call(ctx, http.MethodGet, s.at("/files")+"?"+url.Values{"path": {path}}.Encode(), nil, "")
	if err != nil {
		return nil, 0, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, 0, decode(response, "reading "+path, nil)
	}
	if response.ContentLength < 0 {
		drain(response)
		return nil, 0, fmt.Errorf("sandbox: reading %s: the service did not say the file's size", path)
	}
	return response.Body, response.ContentLength, nil
}

// WriteFile writes a file into the sandbox, creating its directories.
func (s *Session) WriteFile(ctx context.Context, path string, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, s.client.settings.CommandTimeout)
	defer cancel()
	response, err := s.client.call(ctx, http.MethodPut, s.at("/files")+"?"+url.Values{"path": {path}}.Encode(), bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		return err
	}
	return decode(response, "writing "+path, nil)
}
