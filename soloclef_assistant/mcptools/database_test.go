package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The two opt-in tests below ask real servers what the rest of the package
// asks stand-ins. Each is skipped unless its variable names a throwaway
// server; neither must ever be pointed at one anybody uses, because both
// write.
//
// The database has its own variable rather than the chat package's
// SOLOCLEF_API_TEST_CHAT_DATABASE: that package's test truncates the tables
// this one seeds, and `go test ./...` runs the two at the same time. It is
// a pgvector/pgvector:pg17 brought to the schema by soloclef_schema's
// `migrate up`; the Valkey is a valkey/valkey:8-alpine.
//
//	SOLOCLEF_API_TEST_MCPTOOLS_DATABASE=postgres://check:check@127.0.0.1:39827/check \
//	SOLOCLEF_API_TEST_VALKEY_URL=redis://127.0.0.1:39828/0 \
//	  go test soloclef_assistant/mcptools/ -run 'OverAReal' -count=1 -v
const (
	databaseVariable = "SOLOCLEF_API_TEST_MCPTOOLS_DATABASE"
	valkeyVariable   = "SOLOCLEF_API_TEST_VALKEY_URL"
)

// Ada has four connectors that are switched on (three MCP servers, one of
// them with a config that is not an object, and a crawler) and one MCP
// server she switched off. Ben has one MCP server. Everything asked as Ben
// about Ada's rows, and as Ada about Ben's, must find nothing.
const seed = `
TRUNCATE "user", site_settings CASCADE;
INSERT INTO "user" (id, email) VALUES
  ('aaaaaaaa-0000-4000-8000-000000000001', 'ada@example.org'),
  ('bbbbbbbb-0000-4000-8000-000000000002', 'ben@example.org');
INSERT INTO search_source_connectors (id, name, connector_type, config, user_id, enabled) VALUES
  (12, 'Wiki', 'MCP_CONNECTOR', '{"server_config": {"transport": "http", "url": "https://wiki.example.com/mcp"}, "server_origin": "member"}', 'aaaaaaaa-0000-4000-8000-000000000001', true),
  (11, 'Notes', 'MCP_CONNECTOR', '{"server_config": {"transport": "streamable-http", "url": "https://127.0.0.1:1/mcp?apiKey=k", "headers": {"x-api-key": "k"}}, "server_origin": "member", "trusted_tools": ["create_note", 5], "kept": {"as": ["it", "was"]}}', 'aaaaaaaa-0000-4000-8000-000000000001', true),
  (13, 'Not an MCP server', 'WEBCRAWLER_CONNECTOR', '{"server_config": {"transport": "http", "url": "https://x.example.com/"}}', 'aaaaaaaa-0000-4000-8000-000000000001', true),
  (14, 'A list for a config', 'MCP_CONNECTOR', '["server_config"]', 'aaaaaaaa-0000-4000-8000-000000000001', true),
  (15, 'Switched off', 'MCP_CONNECTOR', '{"server_config": {"transport": "http", "url": "https://off.example.com/mcp"}, "trusted_tools": ["lookup"]}', 'aaaaaaaa-0000-4000-8000-000000000001', false),
  (20, 'Elsewhere', 'MCP_CONNECTOR', '{"server_config": {"transport": "http", "url": "https://else.example.com/mcp"}}', 'bbbbbbbb-0000-4000-8000-000000000002', true);
`

func TestTheRowsOverARealDatabase(t *testing.T) {
	address := os.Getenv(databaseVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, seed); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	rows := Postgres{Pool: pool}

	t.Run("an account's MCP connections that are switched on, oldest first, and nobody else's", func(t *testing.T) {
		listed, err := rows.Connections(ctx, ada)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, row := range listed {
			ids = append(ids, row.ID)
			if row.SiteHosts != nil {
				t.Errorf("connection %d has site hosts %s though none were saved", row.ID, row.SiteHosts)
			}
		}
		if !reflect.DeepEqual(ids, []int64{11, 12, 14}) {
			t.Errorf("connections = %v", ids)
		}
		if where, err := readTarget(listed[0].Config); err != nil || where.headers["x-api-key"] != "k" {
			t.Errorf("the first connection's target: %+v, %v", where, err)
		}
	})

	t.Run("one connection, as it is now, to its own account only", func(t *testing.T) {
		row, err := rows.Connection(ctx, ada, 11)
		if err != nil || row == nil || row.Name != "Notes" {
			t.Fatalf("connection 11 = %+v, %v", row, err)
		}
		// The same account, spelled in capitals by its token.
		if row, err := rows.Connection(ctx, "AAAAAAAA-0000-4000-8000-000000000001", 11); err != nil || row == nil {
			t.Errorf("the account is not recognised when the id's case differs: %+v, %v", row, err)
		}
		for label, ask := range map[string]struct {
			account string
			id      int64
		}{
			"Ben's, asked as Ada": {ada, 20}, "Ada's, asked as Ben": {ben, 11},
			"not an MCP server": {ada, 13}, "switched off": {ada, 15}, "absent": {ada, 404},
		} {
			if row, err := rows.Connection(ctx, ask.account, ask.id); row != nil || err != nil {
				t.Errorf("%s: %+v, %v", label, row, err)
			}
		}
		// Ben's list is Ben's one server.
		if listed, err := rows.Connections(ctx, ben); err != nil || len(listed) != 1 || listed[0].ID != 20 {
			t.Errorf("Ben's connections = %+v, %v", listed, err)
		}
	})

	t.Run("the operator's list is read beside the row", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `INSERT INTO site_settings (key, value) VALUES ('mcp_allowed_hosts', '["mcp.exa.ai"]')`); err != nil {
			t.Fatal(err)
		}
		row, _ := rows.Connection(ctx, ada, 11)
		if hosts, stored := readSiteHosts(row.SiteHosts); !stored || !reflect.DeepEqual(hosts, []string{"mcp.exa.ai"}) {
			t.Errorf("site hosts = %v, %v (%s)", hosts, stored, row.SiteHosts)
		}
		listed, _ := rows.Connections(ctx, ada)
		if hosts, stored := readSiteHosts(listed[0].SiteHosts); !stored || len(hosts) != 1 {
			t.Errorf("the list's site hosts = %v, %v", hosts, stored)
		}
	})

	t.Run("always allow is added once, by the owner, and loses nothing", func(t *testing.T) {
		if found, err := rows.AddTrusted(ctx, ben, 11, "search"); found || err != nil {
			t.Fatalf("another account trusted a tool of Ada's: %v, %v", found, err)
		}
		if row, _ := rows.Connection(ctx, ada, 11); len(readTrusted(row.Config)) != 1 {
			t.Fatalf("Ben's attempt changed Ada's list: %s", row.Config)
		}
		// Twelve approvals at once, two of them the same name. Every name
		// is in the list afterwards and none twice: the row is locked
		// while the list is read and written.
		var together sync.WaitGroup
		for i := range 12 {
			together.Add(1)
			go func() {
				defer together.Done()
				if owned, err := rows.AddTrusted(ctx, ada, 11, fmt.Sprintf("tool_%d", i%11)); !owned || err != nil {
					t.Errorf("AddTrusted: %v, %v", owned, err)
				}
			}()
		}
		together.Wait()
		row, _ := rows.Connection(ctx, ada, 11)
		trusted := readTrusted(row.Config)
		seen := map[string]bool{}
		for _, name := range trusted {
			if seen[name] {
				t.Errorf("%s is listed twice", name)
			}
			seen[name] = true
		}
		if len(trusted) != 12 || trusted[0] != "create_note" {
			t.Errorf("trusted_tools = %v", trusted)
		}
		// The rest of the config is as it was.
		top := keys(row.Config)
		var kept map[string][]string
		if json.Unmarshal(top["kept"], &kept) != nil || !reflect.DeepEqual(kept["as"], []string{"it", "was"}) {
			t.Errorf("another key was changed: %s", top["kept"])
		}
		if where, err := readTarget(row.Config); err != nil || where.origin != originMember || where.headers["x-api-key"] != "k" {
			t.Errorf("the server's address or key was changed: %+v, %v", where, err)
		}
		// A config that is not an object cannot take a list; that is an
		// error, not a quiet nothing.
		if owned, err := rows.AddTrusted(ctx, ada, 14, "search"); owned || err == nil {
			t.Errorf("a list for a config took a trusted tool: %v, %v", owned, err)
		}
	})

	t.Run("a discovered list is saved where both tiers read it", func(t *testing.T) {
		cached, err := writeCached([]definition{{name: "lookup", description: "Finds.", schema: emptySchema}}, "wiki", "", "streamable-http", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := rows.StoreDefinitions(ctx, ada, 12, cached); err != nil {
			t.Fatal(err)
		}
		row, _ := rows.Connection(ctx, ada, 12)
		if tools, found := readCached(row.Config); !found || len(tools) != 1 || tools[0].name != "lookup" {
			t.Errorf("read back %v, %v", tools, found)
		}
	})

	t.Run("a refused key is flagged the first time only", func(t *testing.T) {
		for attempt, want := range []bool{true, false} {
			if changed, err := rows.MarkAuthExpired(ctx, ada, 12); changed != want || err != nil {
				t.Errorf("attempt %d: changed = %v, %v", attempt, changed, err)
			}
		}
		row, _ := rows.Connection(ctx, ada, 12)
		if string(keys(row.Config)["auth_expired"]) != "true" {
			t.Errorf("config = %s", row.Config)
		}
		if changed, _ := rows.MarkAuthExpired(ctx, ben, 20); !changed {
			t.Error("Ben's own connection could not be flagged")
		}
		if _, err := pool.Exec(ctx, `UPDATE search_source_connectors SET config = config - 'auth_expired' WHERE id = 12`); err != nil {
			t.Fatal(err)
		}
		if changed, _ := rows.MarkAuthExpired(ctx, ben, 12); changed {
			t.Error("Ben flagged a connection of Ada's")
		}
		if err := rows.StoreDefinitions(ctx, ben, 12, []byte(`{"tools":[]}`)); err != nil {
			t.Fatal(err)
		}
		if row, _ := rows.Connection(ctx, ada, 12); string(keys(row.Config)["auth_expired"]) == "true" || len(mustCached(t, row.Config)) != 1 {
			t.Errorf("Ben's writes reached Ada's connection: %s", row.Config)
		}
	})

	t.Run("a turn over the real rows", func(t *testing.T) {
		source, err := NewSource(Settings{
			Rows: rows, Generations: newCounter(), Network: loopback{}, Logger: slog.New(slog.DiscardHandler), Now: time.Now,
			ClientName: "chat-under-test", SiteHostsWhenUnset: []string{}, Limits: testLimits(),
		})
		if err != nil {
			t.Fatal(err)
		}
		set, err := source.ForTurn(ctx, ada)
		if err != nil {
			t.Fatal(err)
		}
		defer set.Close()
		// Connection 11 has no saved list and nothing listens at its address;
		// 12 has the list saved above; 14's config is unreadable.
		if got := names(set); !reflect.DeepEqual(got, []string{"lookup"}) {
			t.Fatalf("tools = %v", got)
		}
		if err := set.Trust(ctx, "lookup"); err != nil {
			t.Fatal(err)
		}
		if trusted, mcp, err := set.Trusted(ctx, "lookup"); !trusted || !mcp || err != nil {
			t.Errorf("Trusted = %v, %v, %v", trusted, mcp, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM search_source_connectors WHERE id = 12`); err != nil {
			t.Fatal(err)
		}
		if trusted, _, _ := set.Trusted(ctx, "lookup"); trusted {
			t.Error("a deleted connection's tool is trusted")
		}
	})
}

// Two Valkey values are two processes. What one bumps the other reads, and
// a server that cannot be reached is an error from Current, which is what
// makes a turn build from the database and cache nothing.
func TestTheCounterOverARealValkey(t *testing.T) {
	address := os.Getenv(valkeyVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Valkey", valkeyVariable)
	}
	ctx := context.Background()
	first, err := NewValkey(address)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewValkey(address)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	// An account no other run has used.
	account := fmt.Sprintf("dddddddd-0000-4000-8000-%012d", time.Now().UnixNano()%1_000_000_000_000)
	other := "eeeeeeee" + account[8:]
	if n, err := first.Current(ctx, account); n != 0 || err != nil {
		t.Fatalf("an account nobody bumped is at %d, %v", n, err)
	}
	for want := int64(1); want <= 3; want++ {
		if err := first.Bump(ctx, account); err != nil {
			t.Fatal(err)
		}
		if n, err := second.Current(ctx, account); n != want || err != nil {
			t.Fatalf("the other process reads %d, %v; want %d", n, err, want)
		}
	}
	if n, _ := second.Current(ctx, other); n != 0 {
		t.Errorf("another account moved to %d", n)
	}

	// The whole path: two Sources, one database of rows, this Valkey.
	rows := newRows()
	rows.add(account, 1, "Notes", serverConfig("https://notes.example.com/mcp", "k", cachedList("create_note")))
	build := func(generations Generations) *Source {
		source, err := NewSource(Settings{
			Rows: rows, Generations: generations, Network: loopback{}, Logger: slog.New(slog.DiscardHandler), Now: time.Now,
			ClientName: "chat-under-test", SiteHostsWhenUnset: []string{}, Limits: testLimits(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return source
	}
	one, two := build(first), build(second)
	tools := func(source *Source) []string {
		set, err := source.ForTurn(ctx, account)
		if err != nil {
			t.Fatal(err)
		}
		defer set.Close()
		return names(set)
	}
	tools(one)
	tools(one)
	if lists, _ := rows.counts(); lists != 1 {
		t.Fatalf("listed %d times for two turns", lists)
	}
	rows.add(account, 2, "Wiki", serverConfig("https://wiki.example.com/mcp", "k", cachedList("lookup")))
	if err := two.Bump(ctx, account); err != nil {
		t.Fatal(err)
	}
	if got := tools(one); !reflect.DeepEqual(got, []string{"create_note", "lookup"}) {
		t.Errorf("after the other process's bump, tools = %v", got)
	}

	// A Valkey that is not there.
	absent, err := NewValkey("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatal(err)
	}
	defer absent.Close()
	if _, err := absent.Current(ctx, account); err == nil {
		t.Error("an unreachable Valkey answered")
	}
	before, _ := rows.counts()
	down := build(absent)
	tools(down)
	tools(down)
	if after, _ := rows.counts(); after != before+2 {
		t.Errorf("with Valkey unreachable, two turns listed the rows %d times, want 2", after-before)
	}
	if _, err := NewValkey("not a url"); err == nil {
		t.Error("a bad address was accepted")
	}
}

// mustCached is the tool list saved on a connection's config.
func mustCached(t *testing.T, config []byte) []definition {
	t.Helper()
	tools, found := readCached(config)
	if !found {
		t.Fatalf("no saved tool list in %s", config)
	}
	return tools
}

// The key is a contract with every writer that bumps it: the account's uuid,
// in lower case whatever case the caller's token carried it in.
func TestTheGenerationKeyIsTheAccountsUUIDInLowerCase(t *testing.T) {
	if got := GenerationKey("AAAAAAAA-0000-4000-8000-000000000001"); got != "soloclef:mcp:generation:aaaaaaaa-0000-4000-8000-000000000001" {
		t.Errorf("the key is %q", got)
	}
	if GenerationKey(ada) == GenerationKey(ben) {
		t.Error("two accounts share a counter")
	}
}
