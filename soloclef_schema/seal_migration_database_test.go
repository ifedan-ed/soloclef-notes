package schema_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
)

const (
	sealOwner = "11111111-1111-4111-8111-111111111111"
	// sealBeforeVersion is the version just before the credential migration.
	sealBeforeVersion = 11
	// sealVersion is the credential migration's own: the test stops there,
	// so what a later migration does to the same rows (00015 removes
	// trusted_tools) is not mistaken for this one's work.
	sealVersion = 12
)

// beforeSealing is a database at the version before 00012, holding one
// account: what a deployment looked like when the migration first ran.
func beforeSealing(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := migrated(t)
	if _, err := schema.DownTo(context.Background(), pool, sealBeforeVersion, testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", sealBeforeVersion, err)
	}
	exec(t, pool, `INSERT INTO "user" (id, email) VALUES ($1, 'ada@example.test')`, sealOwner)
	return pool
}

func addServer(t *testing.T, pool *pgxpool.Pool, name string, enabled bool, config string) {
	t.Helper()
	exec(t, pool, `INSERT INTO search_source_connectors (user_id, name, connector_type, enabled, config)
		VALUES ($1, $2, 'MCP_CONNECTOR', $3, $4::jsonb)`, sealOwner, name, enabled, config)
}

func TestTheCredentialMigrationSealsServersAndMovesWebSearchKeys(t *testing.T) {
	pool := beforeSealing(t)
	ctx := context.Background()
	// Invented keys, none of them anyone's.
	addServer(t, pool, "Exa", true, `{"server_config":{"url":"https://mcp.exa.ai/mcp","headers":{"X-API-Key":"fake-exa-key-0001"},"transport":"http"},"server_origin":"member"}`)
	addServer(t, pool, "Tavily", true, `{"server_config":{"url":"https://mcp.tavily.com/mcp/?tavilyApiKey=fake-tavily-key-0002"},"server_origin":"member"}`)
	addServer(t, pool, "Exa again", true, `{"server_config":{"url":"https://mcp.exa.ai/mcp","headers":{"x-api-key":"fake-exa-key-later"}}}`)
	addServer(t, pool, "Exa switched off", false, `{"server_config":{"url":"https://mcp.exa.ai/mcp","headers":{"x-api-key":"fake-exa-key-off-03"}}}`)
	addServer(t, pool, "Tools", true, `{"server_config":{"url":"https://tools.example.com/mcp?api_key=fake-url-key-0004&region=eu","headers":{"Authorization":"Bearer fake-bearer-0005","X-Org":"fake-org-0006"},"transport":"sse"},"server_origin":"member","trusted_tools":["lookup"]}`)

	if _, err := schema.UpTo(ctx, pool, sealVersion, testKeys); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	// The enabled provider rows are native connections now, keys sealed;
	// the oldest Exa key won.
	keySeal, _ := sealedsecret.New(testKeys.SecretKey, sealedsecret.WebSearchKey)
	rows, err := pool.Query(ctx, `SELECT provider, api_key, key_mask FROM web_search_connections WHERE user_id = $1 ORDER BY provider`, sealOwner)
	if err != nil {
		t.Fatal(err)
	}
	opened := map[string]string{}
	for rows.Next() {
		var provider, sealed, mask string
		if err := rows.Scan(&provider, &sealed, &mask); err != nil {
			t.Fatal(err)
		}
		key, err := keySeal.Open(sealOwner, sealed)
		if err != nil {
			t.Fatalf("%s: %v", provider, err)
		}
		opened[provider] = key + " " + mask
	}
	rows.Close()
	if opened["exa"] != "fake-exa-key-0001 ••••0001" || opened["tavily"] != "fake-tavily-key-0002 ••••0002" || len(opened) != 2 {
		t.Fatalf("web search connections = %v", opened)
	}

	names := column[string](t, pool, `SELECT name FROM search_source_connectors WHERE connector_type = 'MCP_CONNECTOR' ORDER BY name`)
	if strings.Join(names, ",") != "Exa switched off,Tools" {
		t.Fatalf("MCP servers left = %v", names)
	}
	// No secret of any server is left in the clear.
	everything := strings.Join(column[string](t, pool, `SELECT config::text FROM search_source_connectors`), "\n")
	for _, secret := range []string{"fake-exa-key-off-03", "fake-url-key-0004", "fake-bearer-0005", "fake-org-0006"} {
		if strings.Contains(everything, secret) {
			t.Errorf("%s is still stored in the clear", secret)
		}
	}

	// And each opens back to what was typed, the rest of the row untouched.
	serverSeal, _ := sealedsecret.New(testKeys.SecretKey, sealedsecret.MCPServerSecret)
	raw := column[[]byte](t, pool, `SELECT config -> 'server_config' FROM search_source_connectors WHERE name = 'Tools'`)[0]
	stored, err := mcpserver.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	target, err := mcpserver.Open(serverSeal, sealOwner, stored, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Auth.Type != mcpserver.AuthBearer || stored.Transport != mcpserver.SSE ||
		target.Address != "https://tools.example.com/mcp?api_key=fake-url-key-0004&region=eu" ||
		target.Headers["Authorization"] != "Bearer fake-bearer-0005" || target.Headers["X-Org"] != "fake-org-0006" ||
		stored.URL != "https://tools.example.com/mcp?api_key=••••0004&region=eu" {
		t.Fatalf("stored = %+v, opened = %+v", stored, target)
	}
	if trusted := column[string](t, pool, `SELECT config ->> 'trusted_tools' FROM search_source_connectors WHERE name = 'Tools'`); trusted[0] != `["lookup"]` {
		t.Errorf("trusted tools = %v", trusted)
	}
	switchedOff, _ := mcpserver.Parse(column[[]byte](t, pool, `SELECT config -> 'server_config' FROM search_source_connectors WHERE name = 'Exa switched off'`)[0])
	if switchedOff.Transport != mcpserver.StreamableHTTP || switchedOff.Headers["x-api-key"].Mask != "••••f-03" {
		t.Errorf("the switched-off server = %+v", switchedOff)
	}

	// The way back (development databases only) writes them in the clear
	// again and gives the providers their MCP servers back.
	if _, err := schema.DownTo(ctx, pool, sealBeforeVersion, testKeys); err != nil {
		t.Fatalf("rolling back: %v", err)
	}
	back := column[string](t, pool, `SELECT config -> 'server_config' ->> 'url' FROM search_source_connectors WHERE name = 'Tools'`)
	header := column[string](t, pool, `SELECT config -> 'server_config' -> 'headers' ->> 'Authorization' FROM search_source_connectors WHERE name = 'Tools'`)
	if back[0] != "https://tools.example.com/mcp?api_key=fake-url-key-0004&region=eu" || header[0] != "Bearer fake-bearer-0005" {
		t.Errorf("rolled back to %v, %v", back, header)
	}
	if restored := column[string](t, pool, `SELECT config -> 'server_config' -> 'headers' ->> 'x-api-key' FROM search_source_connectors WHERE name = 'Exa'`); len(restored) != 1 || restored[0] != "fake-exa-key-0001" {
		t.Errorf("the Exa server came back as %v", restored)
	}
}

func TestTheCredentialMigrationStopsAtARowItCannotRead(t *testing.T) {
	ctx := context.Background()
	for name, config := range map[string]string{
		"a local server":  `{"server_config":{"command":"npx","args":["server"]}}`,
		"no address":      `{"server_config":{"headers":{"X":"y"}}}`,
		"a header number": `{"server_config":{"url":"https://a.example/mcp","headers":{"X":1}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			pool := beforeSealing(t)
			addServer(t, pool, "Broken", true, config)
			_, err := schema.Migrate(ctx, pool, testKeys)
			if err == nil || !strings.Contains(err.Error(), "MCP connector") {
				t.Fatalf("err = %v, want the row named", err)
			}
			// Nothing of the run is kept: the row is as it was.
			if kept := column[string](t, pool, `SELECT config::text FROM search_source_connectors`); len(kept) != 1 || strings.Contains(kept[0], "sealed") {
				t.Errorf("the row was changed: %v", kept)
			}
		})
	}
}

func TestTheCredentialMigrationNeedsTheSecret(t *testing.T) {
	pool := beforeSealing(t)
	if _, err := schema.Migrate(context.Background(), pool, testKeysNone); err == nil || !strings.Contains(err.Error(), "secret key") {
		t.Fatalf("err = %v", err)
	}
}
