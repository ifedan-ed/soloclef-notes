package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

func TestSettingsAreAllSaidOrTheSourceIsNotBuilt(t *testing.T) {
	_, err := NewSource(Settings{})
	if err == nil {
		t.Fatal("a Source was built from nothing")
	}
	for _, name := range []string{"Rows", "Generations", "Network", "Logger", "Now", "ClientName", "SiteHostsWhenUnset", "ToolsPerServer", "Discovery", "Call", "Lifetime", "RetryIncomplete", "Accounts", "ResultBytes"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal does not name %s: %v", name, err)
		}
	}
	// The production Network is checked with the rest.
	w := newWorld(t, newRows(), newCounter(), nil)
	settings := Settings{
		Rows: newRows(), Generations: newCounter(), Network: PublicInternet{}, Logger: w.source.logger, Now: time.Now,
		ClientName: "x", SiteHostsWhenUnset: []string{}, Limits: testLimits(),
	}
	if _, err := NewSource(settings); err == nil || !strings.Contains(err.Error(), "Resolve") {
		t.Errorf("a PublicInternet with no resolver was accepted: %v", err)
	}
	settings.Limits.RetryIncomplete = settings.Limits.Lifetime + time.Second
	settings.Network = loopback{}
	if _, err := NewSource(settings); err == nil {
		t.Error("a retry time longer than the lifetime was accepted")
	}
	if _, err := w.source.ForTurn(context.Background(), ""); err == nil {
		t.Error("a turn for nobody was built")
	}
}

func TestToolsComeFromTheSavedListWithoutAskingTheServer(t *testing.T) {
	server := newRemote(t, 0)
	rows := newRows()
	rows.add(ada, 1, "Notes", serverConfig(server.URL, "k", cachedList("create_note", "navigate")))
	rows.add(ada, 2, "Local", `{"server_config": {"transport": "stdio", "command": "npx"}, `+cachedList("run_anything")+`}`)
	rows.add(ben, 3, "Elsewhere", serverConfig(server.URL, "k", cachedList("other_account")))
	w := newWorld(t, rows, newCounter(), nil)

	set := w.turn(t, ada)
	if got := names(set); !reflect.DeepEqual(got, []string{"create_note", "navigate"}) {
		t.Fatalf("tools = %v", got)
	}
	if n := server.requests.Load(); n != 0 {
		t.Errorf("the server was asked %d times though its list is saved", n)
	}
	for _, tl := range set.Tools() {
		if err := tl.Validate(); err != nil {
			t.Errorf("%s is not a tool the loop accepts: %v", tl.Name, err)
		}
		if tl.Parallel {
			t.Errorf("%s may run beside other calls", tl.Name)
		}
		want := tool.Ask
		if tl.Name == "navigate" {
			want = tool.AlwaysAsk
		}
		if tl.Approval != want {
			t.Errorf("%s: approval = %v, want %v", tl.Name, tl.Approval, want)
		}
		if !strings.HasPrefix(tl.Description, "[MCP server: Notes] Does ") {
			t.Errorf("%s: description = %q", tl.Name, tl.Description)
		}
	}
	info, found := set.Lookup("navigate")
	if !found || info.ConnectorID != 1 || info.Server != "Notes" || info.Tool != "navigate" || !info.AlwaysAsk {
		t.Errorf("Lookup = %+v, %v", info, found)
	}
	if _, found := set.Lookup("search_knowledge_base"); found {
		t.Error("the chat's own tool is described as an MCP tool")
	}
}

func TestALiveListIsFollowedToItsLastPageAndSaved(t *testing.T) {
	server := newRemote(t, 3) // eight tools, three to a page
	var want []string
	for i := range 8 {
		name := fmt.Sprintf("tool_%d", i)
		server.add(name, says("ok"))
		want = append(want, name)
	}
	rows, shared := newRows(), newCounter()
	rows.add(ada, 1, "Paged", serverConfig(server.URL, "page-key"))
	w := newWorld(t, rows, shared, nil)

	set := w.turn(t, ada)
	if got := names(set); !reflect.DeepEqual(got, want) {
		t.Fatalf("tools = %v, want all eight", got)
	}
	if pages := server.method("tools/list"); pages != 3 {
		t.Errorf("tools/list was asked %d times, want 3 pages", pages)
	}
	if !server.sent("x-api-key", "page-key") {
		t.Error("the connection's header was not sent")
	}
	// The list is now on the row, in the shape the Python tier reads.
	var saved struct {
		DiscoveredAt string `json:"discovered_at"`
		ServerName   string `json:"server_name"`
		Version      string `json:"server_version"`
		Transport    string `json:"transport"`
		Tools        []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"input_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(rows.config(ada, 1)[keyCached], &saved); err != nil {
		t.Fatalf("cached_tools: %v", err)
	}
	if len(saved.Tools) != 8 || saved.Tools[7].Name != "tool_7" || saved.Tools[0].InputSchema["type"] != "object" ||
		saved.ServerName != "remote-under-test" || saved.Version != "9.9" || saved.Transport != "streamable-http" {
		t.Errorf("cached_tools = %+v", saved)
	}
	if _, err := time.Parse(time.RFC3339Nano, saved.DiscoveredAt); err != nil {
		t.Errorf("discovered_at = %q", saved.DiscoveredAt)
	}
	// Saving it changed the account's tools, so the counter moved; and
	// the next process to build reads the row, not the server.
	if shared.at(ada) != 1 {
		t.Errorf("the counter is at %d after a discovery was saved", shared.at(ada))
	}
	before := server.requests.Load()
	other := newWorld(t, rows, shared, nil)
	if got := names(other.turn(t, ada)); !reflect.DeepEqual(got, want) {
		t.Errorf("another process: tools = %v", got)
	}
	if server.requests.Load() != before {
		t.Error("another process asked the server though the list was saved")
	}
}

func TestAServerIsTakenUpToTheCeiling(t *testing.T) {
	server := newRemote(t, 2)
	for i := range 9 {
		server.add(fmt.Sprintf("tool_%d", i), says("ok"))
	}
	rows := newRows()
	rows.add(ada, 1, "Many", serverConfig(server.URL, "k"))
	rows.add(ada, 2, "Many, saved", serverConfig(server.URL, "k", cachedList("a", "b", "c", "d", "e", "f", "", "a�b")))
	w := newWorld(t, rows, newCounter(), func(s *Settings) { s.Limits.ToolsPerServer = 5 })

	got := names(w.turn(t, ada))
	want := []string{"tool_0", "tool_1", "tool_2", "tool_3", "tool_4", "a", "b", "c", "d", "e"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tools = %v, want %v", got, want)
	}
	// Three pages of two reach six, one past the ceiling; the other two
	// pages are not asked for.
	if pages := server.method("tools/list"); pages != 3 {
		t.Errorf("tools/list was asked %d times", pages)
	}
	if !strings.Contains(w.logs.String(), "more tools than are taken") {
		t.Error("nothing in the log says tools were left out")
	}
	var saved struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(rows.config(ada, 1)[keyCached], &saved); err != nil || len(saved.Tools) != 5 {
		t.Errorf("%d tools were saved on the row, %v", len(saved.Tools), err)
	}
}

func TestToolsThatCannotBeCalledAreLeftOutAndSaid(t *testing.T) {
	rows := newRows()
	rows.add(ada, 1, "Odd", serverConfig("https://odd.example.com/mcp", "k",
		cachedList("fine", "", "broken�name", "fine", strings.Repeat("n", maxServerNameBytes+1), "also fine")))
	w := newWorld(t, rows, newCounter(), nil)
	if got := names(w.turn(t, ada)); !reflect.DeepEqual(got, []string{"fine", "mcp_1_also_fine"}) {
		t.Errorf("tools = %v", got)
	}
	logged := w.logs.String()
	for _, reason := range []string{"it has no name", "did not survive decoding", "listed its name twice", "too long"} {
		if !strings.Contains(logged, reason) {
			t.Errorf("the log does not say %q", reason)
		}
	}
}

// The open bug this package exists to close: one process's change must
// reach another process's cache.
func TestAChangeInOneProcessReachesAnotherThroughTheCounter(t *testing.T) {
	rows, shared := newRows(), newCounter()
	rows.add(ada, 1, "Notes", serverConfig("https://notes.example.com/mcp", "k", cachedList("create_note")))
	first, second := newWorld(t, rows, shared, nil), newWorld(t, rows, shared, nil)

	// Both processes build once and then answer from their own memory.
	for _, w := range []*world{first, second} {
		for range 3 {
			if got := names(w.turn(t, ada)); !reflect.DeepEqual(got, []string{"create_note"}) {
				t.Fatalf("tools = %v", got)
			}
		}
	}
	if lists, _ := rows.counts(); lists != 2 {
		t.Fatalf("the connections were listed %d times for six turns in two processes, want 2", lists)
	}
	if shared.gets != 6 {
		t.Errorf("the counter was read %d times for six turns, want one GET a turn", shared.gets)
	}

	// The second process connects another server and bumps, after its
	// "commit". The first process's very next turn has the new tool.
	rows.add(ada, 2, "Wiki", serverConfig("https://wiki.example.com/mcp", "k", cachedList("lookup")))
	if err := second.source.Bump(context.Background(), ada); err != nil {
		t.Fatal(err)
	}
	if got := names(first.turn(t, ada)); !reflect.DeepEqual(got, []string{"create_note", "lookup"}) {
		t.Errorf("the first process still offers %v", got)
	}
	// And a removal, the direction that matters most.
	rows.remove(ada, 1)
	_ = first.source.Bump(context.Background(), ada)
	if got := names(second.turn(t, ada)); !reflect.DeepEqual(got, []string{"lookup"}) {
		t.Errorf("the second process still offers %v", got)
	}
	// Another account's bump costs this one nothing.
	lists, _ := rows.counts()
	_ = first.source.Bump(context.Background(), ben)
	first.turn(t, ada)
	first.turn(t, ada)
	if after, _ := rows.counts(); after != lists+1 {
		t.Errorf("Ada's tools were rebuilt %d times after Ben's bump, want once (her own)", after-lists)
	}
}

func TestWithoutTheCounterEveryTurnBuildsFromTheDatabase(t *testing.T) {
	rows, shared := newRows(), newCounter()
	rows.add(ada, 1, "Notes", serverConfig("https://notes.example.com/mcp", "k", cachedList("create_note")))
	w := newWorld(t, rows, shared, nil)
	w.turn(t, ada)

	shared.setDown(true)
	// While Valkey is down a change made elsewhere cannot be announced, so
	// nothing held in memory may be used: each turn reads the rows.
	rows.add(ada, 2, "Wiki", serverConfig("https://wiki.example.com/mcp", "k", cachedList("lookup")))
	for range 3 {
		if got := names(w.turn(t, ada)); !reflect.DeepEqual(got, []string{"create_note", "lookup"}) {
			t.Fatalf("with the counter down, tools = %v", got)
		}
	}
	if lists, _ := rows.counts(); lists != 4 {
		t.Errorf("the connections were listed %d times, want 1 + 3", lists)
	}
	if !strings.Contains(w.logs.String(), "caching nothing") {
		t.Error("the log does not say the counter was unreadable")
	}

	// Back up: the entry from before the outage is under the old number
	// only if nothing bumped, and here nothing could. It is still within
	// its lifetime and is used; a turn built during the outage left
	// nothing behind to be used instead.
	shared.setDown(false)
	if got := names(w.turn(t, ada)); !reflect.DeepEqual(got, []string{"create_note"}) {
		t.Errorf("after the outage, tools = %v: something built during it was cached", got)
	}
	// Which is the gap the lifetime bounds.
	w.clock.advance(testLimits().Lifetime)
	if got := names(w.turn(t, ada)); !reflect.DeepEqual(got, []string{"create_note", "lookup"}) {
		t.Errorf("after the lifetime, tools = %v", got)
	}
}

func TestAnEntryExpiresWhateverTheCounterSays(t *testing.T) {
	rows := newRows()
	rows.add(ada, 1, "Notes", serverConfig("https://notes.example.com/mcp", "k", cachedList("create_note")))
	w := newWorld(t, rows, newCounter(), nil)
	w.turn(t, ada)
	w.clock.advance(testLimits().Lifetime - time.Second)
	w.turn(t, ada)
	if lists, _ := rows.counts(); lists != 1 {
		t.Fatalf("listed %d times inside the lifetime", lists)
	}
	w.clock.advance(time.Second)
	w.turn(t, ada)
	if lists, _ := rows.counts(); lists != 2 {
		t.Errorf("listed %d times after the lifetime, want 2", lists)
	}
}

func TestAServerThatDoesNotAnswerIsAskedAgainSoon(t *testing.T) {
	server := newRemote(t, 0)
	server.add("lookup", says("ok"))
	server.status.Store(503)
	rows, shared := newRows(), newCounter()
	rows.add(ada, 1, "Down for now", serverConfig(server.URL, "k"))
	rows.add(ada, 2, "Notes", serverConfig("https://notes.example.com/mcp", "k", cachedList("create_note")))
	w := newWorld(t, rows, shared, nil)

	// The turn goes on with the tools it could get.
	if got := names(w.turn(t, ada)); !reflect.DeepEqual(got, []string{"create_note"}) {
		t.Fatalf("tools = %v", got)
	}
	// Not asked on every turn...
	asked := server.requests.Load()
	w.turn(t, ada)
	if server.requests.Load() != asked {
		t.Error("a server that is down was asked again by the very next turn")
	}
	// ...but long before the full lifetime.
	server.status.Store(0)
	w.clock.advance(testLimits().RetryIncomplete)
	if got := names(w.turn(t, ada)); !reflect.DeepEqual(got, []string{"lookup", "create_note"}) {
		t.Errorf("after the retry time, tools = %v", got)
	}
}

func TestOnlyTheDatabaseFailingFailsTheTurn(t *testing.T) {
	rows := newRows()
	rows.fail = errors.New("connection reset")
	w := newWorld(t, rows, newCounter(), nil)
	if _, err := w.source.ForTurn(context.Background(), ada); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("ForTurn = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	rows.fail = nil
	w.counter.setDown(true)
	if _, err := w.source.ForTurn(cancelled, ada); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled turn: %v", err)
	}
}

func TestTheCacheKeepsNoMoreAccountsThanItWasTold(t *testing.T) {
	rows := newRows()
	account := func(n int64) string { return fmt.Sprintf("cccccccc-0000-4000-8000-%012d", n) }
	for id := int64(1); id <= 4; id++ {
		rows.add(account(id), id, "S", serverConfig("https://s.example.com/mcp", "k", cachedList("t")))
	}
	w := newWorld(t, rows, newCounter(), func(s *Settings) { s.Limits.Accounts = 2 })
	for id := int64(1); id <= 4; id++ {
		set, err := w.source.ForTurn(context.Background(), account(id))
		if err != nil {
			t.Fatal(err)
		}
		set.Close()
		w.clock.advance(time.Second)
	}
	if held := len(w.source.cache.entries); held != 2 {
		t.Errorf("%d accounts are held, want 2", held)
	}
	// The two most recent are the ones kept.
	for _, id := range []int64{3, 4} {
		if _, found := w.source.cache.entries[account(id)]; !found {
			t.Errorf("account %d was dropped", id)
		}
	}
}
