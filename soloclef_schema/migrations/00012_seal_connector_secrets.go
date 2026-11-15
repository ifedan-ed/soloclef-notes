package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// 00012: an MCP server's secrets sealed at rest, and Exa and Tavily made
// connections of their own.
//
// Until now an MCP server's headers and an address carrying a key were
// stored as typed, in the clear, in `config.server_config`, and the only way
// to give the chat an Exa or Tavily key was to add that provider's MCP
// server. This migration, for every MCP row:
//
//   - an enabled server on exa.ai or tavily.com whose key can be read (a
//     header or the address, as the chat used to dig it out) becomes the
//     account's web search connection for that provider (migration 00011's
//     table), the key sealed, and the MCP row is removed. The oldest such
//     row of an account wins, as it did when the key was looked up; a later
//     one is removed all the same, because the provider is now a connection
//     and its MCP tools would only duplicate the native ones;
//   - every other server is rewritten in the sealed form (mcpserver): each
//     header value sealed with its mask beside it, the address sealed when
//     its query carries a key, "http" spelled "streamable-http", and an
//     `Authorization: Bearer …` header read as bearer authentication, which
//     is what it is. Nothing else of the row changes.
//
// A row that is not a remote server this tier could ever have saved (no
// address, a local stdio server, a header that is not text) stops the
// migration with its id: guessing at it could send a key somewhere nobody
// typed, and an operator can look at one row.
func sealConnectorSecrets(ctx context.Context, tx *sql.Tx, keys Keys) error {
	if keys.SecretKey == "" {
		return ErrNoSecretKey
	}
	serverSeal, err := sealedsecret.New(keys.SecretKey, sealedsecret.MCPServerSecret)
	if err != nil {
		return err
	}
	keySeal, err := sealedsecret.New(keys.SecretKey, sealedsecret.WebSearchKey)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, user_id::text, enabled, config
		FROM search_source_connectors
		WHERE connector_type = 'MCP_CONNECTOR'
		ORDER BY id
		FOR UPDATE`)
	if err != nil {
		return err
	}
	type row struct {
		id      int64
		owner   string
		enabled bool
		config  []byte
	}
	var servers []row
	for rows.Next() {
		var held row
		if err := rows.Scan(&held.id, &held.owner, &held.enabled, &held.config); err != nil {
			rows.Close()
			return err
		}
		servers = append(servers, held)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, server := range servers {
		var config map[string]json.RawMessage
		if err := json.Unmarshal(server.config, &config); err != nil || config == nil {
			return fmt.Errorf("MCP connector %d: its config is not an object", server.id)
		}
		legacy, err := readLegacyServer(config["server_config"])
		if err != nil {
			return fmt.Errorf("MCP connector %d: %w", server.id, err)
		}

		if provider, key := webSearchKeyOf(legacy); server.enabled && key != "" {
			sealed, err := keySeal.Seal(server.owner, key)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO web_search_connections (user_id, provider, api_key, key_mask)
				VALUES ($1::uuid, $2, $3, $4)
				ON CONFLICT (user_id, provider) DO NOTHING`,
				server.owner, provider, sealed, mcpserver.MaskOf(key)); err != nil {
				return fmt.Errorf("MCP connector %d: moving its key: %w", server.id, err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM search_source_connectors WHERE id = $1`, server.id); err != nil {
				return err
			}
			continue
		}

		stored, err := mcpserver.Build(serverSeal, server.owner, legacy.settings(), nil)
		if err != nil {
			return fmt.Errorf("MCP connector %d: sealing its settings: %w", server.id, err)
		}
		column, err := json.Marshal(stored)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE search_source_connectors
			SET config = jsonb_set(config, '{server_config}', $2::jsonb, true), updated_at = now()
			WHERE id = $1`, server.id, string(column)); err != nil {
			return fmt.Errorf("MCP connector %d: %w", server.id, err)
		}
	}
	return nil
}

// legacyServer is `server_config` as it was written before this migration.
type legacyServer struct {
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Transport *string           `json:"transport"`
	// A local server's three fields. Only ever saved empty, by an old form.
	Command json.RawMessage `json:"command"`
	Args    json.RawMessage `json:"args"`
	Env     json.RawMessage `json:"env"`
}

func readLegacyServer(raw json.RawMessage) (legacyServer, error) {
	var legacy legacyServer
	if len(raw) == 0 || json.Unmarshal(raw, &legacy) != nil {
		return legacyServer{}, fmt.Errorf("its server_config is not an object of text headers")
	}
	for _, field := range []json.RawMessage{legacy.Command, legacy.Args, legacy.Env} {
		switch strings.TrimSpace(string(field)) {
		case "", "null", `""`, "[]", "{}":
		default:
			return legacyServer{}, fmt.Errorf("it is a local (stdio) server, which was never run")
		}
	}
	transport := mcpserver.StreamableHTTP
	if legacy.Transport != nil {
		switch *legacy.Transport {
		case "streamable-http", "http":
		case "sse":
			transport = mcpserver.SSE
		default:
			return legacyServer{}, fmt.Errorf("its transport %q is not a remote one", *legacy.Transport)
		}
	}
	legacy.Transport = &transport
	if strings.TrimSpace(legacy.URL) == "" {
		return legacyServer{}, fmt.Errorf("it has no address")
	}
	return legacy, nil
}

// settings is the legacy server as the sealed form describes it. A single
// `Authorization: Bearer <token>` header is bearer authentication; every
// other header stays an extra header, exactly as it was sent.
func (l legacyServer) settings() mcpserver.Settings {
	settings := mcpserver.Settings{Address: strings.TrimSpace(l.URL), Transport: *l.Transport,
		Auth: mcpserver.Auth{Kind: mcpserver.AuthNone}, Headers: map[string]string{}}
	for name, value := range l.Headers {
		if strings.EqualFold(name, "authorization") && settings.Auth.Kind == mcpserver.AuthNone {
			if scheme, token, split := strings.Cut(strings.TrimSpace(value), " "); split &&
				strings.EqualFold(scheme, "bearer") && strings.TrimSpace(token) != "" {
				settings.Auth = mcpserver.Auth{Kind: mcpserver.AuthBearer, Secret: strings.TrimSpace(token)}
				continue
			}
		}
		settings.Headers[name] = value
	}
	return settings
}

// The providers' own domains: a key is taken only from a server on its
// provider's domain, so another server's x-api-key is never sent to Exa.
var webSearchHosts = map[string]string{"exa": "exa.ai", "tavily": "tavily.com"}

// The query parameters each provider's MCP server takes a key in.
var webSearchQueryNames = map[string][]string{
	"exa":    {"exaApiKey", "exa_api_key", "apiKey", "api_key"},
	"tavily": {"tavilyApiKey", "tavily_api_key", "apiKey", "api_key"},
}

// webSearchKeyOf is the provider and key a legacy server holds, or "" for a
// server that is no provider's or carries no key it can be read from. It is
// the reading the chat used to make at every search, made once.
func webSearchKeyOf(legacy legacyServer) (provider, key string) {
	address, err := url.Parse(strings.TrimSpace(legacy.URL))
	if err != nil {
		return "", ""
	}
	host := strings.ToLower(address.Hostname())
	for candidate, domain := range webSearchHosts {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			provider = candidate
		}
	}
	if provider == "" {
		return "", ""
	}
	for name, value := range legacy.Headers {
		if provider == "exa" && strings.EqualFold(name, "x-api-key") && strings.TrimSpace(value) != "" {
			return provider, strings.TrimSpace(value)
		}
	}
	for name, value := range legacy.Headers {
		if !strings.EqualFold(name, "authorization") {
			continue
		}
		if scheme, token, split := strings.Cut(strings.TrimSpace(value), " "); split &&
			strings.EqualFold(scheme, "bearer") && strings.TrimSpace(token) != "" {
			return provider, strings.TrimSpace(token)
		}
	}
	query := address.Query()
	for _, name := range webSearchQueryNames[provider] {
		if value := strings.TrimSpace(query.Get(name)); value != "" {
			return provider, value
		}
	}
	return "", ""
}

// unsealConnectorSecrets is the way back, for a development database only
// (the migrate command refuses down-to anywhere else): each sealed server
// is written in the clear again, and each web search connection becomes
// its provider's MCP server once more, with the key in the header the chat
// used to read it from. An OAuth sign-in has no clear form; its server
// keeps its address and loses the sign-in.
func unsealConnectorSecrets(ctx context.Context, tx *sql.Tx, keys Keys) error {
	if keys.SecretKey == "" {
		return ErrNoSecretKey
	}
	serverSeal, err := sealedsecret.New(keys.SecretKey, sealedsecret.MCPServerSecret)
	if err != nil {
		return err
	}
	keySeal, err := sealedsecret.New(keys.SecretKey, sealedsecret.WebSearchKey)
	if err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id, user_id::text, config -> 'server_config'
		FROM search_source_connectors
		WHERE connector_type = 'MCP_CONNECTOR'
		ORDER BY id
		FOR UPDATE`)
	if err != nil {
		return err
	}
	type row struct {
		id     int64
		owner  string
		server []byte
	}
	var servers []row
	for rows.Next() {
		var held row
		if err := rows.Scan(&held.id, &held.owner, &held.server); err != nil {
			rows.Close()
			return err
		}
		servers = append(servers, held)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, server := range servers {
		stored, err := mcpserver.Parse(server.server)
		if err != nil {
			return fmt.Errorf("MCP connector %d: %w", server.id, err)
		}
		var tokens *mcpserver.Tokens
		if stored.Auth.Type == mcpserver.AuthOAuth && stored.Auth.OAuth.Authorized() {
			if tokens, err = mcpserver.OpenTokens(serverSeal, server.owner, stored.Auth.OAuth); err != nil {
				return fmt.Errorf("MCP connector %d: %w", server.id, err)
			}
		}
		target, err := mcpserver.Open(serverSeal, server.owner, stored, tokens)
		if err != nil && err != mcpserver.ErrNotSignedIn {
			return fmt.Errorf("MCP connector %d: %w", server.id, err)
		}
		if err == mcpserver.ErrNotSignedIn {
			target, _ = mcpserver.Open(serverSeal, server.owner, withoutAuth(stored), nil)
		}
		plain, err := json.Marshal(map[string]any{"url": target.Address, "headers": target.Headers, "transport": stored.Transport})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE search_source_connectors SET config = jsonb_set(config, '{server_config}', $2::jsonb, true)
			WHERE id = $1`, server.id, string(plain)); err != nil {
			return err
		}
	}

	connections, err := tx.QueryContext(ctx, `SELECT user_id::text, provider, api_key FROM web_search_connections ORDER BY user_id, provider`)
	if err != nil {
		return err
	}
	type connection struct{ owner, provider, sealed string }
	var held []connection
	for connections.Next() {
		var one connection
		if err := connections.Scan(&one.owner, &one.provider, &one.sealed); err != nil {
			connections.Close()
			return err
		}
		held = append(held, one)
	}
	connections.Close()
	if err := connections.Err(); err != nil {
		return err
	}
	for _, one := range held {
		key, err := keySeal.Open(one.owner, one.sealed)
		if err != nil {
			return fmt.Errorf("the %s key of %s: %w", one.provider, one.owner, err)
		}
		name, server := "Exa", map[string]any{"url": "https://mcp.exa.ai/mcp", "headers": map[string]string{"x-api-key": key}, "transport": "streamable-http"}
		if one.provider == "tavily" {
			name, server = "Tavily", map[string]any{"url": "https://mcp.tavily.com/mcp/", "headers": map[string]string{"Authorization": "Bearer " + key}, "transport": "streamable-http"}
		}
		config, err := json.Marshal(map[string]any{"server_config": server, "server_origin": "member"})
		if err != nil {
			return err
		}
		// A name the account already uses keeps its server; this is a
		// development database's way back, not a merge.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO search_source_connectors (user_id, name, connector_type, config)
			VALUES ($1::uuid, $2, 'MCP_CONNECTOR', $3::jsonb)
			ON CONFLICT (user_id, connector_type, name) DO NOTHING`, one.owner, name, string(config)); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM web_search_connections`)
	return err
}

// withoutAuth is a server with its sign-in left out, for the one way back
// that has no clear form for it.
func withoutAuth(stored mcpserver.Stored) mcpserver.Stored {
	stored.Auth = mcpserver.StoredAuth{Type: mcpserver.AuthNone}
	return stored
}
