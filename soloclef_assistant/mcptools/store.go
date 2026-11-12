package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Connection is one MCP connection's row, as read.
type Connection struct {
	ID   int64
	Name string
	// Config is the row's config column, untouched.
	Config []byte
	// SiteHosts is the administrators' list of allowed hosts as stored, or
	// nil when no list has been saved.
	SiteHosts []byte
}

// Rows is what this package asks of the database. It is an interface so that
// everything but the SQL can be tested without a database; Postgres is the
// implementation a deployment uses.
type Rows interface {
	// Connections lists an account's MCP connections, oldest first.
	Connections(ctx context.Context, userID string) ([]Connection, error)
	// Connection reads one connection of the account as it is now, or nil
	// when the account has no such connection any more, or has switched it
	// off. Another account's connection is nil too: the account is part
	// of every statement, so an id from elsewhere names nothing.
	Connection(ctx context.Context, userID string, connectorID int64) (*Connection, error)
	// AddTrusted adds a tool's name to the connection's trusted list,
	// provided the connection is still the account's. found is false, and
	// nothing is written, when it is not.
	AddTrusted(ctx context.Context, userID string, connectorID int64, toolName string) (found bool, err error)
	// StoreDefinitions saves a server's tool list on its row, where every
	// process (and the Python tier) reads it instead of asking the server.
	StoreDefinitions(ctx context.Context, userID string, connectorID int64, cached []byte) error
	// MarkAuthExpired flags a connection whose server refused its key.
	// changed is true the first time only.
	MarkAuthExpired(ctx context.Context, userID string, connectorID int64) (changed bool, err error)
}

// Postgres is Rows over the tier's database.
type Postgres struct {
	Pool *pgxpool.Pool
}

func (p Postgres) Connections(ctx context.Context, userID string) ([]Connection, error) {
	rows, err := New(p.Pool).Servers(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Connection, len(rows))
	for i, row := range rows {
		out[i] = Connection{ID: row.ID, Name: row.Name, Config: row.Config, SiteHosts: row.SiteHosts}
	}
	return out, nil
}

func (p Postgres) Connection(ctx context.Context, userID string, connectorID int64) (*Connection, error) {
	row, err := New(p.Pool).Server(ctx, ServerParams{ID: connectorID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Connection{ID: connectorID, Name: row.Name, Config: row.Config, SiteHosts: row.SiteHosts}, nil
}

func (p Postgres) AddTrusted(ctx context.Context, userID string, connectorID int64, toolName string) (bool, error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	// After a commit this does nothing; before one it undoes the lock.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	queries := New(tx)

	// The row is locked while its list is read and written back, so two
	// approvals arriving together both end up in the list.
	config, err := queries.LockConfig(ctx, LockConfigParams{ID: connectorID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	trusted := readTrusted(config)
	for _, name := range trusted {
		if name == toolName {
			return true, nil
		}
	}
	list, err := json.Marshal(append(trusted, toolName))
	if err != nil {
		return false, err
	}
	changed, err := queries.SetConfigKey(ctx, SetConfigKeyParams{Key: keyTrusted, Value: list, ID: connectorID, UserID: userID})
	if err != nil {
		return false, err
	}
	if changed == 0 {
		// The row is there and locked, so the only thing that stops the
		// write is a config that is not a JSON object.
		return false, fmt.Errorf("mcptools: connection %d has a config that is not an object", connectorID)
	}
	return true, tx.Commit(ctx)
}

func (p Postgres) StoreDefinitions(ctx context.Context, userID string, connectorID int64, cached []byte) error {
	_, err := New(p.Pool).SetConfigKey(ctx, SetConfigKeyParams{Key: keyCached, Value: cached, ID: connectorID, UserID: userID})
	return err
}

func (p Postgres) MarkAuthExpired(ctx context.Context, userID string, connectorID int64) (bool, error) {
	changed, err := New(p.Pool).MarkAuthExpired(ctx, MarkAuthExpiredParams{ID: connectorID, UserID: userID})
	return changed > 0, err
}

// The keys of a connection's config this package reads or writes. They are
// the Python tier's, because both tiers read the same rows: the settings
// pages list, trust and untrust through that tier.
const (
	keyServer  = "server_config"
	keyOrigin  = "server_origin"
	keyTrusted = "trusted_tools"
	keyCached  = "cached_tools"
)

// transports are the values of server_config.transport that mean "a remote
// server". A row without the key, or with "stdio", was a command run inside
// the backend's container; those are never started.
var transports = map[string]bool{"streamable-http": true, "http": true, "sse": true}

// target is where a connection goes and what it carries: everything about a
// server that is read from its row at the moment of use and kept nowhere.
type target struct {
	origin  string
	address string
	headers map[string]string
}

// keys splits a config into its top-level keys without decoding the values.
// A config that is not an object has none.
func keys(config []byte) map[string]json.RawMessage {
	var top map[string]json.RawMessage
	if json.Unmarshal(config, &top) != nil {
		return nil
	}
	return top
}

// readTarget reads a connection's address and headers from its config. The
// error says why the server cannot be connected to; it never contains the
// address or a header's value.
func readTarget(config []byte) (target, error) {
	top := keys(config)
	var server struct {
		Transport *string         `json:"transport"`
		URL       json.RawMessage `json:"url"`
		Headers   json.RawMessage `json:"headers"`
	}
	if raw, found := top[keyServer]; !found || json.Unmarshal(raw, &server) != nil {
		return target{}, errors.New("the connection has no readable server_config")
	}
	if server.Transport == nil || !transports[*server.Transport] {
		return target{}, errors.New("the connection is not a remote server (only streamable-http, http and sse are connected to)")
	}
	out := target{origin: originSite}
	if json.Unmarshal(server.URL, &out.address) != nil || strings.TrimSpace(out.address) == "" {
		return target{}, errors.New("the connection has no address")
	}
	if len(server.Headers) > 0 && string(server.Headers) != "null" {
		// Every value must be text. A number or an object where a key
		// should be is a broken row, and guessing its spelling would send
		// the server something nobody typed.
		if json.Unmarshal(server.Headers, &out.headers) != nil {
			return target{}, errors.New("the connection's headers are not a map of text to text")
		}
	}
	// Anything not marked "member" is a site server: rows from before the
	// two kinds existed carry no marker, and they were all the site's.
	var origin string
	if json.Unmarshal(top[keyOrigin], &origin) == nil && origin == originMember {
		out.origin = originMember
	}
	return out, nil
}

// readTrusted is the connection's trusted list: the text items of
// config.trusted_tools, and nothing when the key is absent or not a list.
func readTrusted(config []byte) []string {
	var items []json.RawMessage
	if json.Unmarshal(keys(config)[keyTrusted], &items) != nil {
		return nil
	}
	trusted := make([]string, 0, len(items))
	for _, item := range items {
		var name string
		if json.Unmarshal(item, &name) == nil && len(item) > 0 && item[0] == '"' {
			trusted = append(trusted, name)
		}
	}
	return trusted
}

// readSiteHosts is the administrators' list of allowed hosts. stored is
// false when no list has been saved (no row, a null, or a value that is not
// a list), which is when a deployment's SiteHostsWhenUnset applies.
func readSiteHosts(raw []byte) (hosts []string, stored bool) {
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, false
	}
	hosts = make([]string, 0, len(items))
	for _, item := range items {
		var host string
		if json.Unmarshal(item, &host) == nil && host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts, true
}
