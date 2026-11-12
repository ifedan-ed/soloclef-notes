package mcptools

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// trusting is an account with two servers that both have a "search", so
// that tool's model-facing name is not its own.
func trusting(t *testing.T) *world {
	t.Helper()
	rows := newRows()
	rows.add(ada, 3, "Notes", serverConfig("https://notes.example.com/mcp", "k",
		cachedList("search", "create_note", "fetch_page", "odd\nname"), `"trusted_tools": ["create_note", "fetch_page", 5]`))
	rows.add(ada, 5, "Wiki", serverConfig("https://wiki.example.com/mcp", "k", cachedList("search")))
	return newWorld(t, rows, newCounter(), nil)
}

func TestTrustIsReadFromTheRowAtEachDecision(t *testing.T) {
	w := trusting(t)
	ctx := context.Background()
	set := w.turn(t, ada)
	trusted := func(name string) bool {
		t.Helper()
		ok, mcp, err := set.Trusted(ctx, name)
		if err != nil || !mcp {
			t.Fatalf("Trusted(%s) = %v, %v, %v", name, ok, mcp, err)
		}
		return ok
	}

	if !trusted("create_note") {
		t.Fatal("a tool on the trusted list asks")
	}
	// Untrusted on the settings page, by another process, while this turn
	// is open. No ForTurn, no bump: the very next decision asks.
	w.rows.add(ada, 3, "Notes", serverConfig("https://notes.example.com/mcp", "k", `"trusted_tools": []`))
	if trusted("create_note") {
		t.Error("a tool taken off the trusted list still runs unasked")
	}
	// And trusted again the same way.
	w.rows.add(ada, 3, "Notes", serverConfig("https://notes.example.com/mcp", "k", `"trusted_tools": ["create_note", "search"]`))
	if !trusted("create_note") {
		t.Error("a tool put on the trusted list still asks")
	}
	// By connection and the server's own name: server 3's "search" is
	// trusted, server 5's is not, whatever the model calls them.
	if !trusted("mcp_3_search") || trusted("mcp_5_search") {
		t.Error("trust followed the name instead of the connection")
	}
	// The server is removed: nothing of it is trusted.
	w.rows.remove(ada, 3)
	if trusted("create_note") {
		t.Error("a removed server's tool is trusted")
	}
	// One read per decision, and never the list of connections.
	if lists, reads := w.rows.counts(); lists != 1 || reads != 6 {
		t.Errorf("%d lists and %d row reads for six decisions", lists, reads)
	}
}

func TestWhatTrustNeverCovers(t *testing.T) {
	w := trusting(t)
	ctx := context.Background()
	set := w.turn(t, ada)

	// On the stored list, and it fetches an address: it asks regardless,
	// without the row even being read.
	_, before := w.rows.counts()
	if ok, mcp, err := set.Trusted(ctx, "fetch_page"); ok || !mcp || err != nil {
		t.Errorf("a tool that always asks: %v, %v, %v", ok, mcp, err)
	}
	if _, after := w.rows.counts(); after != before {
		t.Error("the row was read to decide a tool that always asks")
	}
	// Not an MCP tool: the caller is told to ask whoever owns it.
	if ok, mcp, err := set.Trusted(ctx, "search_knowledge_base"); ok || mcp || err != nil {
		t.Errorf("the chat's own tool: %v, %v, %v", ok, mcp, err)
	}
	// "search" is no tool's model-facing name here; both were renamed.
	if _, mcp, _ := set.Trusted(ctx, "search"); mcp {
		t.Error("a name the model was never given is treated as an MCP tool")
	}
	// Another account has none of Ada's tools at all: to Ben's turn the
	// name is not an MCP tool, so nothing of hers is trusted for him.
	if ok, mcp, err := w.turn(t, ben).Trusted(ctx, "create_note"); ok || mcp || err != nil {
		t.Errorf("another account: %v, %v, %v", ok, mcp, err)
	}
	// The same account, spelled in capitals by its token.
	if ok, _, _ := w.turn(t, "AAAAAAAA-0000-4000-8000-000000000001").Trusted(ctx, "create_note"); !ok {
		t.Error("the owner is not recognised when the id's case differs")
	}
	// A database that cannot be read is an error, never a yes.
	w.rows.fail = errors.New("connection reset")
	if ok, mcp, err := set.Trusted(ctx, "create_note"); ok || !mcp || err == nil {
		t.Errorf("with the database down: %v, %v, %v", ok, mcp, err)
	}
}

func TestAlwaysAllowIsWrittenToTheConnectionAndRefusedWhereItMust(t *testing.T) {
	w := trusting(t)
	ctx := context.Background()
	set := w.turn(t, ada)

	if can, err := set.CanTrust(ctx, "mcp_3_search"); !can || err != nil {
		t.Fatalf("CanTrust = %v, %v", can, err)
	}
	if err := set.Trust(ctx, "mcp_3_search"); err != nil {
		t.Fatal(err)
	}
	// Stored where the settings page lists and removes it: on the
	// connection, under the server's own name, after what was there. The
	// number that was in the list is gone, as it is whenever the Python
	// tier rewrites it.
	if got := readTrusted(w.rows.connections[ada][3].Config); !reflect.DeepEqual(got, []string{"create_note", "fetch_page", "search"}) {
		t.Errorf("trusted_tools = %v", got)
	}
	if got := readTrusted(w.rows.connections[ada][5].Config); len(got) != 0 {
		t.Errorf("the other server's list was written: %v", got)
	}
	// Honoured at the next decision, with nothing invalidated.
	if ok, _, _ := set.Trusted(ctx, "mcp_3_search"); !ok {
		t.Error("a tool just trusted still asks")
	}
	if w.counter.at(ada) != 0 {
		t.Error("trusting a tool bumped the counter; trust is not cached")
	}
	// Twice is once.
	if err := set.Trust(ctx, "mcp_3_search"); err != nil {
		t.Fatal(err)
	}
	if got := readTrusted(w.rows.connections[ada][3].Config); len(got) != 3 {
		t.Errorf("trusted twice, listed %v", got)
	}

	for label, refusal := range map[string]struct {
		set  *Set
		name string
		want error
	}{
		"a tool that always asks":        {set, "fetch_page", ErrAlwaysAsks},
		"a name that cannot be saved":    {set, "mcp_3_odd_name", ErrUnstorable},
		"a tool that is not an MCP tool": {set, "search_knowledge_base", ErrNotMCP},
		"another account's tool":         {w.turn(t, ben), "create_note", ErrNotMCP},
	} {
		if err := refusal.set.Trust(ctx, refusal.name); !errors.Is(err, refusal.want) {
			t.Errorf("%s: Trust = %v, want %v", label, err, refusal.want)
		}
		if can, err := refusal.set.CanTrust(ctx, refusal.name); can || err != nil {
			t.Errorf("%s: the card would offer always allow (%v, %v)", label, can, err)
		}
	}
	// The unsaveable tool is still offered and can still be called with an
	// approval each time; only trusting it is refused.
	if info, found := set.Lookup("mcp_3_odd_name"); !found || info.Tool != "odd\nname" {
		t.Errorf("the tool with the odd name: %+v, %v", info, found)
	}
	// A server removed since the turn was built.
	w.rows.remove(ada, 5)
	if err := set.Trust(ctx, "mcp_5_search"); !errors.Is(err, ErrGone) {
		t.Errorf("trusting a removed server's tool: %v", err)
	}
}
