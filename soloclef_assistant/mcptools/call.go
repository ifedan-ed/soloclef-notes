package mcptools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// link is one open connection to one server.
type link struct {
	session *mcp.ClientSession
	client  *http.Client
	carrier *carrier
	// print identifies the address and headers the link was opened with,
	// so a turn notices that either changed under it.
	print [sha256.Size]byte
}

func (l *link) close() {
	// Close tells the server the session is over. The library bounds that
	// request itself, so a server that has stopped answering cannot hold a
	// finished turn open.
	_ = l.session.Close()
	l.client.CloseIdleConnections()
}

// carrier puts the connection's headers, which hold its key, on every
// request, and notes whether the server ever answered 401.
type carrier struct {
	next         http.RoundTripper
	headers      map[string]string
	unauthorized atomic.Bool
}

func (c *carrier) RoundTrip(request *http.Request) (*http.Response, error) {
	// A RoundTripper must not change the request it was given.
	request = request.Clone(request.Context())
	for name, value := range c.headers {
		request.Header.Set(name, value)
	}
	response, err := c.next.RoundTrip(request)
	if response != nil && response.StatusCode == http.StatusUnauthorized {
		c.unauthorized.Store(true)
	}
	return response, err
}

// fingerprint is a digest of where a connection goes and what it carries.
// A digest, so that a link does not hold a second copy of the key.
func fingerprint(where target) [sha256.Size]byte {
	names := make([]string, 0, len(where.headers))
	for name := range where.headers {
		names = append(names, name)
	}
	slices.Sort(names)
	digest := sha256.New()
	fmt.Fprintf(digest, "%d:%s", len(where.address), where.address)
	for _, name := range names {
		fmt.Fprintf(digest, "|%d:%s=%d:%s", len(name), name, len(where.headers[name]), where.headers[name])
	}
	return [sha256.Size]byte(digest.Sum(nil))
}

// open connects to a server and completes the protocol's handshake.
func (s *Source) open(ctx context.Context, where target, hosts []string) (*link, error) {
	client, err := s.network.Client(where.address, hosts)
	if err != nil {
		return nil, err
	}
	carried := &carrier{next: client.Transport, headers: where.headers}
	if carried.next == nil {
		carried.next = http.DefaultTransport
	}
	// A copy of the guard's client with the headers added, so its redirect
	// rule and its dialer stay exactly as the guard made them.
	guarded := *client
	guarded.Transport = carried
	session, err := s.client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   strings.TrimSpace(where.address),
		HTTPClient: &guarded,
		// The standalone stream is how a server speaks first: to say its
		// tool list changed, or to ask the client something. This client
		// asks and is answered, and declared no capability a server could
		// call on, so the extra connection would carry nothing.
		DisableStandaloneSSE: true,
	}, &mcp.ClientSessionOptions{ProtocolVersion: protocolVersion})
	if err != nil {
		client.CloseIdleConnections()
		if carried.unauthorized.Load() {
			return nil, errUnauthorized
		}
		return nil, err
	}
	return &link{session: session, client: client, carrier: carried, print: fingerprint(where)}, nil
}

// protocolVersion is the newest version of the protocol this client asks a
// server for; a server that speaks an older one says so in its answer and
// that one is used. It is named, and not left to the library's latest,
// because the library opens every connection to a newer draft with a probe
// request that servers of this version answer with an error, and the
// handshake then starts over: one wasted round trip per server per turn.
// Raise it when the servers people connect have moved.
const protocolVersion = "2025-11-25"

// errUnauthorized is a server answering 401: its key is wrong or has
// expired.
var errUnauthorized = errors.New("the server refused the connection's key (401)")

// discover asks a server for its tools, following the list to its last
// page, saves the list on the connection's row and announces the change.
func (s *Source) discover(ctx context.Context, userID string, row Connection, where target, hosts []string) ([]definition, error) {
	ctx, cancel := context.WithTimeout(ctx, s.limits.Discovery)
	defer cancel()

	opened, err := s.open(ctx, where, hosts)
	if err != nil {
		if errors.Is(err, errUnauthorized) {
			s.expire(ctx, userID, row.ID)
		}
		return nil, err
	}
	defer opened.close()

	var found []definition
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := opened.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			if opened.carrier.unauthorized.Load() {
				s.expire(ctx, userID, row.ID)
				return nil, errUnauthorized
			}
			return nil, err
		}
		for _, listed := range page.Tools {
			// The schema arrives decoded; it is kept as the JSON it was.
			schema, err := json.Marshal(listed.InputSchema)
			if err != nil {
				schema = nil
			}
			found = append(found, definition{name: listed.Name, description: listed.Description, schema: objectSchema(schema)})
		}
		cursor = page.NextCursor
		// Three ways out: the last page, the ceiling (one more than is
		// kept, so that sift can say tools were left out), and a server
		// that hands back a cursor it already gave, which would otherwise
		// be followed for ever.
		if cursor == "" || len(found) > s.limits.ToolsPerServer || seen[cursor] {
			break
		}
		seen[cursor] = true
	}

	info := opened.session.InitializeResult()
	var serverName, serverVersion string
	if info != nil && info.ServerInfo != nil {
		serverName, serverVersion = info.ServerInfo.Name, info.ServerInfo.Version
	}
	transport := "streamable-http"
	cached, err := writeCached(s.siftForStore(found), serverName, serverVersion, transport, s.now())
	if err == nil {
		err = s.rows.StoreDefinitions(ctx, userID, row.ID, cached)
	}
	if err != nil {
		// The turn still has its tools; the next cold build asks again.
		s.logger.WarnContext(ctx, "mcptools: a server's tool list could not be saved", "connector_id", row.ID, "error", err.Error())
		return found, nil
	}
	// After the write, as every writer must: other processes may hold a
	// catalog built while this server was not answering.
	if err := s.generations.Bump(ctx, userID); err != nil {
		s.logger.WarnContext(ctx, "mcptools: the generation counter could not be bumped after a discovery", "user_id", userID, "error", err.Error())
	}
	return found, nil
}

// siftForStore is what of a discovered list is saved on the row: at most
// the ceiling, and no tool whose name cannot be stored (the row is read by
// the Python tier, which a lone surrogate or U+0000 in it would break).
func (s *Source) siftForStore(found []definition) []definition {
	kept := make([]definition, 0, min(len(found), s.limits.ToolsPerServer))
	for _, candidate := range found {
		if len(kept) == s.limits.ToolsPerServer {
			break
		}
		if ok, _ := usable(candidate.name); ok && !strings.ContainsRune(candidate.name, 0) && !strings.ContainsRune(candidate.description, 0) {
			kept = append(kept, candidate)
		}
	}
	return kept
}

// expire flags a connection whose key was refused, so the settings page
// offers to enter it again, and announces the change the first time.
func (s *Source) expire(ctx context.Context, userID string, connectorID int64) {
	// The call that found the 401 may be at its deadline; the flag is
	// worth a few seconds of its own.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	changed, err := s.rows.MarkAuthExpired(ctx, userID, connectorID)
	if err != nil {
		s.logger.WarnContext(ctx, "mcptools: a connection could not be flagged as needing its key again", "connector_id", connectorID, "error", err.Error())
		return
	}
	if !changed {
		return
	}
	if err := s.generations.Bump(ctx, userID); err != nil {
		s.logger.WarnContext(ctx, "mcptools: the generation counter could not be bumped after a 401", "user_id", userID, "error", err.Error())
	}
}

// What the model is told when a call cannot be made. Each is one sentence
// it can act on or pass to the person, and none says anything about the
// server's address.
const (
	gone        = "the MCP server this tool belongs to has been removed or switched off"
	unavailable = "this MCP tool is not available"
	unreachable = "the MCP server could not be reached"
	keyRefused  = "the MCP server refused its key; it has to be entered again in the user's connections"
	ended       = "this turn has ended"
)

// run is a tool's Run: one call, bounded by Limits.Call.
func (t *Set) run(ctx context.Context, entry *offered, call tool.Call) (string, error) {
	limited, cancel := context.WithTimeout(ctx, t.source.limits.Call)
	defer cancel()
	text, err := t.call(limited, entry, call.Args)
	if err == nil {
		return text, nil
	}
	// The caller's context ending means the run is being stopped, and the
	// loop must see that, not a failure it would hand the model.
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if limited.Err() != nil {
		return "", fmt.Errorf("%s did not answer within %s", entry.modelName, t.source.limits.Call)
	}
	return "", err
}

// call makes one call. Every error it returns is text for the model.
func (t *Set) call(ctx context.Context, entry *offered, arguments json.RawMessage) (string, error) {
	source := t.source
	// The row is read now, not when the turn was built: a turn can wait
	// hours for an approval, and the server may have been removed, moved
	// or given a new key since.
	row, err := source.rows.Connection(ctx, t.userID, entry.connectorID)
	if err != nil {
		source.logger.ErrorContext(ctx, "mcptools: a connection could not be read", "connector_id", entry.connectorID, "error", err.Error())
		return "", errors.New("the tool's connection could not be read")
	}
	if row == nil {
		return "", errors.New(gone)
	}
	where, err := readTarget(row.Config)
	if err != nil {
		source.logger.WarnContext(ctx, "mcptools: a call was not made", "connector_id", entry.connectorID, "reason", err.Error())
		return "", errors.New(unavailable)
	}
	held, err := t.link(ctx, entry.connectorID, where, source.hosts(where, row.SiteHosts))
	if err != nil {
		return "", t.failed(ctx, entry, where, err)
	}
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	result, err := held.session.CallTool(ctx, &mcp.CallToolParams{Name: entry.name, Arguments: arguments})
	if err != nil {
		// An answer that is an error is still an answer: the server is
		// there and the session is good. Its message is the server's own
		// words about the call ("unknown tool", "invalid arguments"),
		// which the model can act on.
		var answered *jsonrpc.Error
		if errors.As(err, &answered) && !held.carrier.unauthorized.Load() {
			return "", fmt.Errorf("the MCP server refused the call: %s", clip(answered.Message, 500))
		}
		// Anything else may have broken the connection. It is dropped so
		// that the turn's next call to this server starts a fresh one.
		t.drop(entry.connectorID, held)
		if held.carrier.unauthorized.Load() {
			err = errUnauthorized
		}
		return "", t.failed(ctx, entry, where, err)
	}
	text := resultText(result, source.limits.ResultBytes)
	if result.IsError {
		if text == "" {
			text = "the tool reported an error and said nothing more"
		}
		return "", errors.New(text)
	}
	return text, nil
}

// failed turns a connection's failure into what the model is told, and
// writes the detail to the log.
func (t *Set) failed(ctx context.Context, entry *offered, where target, err error) error {
	var refusal *Refusal
	switch {
	case errors.Is(err, errEnded):
		return errors.New(ended)
	case errors.As(err, &refusal):
		t.source.logger.WarnContext(ctx, "mcptools: a call was not made", "connector_id", entry.connectorID, "reason", refusal.Reason)
		return errors.New(unavailable)
	case errors.Is(err, errUnauthorized):
		t.source.logger.WarnContext(ctx, "mcptools: a server refused its key", "connector_id", entry.connectorID)
		t.source.expire(ctx, t.userID, entry.connectorID)
		return errors.New(keyRefused)
	}
	t.source.logger.WarnContext(ctx, "mcptools: a call failed", "connector_id", entry.connectorID, "tool", entry.name, "error", redact(err, where))
	return errors.New(unreachable)
}

var errEnded = errors.New("mcptools: the turn's tools are closed")

// link returns the turn's connection to a server, opening it on first use.
// If the connection's address or headers have changed since it was opened
// (someone edited the server while the turn waited for an approval), the
// old connection is closed and a new one made: the address and the key are
// the row's as it is now, never the turn's memory of them.
func (t *Set) link(ctx context.Context, connectorID int64, where target, hosts []string) (*link, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errEnded
	}
	if held := t.links[connectorID]; held != nil {
		if held.print == fingerprint(where) {
			return held, nil
		}
		delete(t.links, connectorID)
		t.closing.Add(1)
		go func() {
			defer t.closing.Done()
			held.close()
		}()
	}
	opened, err := t.source.open(ctx, where, hosts)
	if err != nil {
		return nil, err
	}
	t.links[connectorID] = opened
	return opened, nil
}

// drop forgets a connection that failed, if it is still the turn's, and
// closes it without making the call wait: ending a session whose server has
// stopped answering takes the library's five seconds, and the call that
// just timed out has used its time already. Close waits for it instead.
func (t *Set) drop(connectorID int64, failed *link) {
	t.mu.Lock()
	if t.closed {
		// Close has taken the turn's connections, this one among them,
		// and is closing them itself.
		t.mu.Unlock()
		return
	}
	if t.links[connectorID] == failed {
		delete(t.links, connectorID)
	}
	// Counted while the lock is held and the Set is open, so that Close,
	// which marks the Set closed under the same lock before it waits,
	// never starts waiting ahead of this.
	t.closing.Add(1)
	t.mu.Unlock()
	go func() {
		defer t.closing.Done()
		failed.close()
	}()
}

// redact is an error's text for the log with the server's address and
// header values taken out. The address can hold a key in its query, and
// the standard library quotes the whole address in every network error.
func redact(err error, where target) string {
	var network *url.Error
	text := err.Error()
	if errors.As(err, &network) {
		text = network.Op + ": " + network.Err.Error()
	}
	if address := strings.TrimSpace(where.address); address != "" {
		text = strings.ReplaceAll(text, address, "[address]")
	}
	for _, value := range where.headers {
		if len(value) >= 8 {
			text = strings.ReplaceAll(text, value, "[header]")
		}
	}
	return text
}

// resultText is a call's result as the model reads it: the text parts as
// they are, and one line for each part that is not text. A picture pasted
// in as base64 would fill the model's context with characters it cannot
// read.
func resultText(result *mcp.CallToolResult, limit int) string {
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		switch part := content.(type) {
		case *mcp.TextContent:
			parts = append(parts, part.Text)
		case *mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[an image, %s, %d bytes, not shown]", orUnknown(part.MIMEType), len(part.Data)))
		case *mcp.AudioContent:
			parts = append(parts, fmt.Sprintf("[audio, %s, %d bytes, not shown]", orUnknown(part.MIMEType), len(part.Data)))
		case *mcp.ResourceLink:
			parts = append(parts, strings.TrimSpace(fmt.Sprintf("[a link to a resource: %s %s]", part.Name, part.URI)))
		case *mcp.EmbeddedResource:
			switch {
			case part.Resource == nil:
			case part.Resource.Text != "":
				parts = append(parts, part.Resource.Text)
			default:
				parts = append(parts, fmt.Sprintf("[a resource, %s, %s, %d bytes, not shown]", part.Resource.URI, orUnknown(part.Resource.MIMEType), len(part.Resource.Blob)))
			}
		default:
			parts = append(parts, "[content of a kind this chat cannot show]")
		}
	}
	text := strings.Join(parts, "\n")
	// A server may answer with structured content only. It is the result,
	// so the model is given it as the JSON it is.
	if strings.TrimSpace(text) == "" && result.StructuredContent != nil {
		if encoded, err := json.Marshal(result.StructuredContent); err == nil {
			text = string(encoded)
		}
	}
	if len(text) > limit {
		text = clip(text, limit) + fmt.Sprintf("\n[the result was cut at %d bytes of %d]", limit, len(text))
	}
	return text
}

func orUnknown(kind string) string {
	if kind == "" {
		return "of unknown type"
	}
	return kind
}

// clip cuts text to at most limit bytes without splitting a character.
func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}
