package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
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
	// StoreDefinitions saves a server's tool list on its row, where every
	// process (and the Python tier) reads it instead of asking the server.
	StoreDefinitions(ctx context.Context, userID string, connectorID int64, cached []byte) error
	// MarkAuthExpired flags a connection whose server refused its key.
	// changed is true the first time only.
	MarkAuthExpired(ctx context.Context, userID string, connectorID int64) (changed bool, err error)
	// StoreTokens is TokenRows': an OAuth server's renewed tokens.
	StoreTokens(ctx context.Context, userID string, connectorID int64, previous, sealed string, expiresAt int64) (stored bool, err error)
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

func (p Postgres) StoreDefinitions(ctx context.Context, userID string, connectorID int64, cached []byte) error {
	_, err := New(p.Pool).SetConfigKey(ctx, SetConfigKeyParams{Key: keyCached, Value: cached, ID: connectorID, UserID: userID})
	return err
}

func (p Postgres) StoreTokens(ctx context.Context, userID string, connectorID int64, previous, sealed string, expiresAt int64) (bool, error) {
	changed, err := New(p.Pool).StoreTokens(ctx, StoreTokensParams{
		Sealed: sealed, ExpiresAt: expiresAt, ID: connectorID, UserID: userID, Previous: previous,
	})
	return changed > 0, err
}

func (p Postgres) MarkAuthExpired(ctx context.Context, userID string, connectorID int64) (bool, error) {
	changed, err := New(p.Pool).MarkAuthExpired(ctx, MarkAuthExpiredParams{ID: connectorID, UserID: userID})
	return changed > 0, err
}

// The keys of a connection's config this package reads or writes. They are
// the Python tier's, because both tiers read the same rows.
const (
	keyServer = "server_config"
	keyOrigin = "server_origin"
	keyCached = "cached_tools"
)

// target is where a connection goes and what it carries: everything about a
// server that is read from its row at the moment of use, opened there, and
// kept nowhere.
type target struct {
	origin    string
	address   string
	transport string
	headers   map[string]string
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

// readServer reads a connection's stored server settings and who added it,
// opening nothing. The error says why the server cannot be connected to;
// it never contains the address or a secret.
func readServer(config []byte) (mcpserver.Stored, string, error) {
	top := keys(config)
	raw, found := top[keyServer]
	if !found {
		return mcpserver.Stored{}, "", errors.New("the connection has no server_config")
	}
	stored, err := mcpserver.Parse(raw)
	if err != nil {
		// A local (stdio) server, a row from before its secrets were
		// sealed, or a damaged one: never guessed at.
		return mcpserver.Stored{}, "", errors.New("the connection's server_config is not a remote server's sealed settings")
	}
	// Anything not marked "member" is a site server: rows from before the
	// two kinds existed carry no marker, and they were all the site's.
	origin := originSite
	var marker string
	if json.Unmarshal(top[keyOrigin], &marker) == nil && marker == originMember {
		origin = originMember
	}
	return stored, origin, nil
}

// target opens a connection's settings for one connection: the one place a
// stored secret leaves its seal, at the moment it is needed. An OAuth
// server's access token is renewed first when it has run out (tokens.go).
func (s *Source) target(ctx context.Context, userID string, row Connection) (target, error) {
	stored, origin, err := readServer(row.Config)
	if err != nil {
		return target{}, err
	}
	var tokens *mcpserver.Tokens
	if stored.Auth.Type == mcpserver.AuthOAuth {
		if tokens, err = s.currentTokens(ctx, userID, row, stored, origin); err != nil {
			return target{}, err
		}
	}
	opened, err := mcpserver.Open(s.seal, userID, stored, tokens)
	if err != nil {
		return target{}, err
	}
	return target{origin: origin, address: strings.TrimSpace(opened.Address), transport: stored.Transport, headers: opened.Headers}, nil
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
