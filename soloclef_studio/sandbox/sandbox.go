// Package sandbox is this tier's client for the OpenSandbox server: the
// place code a model wrote is run, so that a report, a deck or a workbook
// can be built by a real library (WeasyPrint, python-docx, python-pptx,
// openpyxl) and rendered by LibreOffice, without any of it running in this
// process or reaching the network.
//
// The server starts each sandbox as a container of its own on the host. This
// package talks to two things over the server's one address: the server's
// lifecycle routes (create, list, renew, delete) and, through the server's
// proxy, the small daemon inside each sandbox that runs commands and moves
// files (execd, port 44772). It needs the server's address and key and
// nothing else; it never reaches a sandbox directly.
//
// # One sandbox per thread
//
// A chat thread has at most one sandbox, found by a label on the sandbox
// itself ("soloclef_thread"). The server is the record of which sandbox is
// whose:
//
//   - Any replica can serve any turn: a process that has never seen the
//     thread finds its sandbox by the label.
//   - An account's limit on sandboxes is counted on the server, from the
//     "soloclef_account" label (the owning account's uuid), so it holds
//     across replicas and restarts.
//     It used to be a count of one process's dict.
//
// What this process remembers is only a shortcut: the id it last found for
// a thread and when it last renewed it, so that the calls of one task do
// not each pay for a list and a renewal (measured against the real server:
// about 140 ms and 100 ms). The shortcut is dropped the moment the server
// says the sandbox is gone, and the label is asked again.
//
// # One sandbox per job attempt
//
// A queued render (the worker's video) has a sandbox of its own, found by
// the "soloclef_job" label (job.go): it lives as long as one attempt of the
// job and is removed when the attempt ends, whatever its thread is doing.
//
// # One stack's sandboxes
//
// The server finds sandboxes by label among every container on its Docker,
// not only the ones it made, and a thread's or a job's label is its id,
// which every stack counts from one. So every sandbox also carries the
// "soloclef_stack" label, the deployment's own name for itself
// (Settings.Stack), and every lookup asks for it too: two stacks on one
// host (a test stack beside the real one, or the Go stack beside the
// Python one) never find, renew, count or remove each other's sandboxes,
// and a sandbox without the label, whoever made it, is never touched.
//
// # Expiry
//
// A sandbox is created with a time to live and the server removes it when
// that passes. Every use renews it, and a command that runs longer than a
// third of the time to live renews it while it runs, so an idle sandbox goes
// away by itself and a busy one never dies under its command.
package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The labels a sandbox carries.
const (
	threadLabel  = "soloclef_thread"
	accountLabel = "soloclef_account"
	// stackLabel is the deployment's name (Settings.Stack), on every
	// sandbox and in every lookup: see the package comment.
	stackLabel = "soloclef_stack"
)

// stackName is what Settings.Stack may be: a Compose project's name (the
// value a deployment is expected to give it), which is also a label value
// the server and Docker accept.
var stackName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// execdPort is where the daemon inside every sandbox listens.
const execdPort = "44772"

// keyHeader carries the server's API key.
const keyHeader = "OPEN-SANDBOX-API-KEY"

// maxOutputBytes bounds what one command's output may take in this
// process. A command that prints more has the rest dropped, and its result
// says so; the tools above this cut far shorter for the model.
const maxOutputBytes = 1 << 20

// The entry process and environment the sandbox image needs: OpenSandbox
// has to be told the image's entrypoint, and PYTHON_VERSION picks which of
// the image's interpreters is on the path.
var (
	imageEntrypoint = []string{"/opt/code-interpreter/code-interpreter.sh"}
	imageEnv        = map[string]string{"PYTHON_VERSION": "3.12"}
)

// ErrNotFound is a file that is not in the sandbox.
var ErrNotFound = errors.New("sandbox: the file does not exist")

// ErrTooLarge is a file bigger than the caller's limit.
var ErrTooLarge = errors.New("sandbox: the file is larger than the limit")

// ErrGone is a sandbox that no longer exists: it expired or was removed
// between being found and being used.
var ErrGone = errors.New("sandbox: the sandbox no longer exists")

// ErrAccountFull is an account that already has as many sandboxes as it
// may. It is not a failure of the server: other conversations are using
// them, and they free themselves when idle.
var ErrAccountFull = errors.New("sandbox: the account has reached its limit of sandboxes")

// Settings is what a deployment says about its sandboxes. All are required:
// none has a default here, so that a missing one is a start-up error and
// never a sandbox of a size nobody chose.
type Settings struct {
	// Stack names this deployment's sandboxes apart from every other
	// stack's on the same Docker (the package comment): the Compose
	// project's name, so that a second copy of a stack started under
	// another project has sandboxes of its own.
	Stack string
	// URL is the OpenSandbox server's root, e.g. http://opensandbox-server:8080.
	URL string
	// APIKey is the server's key.
	APIKey string
	// Image is the sandbox image, with the document libraries, LibreOffice
	// and the skills in it.
	Image string
	// CPU and Memory are one sandbox's hard limits, in the server's own
	// spelling ("2", "6Gi").
	CPU    string
	Memory string
	// IdleTTL is how long a sandbox outlives its last use.
	IdleTTL time.Duration
	// CommandTimeout is the longest one command may run, and the bound on
	// each call to the server.
	CommandTimeout time.Duration
	// MaxPerAccount is how many sandboxes one account may have at once.
	MaxPerAccount int
}

func (s Settings) check() error {
	var missing []string
	need := func(name string, set bool) {
		if !set {
			missing = append(missing, name)
		}
	}
	need("Stack", s.Stack != "")
	need("URL", strings.TrimSpace(s.URL) != "")
	need("APIKey", s.APIKey != "")
	need("Image", strings.TrimSpace(s.Image) != "")
	need("CPU", strings.TrimSpace(s.CPU) != "")
	need("Memory", strings.TrimSpace(s.Memory) != "")
	// The server refuses a time to live under a minute.
	need("IdleTTL (at least one minute)", s.IdleTTL >= time.Minute)
	need("CommandTimeout", s.CommandTimeout > 0)
	need("MaxPerAccount", s.MaxPerAccount >= 1)
	if len(missing) > 0 {
		return fmt.Errorf("sandbox: not set: %s", strings.Join(missing, ", "))
	}
	if !stackName.MatchString(s.Stack) {
		return fmt.Errorf("sandbox: Stack %q is not a stack's name (lower-case letters, digits, '-' and '_')", s.Stack)
	}
	if _, err := url.Parse(s.URL); err != nil {
		return fmt.Errorf("sandbox: URL is not an address: %w", err)
	}
	return nil
}

// Client reaches the OpenSandbox server. It is safe for concurrent use and
// holds no sandbox: see the package comment.
type Client struct {
	settings Settings
	root     string
	http     *http.Client
	now      func() time.Time

	// creating serialises "find or create" per thread within this process,
	// so two tool calls of one thread cannot each find nothing and each
	// create a sandbox. Across processes the chat's own turn lease does the
	// same job: one thread runs one turn at a time.
	mu       sync.Mutex
	creating map[string]*sync.Mutex
	// known is the shortcut the package comment describes: thread to the
	// sandbox last found for it.
	known map[string]known

	// keepAliveEvery is how often a call in flight renews its sandbox: a
	// third of the time to live.
	keepAliveEvery time.Duration
}

// known is a sandbox this process found for a thread, and when it last
// renewed it.
type known struct {
	id      string
	renewed time.Time
}

// New checks the settings and returns a client. httpClient must have no
// Timeout of its own: a command's stream is read for as long as the command
// runs, and each call is bounded by its context instead.
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
		settings: settings, root: strings.TrimRight(strings.TrimSpace(settings.URL), "/") + "/v1",
		http: httpClient, now: time.Now, creating: map[string]*sync.Mutex{}, known: map[string]known{},
		keepAliveEvery: settings.IdleTTL / 3,
	}, nil
}

// Session is one thread's sandbox, for the length of one tool call.
type Session struct {
	client *Client
	// ID is the sandbox's id on the server.
	ID string
	// key is what this process remembers the sandbox under: the thread's
	// id, or "job:" and the job's name (job.go).
	key string
}

// gone passes err on, first dropping this process's memory of the sandbox
// if err says the server no longer has it.
func (s *Session) gone(err error) error {
	if errors.Is(err, ErrGone) {
		s.client.forget(s.key, s.ID)
	}
	return err
}

// Result is how a command ended.
type Result struct {
	// Output is what the command printed, standard output and standard
	// error together, each as the daemon delivered it.
	Output string
	// ExitCode is the command's exit status; -1 when it was killed.
	ExitCode int
	// TimedOut says the daemon killed the command at its time limit.
	TimedOut bool
	// Truncated says Output is not all of what was printed.
	Truncated bool
}

// OK reports whether the command succeeded.
func (r Result) OK() bool { return r.ExitCode == 0 }

// lockFor returns the mutex of one thread's "find or create".
func (c *Client) lockFor(thread string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock := c.creating[thread]
	if lock == nil {
		lock = &sync.Mutex{}
		c.creating[thread] = lock
	}
	return lock
}

// ForThread returns the thread's sandbox, renewed, creating it if the thread
// has none. An account at its limit gets ErrAccountFull.
func (c *Client) ForThread(ctx context.Context, userID string, threadID int64) (*Session, error) {
	thread := strconv.FormatInt(threadID, 10)
	lock := c.lockFor(thread)
	lock.Lock()
	defer lock.Unlock()

	// The shortcut: a sandbox this process renewed less than half a time
	// to live ago is certainly still there, and is used without asking.
	c.mu.Lock()
	last, remembered := c.known[thread]
	c.mu.Unlock()
	if remembered && c.now().Sub(last.renewed) < c.settings.IdleTTL/2 {
		return &Session{client: c, ID: last.id, key: thread}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, c.settings.CommandTimeout)
	defer cancel()
	candidates := []string{}
	if remembered {
		// Renewing the one remembered saves the list when it is still
		// there, which it nearly always is.
		candidates = append(candidates, last.id)
	} else {
		live, err := c.list(ctx, threadLabel, thread)
		if err != nil {
			return nil, err
		}
		candidates = live
	}
	for attempt := 0; attempt < 2; attempt++ {
		for _, found := range candidates {
			// Renewed before it is handed over: a sandbox found a second
			// before its expiry would otherwise die under the first
			// command.
			err := c.renew(ctx, found)
			if err == nil {
				c.remember(thread, found)
				return &Session{client: c, ID: found, key: thread}, nil
			}
			if !errors.Is(err, ErrGone) {
				return nil, err
			}
		}
		if !remembered || attempt == 1 {
			break
		}
		// The remembered one is gone. Another replica may have made the
		// thread a new one since, so the label is asked before creating.
		c.forget(thread, last.id)
		live, err := c.list(ctx, threadLabel, thread)
		if err != nil {
			return nil, err
		}
		candidates = live
	}

	// The limit is the account's, counted on the server by label: every
	// sandbox of every thread the account has, whichever replica made it.
	// The uuid is lowered so that one account is one label value whatever
	// case its token carried.
	account := strings.ToLower(userID)
	others, err := c.list(ctx, accountLabel, account)
	if err != nil {
		return nil, err
	}
	if len(others) >= c.settings.MaxPerAccount {
		return nil, ErrAccountFull
	}
	id, err := c.create(ctx, map[string]string{threadLabel: thread, accountLabel: account})
	if err != nil {
		return nil, err
	}
	c.remember(thread, id)
	return &Session{client: c, ID: id, key: thread}, nil
}

// remember records the sandbox just created or renewed for a thread.
func (c *Client) remember(thread, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.known[thread] = known{id: id, renewed: c.now()}
}

// forget drops the shortcut for a thread, if it still names id. It is
// called when the server says that sandbox is gone.
func (c *Client) forget(thread, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.known[thread].id == id {
		delete(c.known, thread)
		// The thread's lock goes with it when nobody holds it, so a
		// process that has served many threads does not keep a mutex for
		// each for ever. TryLock: a holder is about to use it again.
		if lock := c.creating[thread]; lock != nil && lock.TryLock() {
			delete(c.creating, thread)
			lock.Unlock()
		}
	}
}

// Terminate removes the thread's sandbox, if it has one. It is what deleting
// a thread calls; a thread with no sandbox is not an error.
func (c *Client) Terminate(ctx context.Context, threadID int64) error {
	thread := strconv.FormatInt(threadID, 10)
	return c.terminate(ctx, threadLabel, thread, thread)
}

// terminate removes every sandbox carrying the label, forgetting each under
// key.
func (c *Client) terminate(ctx context.Context, label, value, key string) error {
	ctx, cancel := context.WithTimeout(ctx, c.settings.CommandTimeout)
	defer cancel()
	live, err := c.list(ctx, label, value)
	if err != nil {
		return err
	}
	for _, id := range live {
		c.forget(key, id)
		response, err := c.call(ctx, http.MethodDelete, c.root+"/sandboxes/"+url.PathEscape(id), nil, "")
		if err != nil {
			return err
		}
		drain(response)
		if response.StatusCode >= 300 && response.StatusCode != http.StatusNotFound {
			return fmt.Errorf("sandbox: removing sandbox %s: the server answered %d", id, response.StatusCode)
		}
	}
	return nil
}

// listPageSize is the server's largest page. A label names at most a
// handful of sandboxes (one per thread, a few per account), so one page is
// the whole list; a second page is read if the server says there is one.
const listPageSize = 200

// list returns the ids of this stack's running sandboxes carrying a label.
func (c *Client) list(ctx context.Context, label, value string) ([]string, error) {
	// The server reads the filter as a query string of its own, every pair
	// of which must match (AND), so the stack is asked for alongside the
	// label: another stack's sandbox with the same thread id is not listed.
	filter := url.Values{label: {value}, stackLabel: {c.settings.Stack}}.Encode()
	var ids []string
	for page := 1; ; page++ {
		query := url.Values{
			"metadata": {filter},
			// Pending is a sandbox still starting: it counts against the
			// limit and is this thread's, so it is not created twice.
			"state":    {"Running", "Pending"},
			"page":     {strconv.Itoa(page)},
			"pageSize": {strconv.Itoa(listPageSize)},
		}
		response, err := c.call(ctx, http.MethodGet, c.root+"/sandboxes?"+query.Encode(), nil, "")
		if err != nil {
			return nil, err
		}
		var body struct {
			Items []struct {
				ID       string            `json:"id"`
				Metadata map[string]string `json:"metadata"`
			} `json:"items"`
			Pagination struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pagination"`
		}
		if err := decode(response, "listing sandboxes", &body); err != nil {
			return nil, err
		}
		for _, item := range body.Items {
			// Held to the label and the stack here as well as in the
			// query: a server that ignored the filter would otherwise hand
			// one thread another thread's, or another stack's, sandbox.
			if item.ID != "" && item.Metadata[label] == value && item.Metadata[stackLabel] == c.settings.Stack {
				ids = append(ids, item.ID)
			}
		}
		if !body.Pagination.HasNextPage || len(body.Items) == 0 {
			return ids, nil
		}
	}
}

// create starts a sandbox carrying the labels in metadata and this stack's
// label.
func (c *Client) create(ctx context.Context, metadata map[string]string) (string, error) {
	labels := make(map[string]string, len(metadata)+1)
	for key, value := range metadata {
		labels[key] = value
	}
	labels[stackLabel] = c.settings.Stack
	request := map[string]any{
		"image":      map[string]string{"uri": c.settings.Image},
		"timeout":    int(c.settings.IdleTTL.Seconds()),
		"entrypoint": imageEntrypoint,
		"env":        imageEnv,
		"metadata":   labels,
		// Nothing a model's code does in here may reach the network: not
		// the internet, and not the database on the compose network.
		"networkPolicy":  map[string]string{"defaultAction": "deny"},
		"resourceLimits": map[string]string{"cpu": c.settings.CPU, "memory": c.settings.Memory},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	response, err := c.call(ctx, http.MethodPost, c.root+"/sandboxes", bytes.NewReader(payload), "application/json")
	if err != nil {
		return "", err
	}
	var created struct {
		ID     string `json:"id"`
		Status struct {
			State   string `json:"state"`
			Message string `json:"message"`
		} `json:"status"`
	}
	if err := decode(response, "creating a sandbox", &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", errors.New("sandbox: the server created a sandbox without an id")
	}
	if state := strings.ToLower(created.Status.State); state != "running" && state != "pending" {
		return "", fmt.Errorf("sandbox: the new sandbox is %s: %s", created.Status.State, created.Status.Message)
	}
	return created.ID, nil
}

// renew moves a sandbox's expiry to one time-to-live from now.
func (c *Client) renew(ctx context.Context, id string) error {
	payload, err := json.Marshal(map[string]string{"expiresAt": c.now().Add(c.settings.IdleTTL).UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	response, err := c.call(ctx, http.MethodPost, c.root+"/sandboxes/"+url.PathEscape(id)+"/renew-expiration", bytes.NewReader(payload), "application/json")
	if err != nil {
		return err
	}
	return decode(response, "renewing sandbox "+id, nil)
}

// call sends one request with the key. The caller reads and closes the body.
func (c *Client) call(ctx context.Context, method, address string, body io.Reader, contentType string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set(keyHeader, c.settings.APIKey)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("sandbox: the sandbox server could not be reached: %w", err)
	}
	return response, nil
}

// maxErrorBytes bounds how much of a failed answer is kept for the error.
const maxErrorBytes = 2048

// decode reads a response: a 2xx into out (when out is not nil), anything
// else into an error. 404 is ErrGone, wrapped.
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
	detail, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBytes))
	err := fmt.Errorf("sandbox: %s: the server answered %d: %s", doing, response.StatusCode, strings.TrimSpace(string(detail)))
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w (%v)", ErrGone, err)
	}
	return err
}

// drain reads what is left of a body and closes it, so the connection goes
// back to the pool.
func drain(response *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
}

// execd is the address of a path on the daemon inside the sandbox, through
// the server's proxy.
func (s *Session) execd(path string) string {
	return s.client.root + "/sandboxes/" + url.PathEscape(s.ID) + "/proxy/" + execdPort + path
}

// keepAlive renews the sandbox while a long call is in flight, and returns
// the function that stops it. Renewal otherwise happens only when a call
// starts, so one command that outlived the time to live would have its
// sandbox removed under it.
func (s *Session) keepAlive(ctx context.Context) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.client.keepAliveEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// A failed renewal is not this command's failure: if the
				// sandbox really is gone the command's own stream ends.
				_ = s.client.renew(ctx, s.ID)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// Run runs a shell command in the sandbox and waits for it. Each call is a
// new process (sh -c), so state is carried between calls in files.
//
// A command that exits non-zero is a Result, not an error: its output is
// what the caller wants. An error is the sandbox failing, not the command.
func (s *Session) Run(ctx context.Context, command string) (Result, error) {
	return s.RunWithin(ctx, command, s.client.settings.CommandTimeout)
}

// RunWithin is Run with a time limit of the caller's own instead of
// CommandTimeout: a video's render runs for many minutes, while every other
// command of the same job is held to the short limit.
func (s *Session) RunWithin(ctx context.Context, command string, limit time.Duration) (Result, error) {
	if limit <= 0 {
		return Result{}, errors.New("sandbox: a command needs a time limit")
	}
	// The daemon is told the limit and kills the command at it; the context
	// is given a little longer so that the daemon's "killed" arrives as the
	// answer instead of this side hanging up first.
	ctx, cancel := context.WithTimeout(ctx, limit+transportGrace)
	defer cancel()
	stop := s.keepAlive(ctx)
	defer stop()

	payload, err := json.Marshal(map[string]any{"command": command, "timeout": limit.Milliseconds()})
	if err != nil {
		return Result{}, err
	}
	response, err := s.client.call(ctx, http.MethodPost, s.execd("/command"), bytes.NewReader(payload), "application/json")
	if err != nil {
		return Result{}, err
	}
	// Closed, not drained: the daemon holds a finished command's stream
	// open for about a second, and nothing after the final event is wanted.
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Result{}, s.gone(decode(response, "running a command", nil))
	}
	return readCommand(response.Body)
}

// transportGrace is how much longer than the command's own limit this side
// waits for the daemon to say how the command ended.
const transportGrace = 10 * time.Second

// commandEvent is one event of a command's stream. The stream is JSON
// objects separated by blank lines.
type commandEvent struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Error *struct {
		Name      string   `json:"ename"`
		Value     string   `json:"evalue"`
		Traceback []string `json:"traceback"`
	} `json:"error"`
}

// readCommand reads a command's stream to its final event.
func readCommand(stream io.Reader) (Result, error) {
	var (
		out    strings.Builder
		result Result
	)
	lines := bufio.NewReader(stream)
	for {
		line, err := lines.ReadBytes('\n')
		if text := bytes.TrimSpace(line); len(text) > 0 {
			// Tolerated: the same events framed as server-sent events.
			text = bytes.TrimSpace(bytes.TrimPrefix(text, []byte("data:")))
			var event commandEvent
			if json.Unmarshal(text, &event) == nil {
				switch event.Type {
				case "stdout", "stderr":
					// The daemon sends output a line at a time without
					// the line's end, and an empty line as "\n" alone.
					piece := event.Text
					if piece != "\n" {
						piece += "\n"
					}
					if out.Len()+len(piece) > maxOutputBytes {
						result.Truncated = true
					} else {
						out.WriteString(piece)
					}
				case "execution_complete":
					result.Output = out.String()
					return result, nil
				case "error":
					result.Output = out.String()
					result.ExitCode = 1
					if event.Error != nil {
						if code, convErr := strconv.Atoi(strings.TrimSpace(event.Error.Value)); convErr == nil {
							result.ExitCode = code
						}
						// The daemon reports a command it killed at its
						// time limit as exit -1, "signal: killed".
						result.TimedOut = result.ExitCode == -1 && strings.Contains(strings.Join(event.Error.Traceback, " "), "killed")
					}
					return result, nil
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				// The stream ended without saying how the command did:
				// the sandbox died or the connection was cut.
				return Result{}, errors.New("sandbox: the command's stream ended before the command did")
			}
			return Result{}, fmt.Errorf("sandbox: reading the command's stream: %w", err)
		}
	}
}

// ReadFile reads a file out of the sandbox. A file over limit bytes is
// ErrTooLarge, and nothing of it is returned: half a document is not one.
func (s *Session) ReadFile(ctx context.Context, path string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.client.settings.CommandTimeout)
	defer cancel()
	response, err := s.client.call(ctx, http.MethodGet, s.execd("/files/download")+"?"+url.Values{"path": {path}}.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusNotFound {
		drain(response)
		return nil, ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return nil, s.gone(decode(response, "reading a file", nil))
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("sandbox: reading a file: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	return data, nil
}

// WriteFile writes a file into the sandbox, creating its directories.
func (s *Session) WriteFile(ctx context.Context, path string, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, s.client.settings.CommandTimeout)
	defer cancel()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	// The daemon reads two parts per file: its description, then its bytes.
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="metadata"; filename="metadata"`)
	header.Set("Content-Type", "application/json")
	part, err := form.CreatePart(header)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(part).Encode(map[string]any{"path": path, "mode": 644}); err != nil {
		return err
	}
	header = textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="file"`)
	header.Set("Content-Type", "application/octet-stream")
	if part, err = form.CreatePart(header); err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := form.Close(); err != nil {
		return err
	}
	response, err := s.client.call(ctx, http.MethodPost, s.execd("/files/upload"), &body, form.FormDataContentType())
	if err != nil {
		return err
	}
	return s.gone(decode(response, "writing a file", nil))
}
