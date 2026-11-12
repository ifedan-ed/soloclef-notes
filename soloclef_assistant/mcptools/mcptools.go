// Package mcptools offers an account's MCP connections to the chat's model
// as tools.
//
// An account connects remote MCP servers in its settings. Each is a row of
// search_source_connectors whose config holds the server's address and
// headers (server_config), who added it (server_origin), the tools it
// advertised when it was last asked (cached_tools) and the tools its owner
// said may run without asking (trusted_tools). For a turn, Source.ForTurn
// returns a Set: the tools to hand the model, the two functions the
// approval step needs (Trusted, Trust), and a Close that ends the turn's
// connections.
//
// # What is cached, and what never is
//
// One thing is cached per process: an account's tool definitions (names,
// descriptions, schemas) and the model-facing name of each. Everything a
// permission rests on is read from the connection's row at the moment it is
// used and kept nowhere:
//
//   - the server's address and key, at each call;
//   - the trusted list, at each decision;
//   - whether the connection still exists in the account, at both;
//   - the administrators' list of allowed hosts, at each call.
//
// So removing a server, changing its key or untrusting a tool takes effect
// at the very next call from any process, with nothing to invalidate. One
// primary-key read per call is nothing beside the network call after it.
//
// # The generation counter
//
// The definitions cache is invalidated across processes by a counter in
// Valkey, soloclef:mcp:generation:{user_id} (GenerationKey). At the
// start of a turn the number is read (one GET). If it is the number the
// process's entry was built under, the entry is used; if not, the tools are
// rebuilt from Postgres and filed under the number that was read before the
// rows were. When Valkey cannot be read the turn builds from Postgres and
// caches nothing, which is slower and never stale. An entry is also dropped
// after Limits.Lifetime whatever the number says, which bounds the one gap
// left: a writer that committed and died before its INCR.
//
// Every write that changes which tools an account has must INCR the key
// after its transaction commits: the connector routes do, with
// Generations.Bump (internal/searchconnectors). This package does it itself
// after it saves a freshly discovered tool list and after it flags a
// connection whose key was refused. A connection switched off is not
// offered, and a call to one is refused at the call, whatever is cached.
//
// # Names
//
// A tool keeps its server's name when a provider would accept it and no
// other connection in the account uses it; otherwise the model is given
// mcp_{connection id}_{cleaned name} (assemble has the exact rules). The
// model-facing name is used for nothing else. A call goes to the server
// under the server's own name, and trust is recorded under the connection's
// id and the server's own name, so a rename caused by connecting a second
// server neither loses nor grants trust. The names are a pure function of
// the account's rows, so the process that resumes a paused turn finds the
// paused call's tool under the name the first process gave it.
//
// # What this does better than the Python it replaces
//
//   - The cache is invalidated across processes. Python's is a dict in one
//     process that another process's write cannot clear.
//   - Trust, the address and the key are not in the cache at all. Python
//     bakes all three into the cached callables for five minutes, and the
//     trusted list into a compiled agent kept for thirty.
//   - tools/list is followed to its last page, up to a ceiling. Python
//     reads the first page and stops.
//   - A name a provider would refuse is cleaned before the model sees it.
//     Python hands it over as it is, and one such name fails the request.
//   - One connection per server per turn. Python connects and initialises
//     again for every call.
//   - "Always allow" is refused for a tool that always asks, and for a name
//     that would break the row, at the one place it is written, with the
//     row locked so two clicks cannot lose each other's name.
//   - A tool that reports an error (isError) is a failed result. Python
//     returns its text as if the call had succeeded.
//   - Pictures and other binary content are described to the model in a
//     line, and a result is cut at a limit. Python pastes the base64 in.
//   - Errors shown to the model and written to the log never contain the
//     server's address, which can hold its key. Python's include the
//     exception's text.
//   - The allowed-hosts list is read with the connection's row at each
//     call. Python reads a copy up to a minute old.
//   - The tools are offered in every retrieval scope. Python denies them
//     all in the default scope; asking first is the control that matters.
package mcptools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// Settings is what a Source is built from. Everything is required unless
// its comment says optional; there are no defaults for an address, a list
// or a limit.
type Settings struct {
	// Rows is the database. Postgres in a deployment.
	Rows Rows
	// Generations is the shared counter. A *Valkey in a deployment.
	Generations Generations
	// Network decides where a connection may go. PublicInternet in a
	// deployment; only tests have reason to set anything else.
	Network Network
	Logger  *slog.Logger
	Now     func() time.Time

	// ClientName is the name this deployment gives itself to the servers
	// it connects to.
	ClientName string

	// SiteHostsWhenUnset is the hosts a site server may reach while the
	// administrators have saved no list of their own (no mcp_allowed_hosts
	// row in billing_settings). It must be set, to an empty list if the
	// answer is "none". The Python tier's answer is in
	// app/mcp_security/allowlist.py (DEFAULT_ALLOWED_HOSTS), and the two
	// tiers must be given the same one.
	SiteHostsWhenUnset []string

	// ReservedNames are the names of the chat's own tools, every one it
	// may offer in any turn. An MCP tool with one of these names is given
	// its prefixed name instead. Optional only in the sense that a chat
	// with no tools of its own has none to list.
	ReservedNames []string

	Limits Limits
}

// Limits are the sizes and times. All are required.
type Limits struct {
	// ToolsPerServer is the most tools taken from one server. The rest are
	// left out and the log says how many.
	ToolsPerServer int
	// Discovery is how long one server has to list its tools.
	Discovery time.Duration
	// Call is how long one call has, connecting included when the call is
	// the turn's first to that server.
	Call time.Duration
	// Lifetime is how long an account's definitions are kept at most.
	Lifetime time.Duration
	// RetryIncomplete is how long they are kept when a server that had to
	// be asked did not answer: long enough that a dead server does not
	// cost every turn a wait, short enough that a server which was only
	// restarting is back soon. It may not exceed Lifetime.
	RetryIncomplete time.Duration
	// Accounts is how many accounts' definitions one process keeps.
	Accounts int
	// ResultBytes is the most text one call may hand the model.
	ResultBytes int
}

// clientVersion is sent to servers beside Settings.ClientName. It is the
// version of this client's behaviour, not of the product.
const clientVersion = "1.0.0"

// Source builds each turn's MCP tools. One is made at start-up and shared;
// it is safe for concurrent use.
type Source struct {
	rows        Rows
	generations Generations
	network     Network
	logger      *slog.Logger
	now         func() time.Time
	client      *mcp.Client
	siteHosts   []string
	reserved    map[string]bool
	limits      Limits
	cache       definitions
}

// NewSource checks the settings and returns the Source. It connects to nothing.
func NewSource(settings Settings) (*Source, error) {
	var missing []string
	need := func(name string, set bool) {
		if !set {
			missing = append(missing, name)
		}
	}
	limits := settings.Limits
	need("Rows", settings.Rows != nil)
	need("Generations", settings.Generations != nil)
	need("Network", settings.Network != nil)
	need("Logger", settings.Logger != nil)
	need("Now", settings.Now != nil)
	need("ClientName", strings.TrimSpace(settings.ClientName) != "")
	need("SiteHostsWhenUnset (an empty list means none)", settings.SiteHostsWhenUnset != nil)
	need("Limits.ToolsPerServer", limits.ToolsPerServer > 0)
	need("Limits.Discovery", limits.Discovery > 0)
	need("Limits.Call", limits.Call > 0)
	need("Limits.Lifetime", limits.Lifetime > 0)
	need("Limits.RetryIncomplete (positive, at most Lifetime)", limits.RetryIncomplete > 0 && limits.RetryIncomplete <= limits.Lifetime)
	need("Limits.Accounts", limits.Accounts > 0)
	need("Limits.ResultBytes", limits.ResultBytes > 0)
	if len(missing) > 0 {
		return nil, fmt.Errorf("mcptools: these settings are missing or unusable: %s", strings.Join(missing, ", "))
	}
	if checked, ok := settings.Network.(interface{ Check() error }); ok {
		if err := checked.Check(); err != nil {
			return nil, err
		}
	}
	reserved := make(map[string]bool, len(settings.ReservedNames))
	for _, name := range settings.ReservedNames {
		reserved[name] = true
	}
	return &Source{
		rows: settings.Rows, generations: settings.Generations, network: settings.Network,
		logger: settings.Logger, now: settings.Now,
		// No capabilities are declared: this client lists and calls tools.
		// It offers a server no roots to read, no model to sample from and
		// nobody to put a question to.
		client: mcp.NewClient(
			&mcp.Implementation{Name: settings.ClientName, Version: clientVersion},
			&mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}},
		),
		siteHosts: slices.Clone(settings.SiteHostsWhenUnset),
		reserved:  reserved,
		limits:    limits,
		cache:     definitions{entries: map[string]entry{}, most: limits.Accounts},
	}, nil
}

// Bump tells every process that the account's MCP tools have changed. A
// route that adds, edits or removes an MCP connection calls it after its
// transaction has committed.
func (s *Source) Bump(ctx context.Context, userID string) error {
	return s.generations.Bump(ctx, userID)
}

// ForTurn returns userID's MCP tools for one turn. The account is the
// request's principal: it is both whose connections are offered and who is
// asking, so there is no second id to disagree with it. The caller must Close the Set when the turn ends, however it ends.
//
// It fails only when the account's connections cannot be read from
// Postgres. A server that cannot be reached, a row that cannot be read and
// a Valkey that is down each cost what they must (that server's tools, that
// row's, the cache) and are logged; none of them fails the turn.
//
// A turn resumed after a pause calls this again, possibly in another
// process, and gets the same names for the same connections.
func (s *Source) ForTurn(ctx context.Context, userID string) (*Set, error) {
	if userID == "" {
		return nil, errors.New("mcptools: a turn needs the account it runs for")
	}
	// The number is read before the rows. A change committed after this
	// read is filed under the old number and found stale by the next turn;
	// read the other way round, new rows could never be filed under an old
	// number but old rows could be filed under a new one, and kept.
	generation, err := s.generations.Current(ctx, userID)
	counted := err == nil
	if !counted {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.logger.WarnContext(ctx, "mcptools: the generation counter cannot be read; building from the database and caching nothing",
			"user_id", userID, "error", err.Error())
	}
	var built *catalog
	if counted {
		built = s.cache.get(userID, generation, s.now())
	}
	if built == nil {
		built, err = s.build(ctx, userID)
		if err != nil {
			return nil, err
		}
		if counted {
			lifetime := s.limits.Lifetime
			if !built.complete {
				lifetime = s.limits.RetryIncomplete
			}
			s.cache.put(userID, generation, built, s.now(), lifetime)
		}
	}
	return &Set{source: s, userID: userID, catalog: built, links: map[int64]*link{}}, nil
}

// build reads the account's connections and makes its catalog. Servers
// whose row holds no tool list are asked for one, all at the same time.
func (s *Source) build(ctx context.Context, userID string) (*catalog, error) {
	rows, err := s.rows.Connections(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("mcptools: reading account %s's connections: %w", userID, err)
	}
	servers := make([]serverTools, len(rows))
	answered := make([]bool, len(rows))
	var asking sync.WaitGroup
	for i, row := range rows {
		servers[i] = serverTools{id: row.ID, name: row.Name}
		answered[i] = true
		where, err := readTarget(row.Config)
		if err != nil {
			s.logger.WarnContext(ctx, "mcptools: a connection is left out", "connector_id", row.ID, "reason", err.Error())
			continue
		}
		hosts := s.hosts(where, row.SiteHosts)
		if cached, found := readCached(row.Config); found {
			// The saved list is offered only for a server a call could be
			// made to. The network is not touched to find that out here;
			// the full check happens when a call connects.
			probe, err := s.network.Client(where.address, hosts)
			if err != nil {
				s.logger.WarnContext(ctx, "mcptools: a connection is left out", "connector_id", row.ID, "reason", err.Error())
				continue
			}
			probe.CloseIdleConnections()
			servers[i].tools = s.sift(ctx, row.ID, cached)
			continue
		}
		asking.Add(1)
		go func() {
			defer asking.Done()
			found, err := s.discover(ctx, userID, row, where, hosts)
			if err != nil {
				// A refused address will be refused next time too; only
				// a server that might answer later is worth an early retry.
				var refusal *Refusal
				answered[i] = errors.As(err, &refusal)
				s.logger.WarnContext(ctx, "mcptools: a server did not list its tools", "connector_id", row.ID, "error", redact(err, where))
				return
			}
			servers[i].tools = s.sift(ctx, row.ID, found)
		}()
	}
	asking.Wait()
	if ctx.Err() != nil {
		// A catalog cut short by the turn being cancelled is nobody's
		// catalog; it must not be cached as the account's.
		return nil, ctx.Err()
	}
	built := assemble(servers, s.reserved)
	built.complete = !slices.Contains(answered, false)
	return built, nil
}

// hosts is the hosts a server's connection may reach, from who added it
// and the administrators' list read beside its row.
func (s *Source) hosts(where target, storedSiteHosts []byte) []string {
	site, stored := readSiteHosts(storedSiteHosts)
	if !stored {
		site = s.siteHosts
	}
	return reachableHosts(where.origin, where.address, site)
}

// sift is what is kept of one server's tool list: tools that can be called,
// each name once, up to the ceiling. What is left out is logged, by count
// and reason, because "the tool is missing" is otherwise unexplainable.
func (s *Source) sift(ctx context.Context, connectorID int64, listed []definition) []definition {
	kept := make([]definition, 0, min(len(listed), s.limits.ToolsPerServer))
	seen := make(map[string]bool, len(listed))
	for index, candidate := range listed {
		if len(kept) == s.limits.ToolsPerServer {
			s.logger.WarnContext(ctx, "mcptools: a server has more tools than are taken",
				"connector_id", connectorID, "taken", len(kept), "left_out", len(listed)-index)
			break
		}
		if ok, why := usable(candidate.name); !ok {
			s.logger.WarnContext(ctx, "mcptools: a tool is left out", "connector_id", connectorID, "position", index, "reason", why)
			continue
		}
		if seen[candidate.name] {
			s.logger.WarnContext(ctx, "mcptools: a tool is left out", "connector_id", connectorID, "position", index, "reason", "the server listed its name twice")
			continue
		}
		seen[candidate.name] = true
		kept = append(kept, candidate)
	}
	return kept
}

// Set is one turn's MCP tools and the connections its calls have opened.
type Set struct {
	source  *Source
	userID  string
	catalog *catalog

	// mu guards links and closed. It is held while a connection is opened,
	// which is fine: MCP tools are not marked Parallel, so a turn makes one
	// MCP call at a time, and Close is called when none is running.
	mu     sync.Mutex
	links  map[int64]*link
	closed bool
	// closing counts connections that were dropped mid-turn and are still
	// being closed.
	closing sync.WaitGroup
}

// Info is what an approval card says about an MCP tool.
type Info struct {
	// ConnectorID and Server are the connection's id and its name.
	ConnectorID int64
	Server      string
	// Tool is the server's own name for the tool, which can differ from
	// the name the model was given.
	Tool string
	// Description is the server's description, prefixed with the server.
	Description string
	// AlwaysAsk says the tool asks every time and cannot be trusted.
	AlwaysAsk bool
}

// Lookup describes the tool the model knows as name. found is false when
// name is not one of this turn's MCP tools.
func (t *Set) Lookup(name string) (info Info, found bool) {
	entry, found := t.entry(name)
	if !found {
		return Info{}, false
	}
	return Info{
		ConnectorID: entry.connectorID, Server: entry.connectorName, Tool: entry.name,
		Description: entry.description, AlwaysAsk: entry.alwaysAsk,
	}, true
}

func (t *Set) entry(name string) (*offered, bool) {
	index, found := t.catalog.byName[name]
	if !found {
		return nil, false
	}
	return &t.catalog.tools[index], true
}

// Tools is the turn's MCP tools, connections oldest first and each
// server's tools in its own order.
//
// Every one asks before it runs. The tools that fetch an address the model
// chose ask every time (tool.AlwaysAsk); the rest (tool.Ask) may be waived
// by Set.Trusted, which the assembly plugs into agent.Config.Trusted. None
// is Parallel: a server's tools may write, and "create it, then edit it"
// asked for in one batch means that order.
func (t *Set) Tools() []tool.Tool {
	tools := make([]tool.Tool, len(t.catalog.tools))
	for i := range t.catalog.tools {
		entry := &t.catalog.tools[i]
		approval := tool.Ask
		if entry.alwaysAsk {
			approval = tool.AlwaysAsk
		}
		tools[i] = tool.Tool{
			Name:        entry.modelName,
			Description: entry.description,
			Parameters:  entry.schema,
			Approval:    approval,
			Run: func(ctx context.Context, call tool.Call) (string, error) {
				return t.run(ctx, entry, call)
			},
		}
	}
	return tools
}

// Close ends the turn's connections. It is safe to call more than once; a
// call made after it fails.
//
// It returns when every server has been told its session is over, which is
// at once for a server that answers and up to five seconds (the protocol
// library's bound) for one that has stopped answering. A caller with a
// response still to finish should finish it first.
func (t *Set) Close() {
	t.mu.Lock()
	links := t.links
	t.links, t.closed = nil, true
	t.mu.Unlock()
	// Each close tells its server the session is over, which is a request
	// with its own short deadline; side by side, a turn with five servers
	// waits for the slowest and not for the sum.
	for _, held := range links {
		t.closing.Add(1)
		go func() {
			defer t.closing.Done()
			held.close()
		}()
	}
	t.closing.Wait()
}
