package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// benchWorld is an account with five servers of twenty tools each, every
// list saved on its row: the shape of a well-used account on a turn that
// has to ask no server anything.
func benchWorld(b *testing.B) *world {
	rows := newRows()
	for server := int64(1); server <= 5; server++ {
		var toolNames []string
		for i := range 20 {
			// A third of the names are shared between servers, so the
			// naming rules have their collisions to settle.
			if i%3 == 0 {
				toolNames = append(toolNames, fmt.Sprintf("shared_%d", i))
			} else {
				toolNames = append(toolNames, fmt.Sprintf("s%d_tool_%d", server, i))
			}
		}
		rows.add(ada, server, fmt.Sprintf("Server %d", server), serverConfig("https://s.example.com/mcp", "k", cachedList(toolNames...)))
	}
	return newWorld(b, rows, newCounter(), nil)
}

// A turn that finds its account in the cache: one read of the counter and
// a map lookup. This is every turn but the first after a change.
func BenchmarkATurnFromAWarmCache(b *testing.B) {
	w := benchWorld(b)
	ctx := context.Background()
	if _, err := w.source.ForTurn(ctx, ada); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		set, err := w.source.ForTurn(ctx, ada)
		if err != nil {
			b.Fatal(err)
		}
		set.Close()
	}
}

// The same turn with the hundred tool values the loop is handed made too,
// which is what the assembly actually does each turn.
func BenchmarkATurnFromAWarmCacheWithItsTools(b *testing.B) {
	w := benchWorld(b)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		set, err := w.source.ForTurn(ctx, ada)
		if err != nil {
			b.Fatal(err)
		}
		if len(set.Tools()) != 100 {
			b.Fatal("not a hundred tools")
		}
		set.Close()
	}
}

// A turn that has to build: the counter moved, or the entry expired, or
// Valkey is down. Five configs are parsed and a hundred names settled. The
// rows here are in memory; a real build adds one query's round trip.
func BenchmarkATurnBuiltCold(b *testing.B) {
	w := benchWorld(b)
	ctx := context.Background()
	w.counter.setDown(true) // nothing is cached, so every turn builds
	w.source.logger = slogDiscard()
	b.ReportAllocs()
	for b.Loop() {
		set, err := w.source.ForTurn(ctx, ada)
		if err != nil {
			b.Fatal(err)
		}
		set.Close()
	}
}

// A trust decision: one row read and a small list parsed.
func BenchmarkATrustDecision(b *testing.B) {
	w := benchWorld(b)
	ctx := context.Background()
	set, err := w.source.ForTurn(ctx, ada)
	if err != nil {
		b.Fatal(err)
	}
	defer set.Close()
	b.ReportAllocs()
	for b.Loop() {
		if _, mcp, err := set.Trusted(ctx, "s1_tool_1"); !mcp || err != nil {
			b.Fatal(mcp, err)
		}
	}
}

// calling is an account connected to a real MCP server on loopback, and
// the echo tool's Run.
func calling(b *testing.B) (*world, func(*Set) tool.Func) {
	server := newRemote(b, 0)
	server.add("echo", func(json.RawMessage) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}
	})
	rows := newRows()
	rows.add(ada, 1, "Notes", serverConfig(server.URL, "k", cachedList("echo")))
	return newWorld(b, rows, newCounter(), nil), func(set *Set) tool.Func { return set.Tools()[0].Run }
}

// A call on the connection the turn already has: the row read, then one
// request. This is every call of a turn after its first to that server.
func BenchmarkACallOnTheTurnsConnection(b *testing.B) {
	w, run := calling(b)
	ctx := context.Background()
	set, err := w.source.ForTurn(ctx, ada)
	if err != nil {
		b.Fatal(err)
	}
	defer set.Close()
	echo := run(set)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := echo(ctx, tool.Call{Args: json.RawMessage(`{"q":"x"}`)}); err != nil {
			b.Fatal(err)
		}
	}
}

// A call that connects, initialises, calls and closes, which is what every
// call cost before connections were kept for the turn. On loopback the
// difference is requests; against a real server each is a round trip over
// the internet, and the first also a TLS handshake.
func BenchmarkACallThatOpensItsOwnConnection(b *testing.B) {
	w, run := calling(b)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		set, err := w.source.ForTurn(ctx, ada)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := run(set)(ctx, tool.Call{Args: json.RawMessage(`{"q":"x"}`)}); err != nil {
			b.Fatal(err)
		}
		set.Close()
	}
}

// The two turn benchmarks again with the real servers under them, for what
// a turn costs in a deployment: a warm turn is one GET, a cold one is that
// GET and one query. Skipped unless both of database_test.go's variables
// are set.
func BenchmarkATurnOverARealDatabaseAndValkey(b *testing.B) {
	database, counter := os.Getenv(databaseVariable), os.Getenv(valkeyVariable)
	if database == "" || counter == "" {
		b.Skipf("%s and %s are not both set", databaseVariable, valkeyVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, database)
	if err != nil {
		b.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, seed); err != nil {
		b.Fatal(err)
	}
	// Five servers of twenty tools, as in benchWorld, as real rows.
	memory := benchWorld(b).rows
	// As Ben's, whose one seeded server is removed first: the turn below
	// counts exactly these hundred tools.
	if _, err := pool.Exec(ctx, `DELETE FROM search_source_connectors WHERE user_id = $1`, ben); err != nil {
		b.Fatal(err)
	}
	for id, row := range memory.connections[ada] {
		if _, err := pool.Exec(ctx, `INSERT INTO search_source_connectors (id, name, connector_type, config, user_id)
			VALUES ($1, $2, 'MCP_CONNECTOR', $3::jsonb, $4)`, 100+id, row.Name, string(row.Config), ben); err != nil {
			b.Fatal(err)
		}
	}
	valkey, err := NewValkey(counter)
	if err != nil {
		b.Fatal(err)
	}
	defer valkey.Close()
	source, err := NewSource(Settings{
		Rows: Postgres{Pool: pool}, Generations: valkey, Network: loopback{}, Logger: slogDiscard(), Now: time.Now,
		ClientName: "chat-under-test", SiteHostsWhenUnset: []string{}, Limits: testLimits(),
	})
	if err != nil {
		b.Fatal(err)
	}
	turn := func(b *testing.B) {
		set, err := source.ForTurn(ctx, ben)
		if err != nil {
			b.Fatal(err)
		}
		if len(set.Tools()) != 100 {
			b.Fatalf("%d tools", len(set.Tools()))
		}
		set.Close()
	}
	b.Run("warm", func(b *testing.B) {
		turn(b)
		for b.Loop() {
			turn(b)
		}
	})
	b.Run("cold", func(b *testing.B) {
		for b.Loop() {
			// Another process's change, so the next turn rebuilds.
			if err := source.Bump(ctx, ben); err != nil {
				b.Fatal(err)
			}
			turn(b)
		}
	})
}
