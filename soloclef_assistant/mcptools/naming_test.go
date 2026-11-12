package mcptools

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

func defs(toolNames ...string) []definition {
	out := make([]definition, len(toolNames))
	for i, name := range toolNames {
		out[i] = definition{name: name, schema: emptySchema}
	}
	return out
}

// modelNames is each tool's model-facing name, keyed "connection/own name".
func modelNames(built *catalog) map[string]string {
	out := map[string]string{}
	for _, entry := range built.tools {
		out[strings.Join([]string{string(rune('0' + entry.connectorID)), entry.name}, "/")] = entry.modelName
	}
	return out
}

func TestAToolKeepsItsNameOnlyWhenItIsUniqueAndAcceptable(t *testing.T) {
	built := assemble([]serverTools{
		{id: 3, name: "Notes", tools: defs("search", "create_note", "list.all", "search_knowledge_base", "été")},
		{id: 5, name: "Wiki", tools: defs("search", "lookup")},
	}, map[string]bool{"search_knowledge_base": true})

	want := map[string]string{
		"3/create_note": "create_note", // unique and acceptable: kept
		"5/lookup":      "lookup",
		"3/search":      "mcp_3_search", // two servers have it: both prefixed
		"5/search":      "mcp_5_search",
		"3/list.all":    "mcp_3_list_all", // a dot is not accepted by providers
		// The chat's own tool has this name, and the model must not be
		// given two tools it cannot tell apart.
		"3/search_knowledge_base": "mcp_3_search_knowledge_base",
		"3/été":                   "mcp_3___t__", // each byte outside the set becomes _
	}
	if got := modelNames(built); !reflect.DeepEqual(got, want) {
		t.Errorf("names:\n got %v\nwant %v", got, want)
	}
	for _, entry := range built.tools {
		if !tool.ValidName(entry.modelName) {
			t.Errorf("%q was given the name %q, which providers refuse", entry.name, entry.modelName)
		}
		if index := built.byName[entry.modelName]; built.tools[index].name != entry.name || built.tools[index].connectorID != entry.connectorID {
			t.Errorf("%q does not lead back to its own tool", entry.modelName)
		}
	}
}

func TestMadeUpNamesNeverCollideAndNeverTakeAnEarnedName(t *testing.T) {
	long := strings.Repeat("a", 70)
	built := assemble([]serverTools{
		// "a b" and "a.b" clean to the same text; the two long names are
		// the same once cut to 64.
		{id: 3, name: "One", tools: defs("a b", "a.b", long+"x", long+"y", "search")},
		// This server named a tool exactly what server 3's "search" would
		// be renamed to. It is unique and acceptable, so it keeps the
		// name, and server 3's tool is the one that gets a number.
		{id: 4, name: "Two", tools: defs("mcp_3_search", "search")},
	}, nil)

	got := modelNames(built)
	if got["4/mcp_3_search"] != "mcp_3_search" {
		t.Errorf("an earned name was taken away: %q", got["4/mcp_3_search"])
	}
	if got["3/search"] != "mcp_3_search_2" || got["4/search"] != "mcp_4_search" {
		t.Errorf("search: %q and %q", got["3/search"], got["4/search"])
	}
	if got["3/a b"] != "mcp_3_a_b" || got["3/a.b"] != "mcp_3_a_b_2" {
		t.Errorf("names that clean alike: %q and %q", got["3/a b"], got["3/a.b"])
	}
	seen := map[string]bool{}
	for key, name := range got {
		if seen[name] {
			t.Errorf("%q was given to two tools (one of them %s)", name, key)
		}
		seen[name] = true
		if len(name) > 64 || !tool.ValidName(name) {
			t.Errorf("%s: %q is not a name providers accept", key, name)
		}
	}
	if len(seen) != 7 {
		t.Errorf("%d names for 7 tools", len(seen))
	}
}

// A paused turn is resumed by another process, which must find the paused
// call's tool under the same name. The names are a function of the rows and
// of nothing else: not of map order, not of which process built them.
func TestTheSameRowsAlwaysGiveTheSameNames(t *testing.T) {
	servers := func() []serverTools {
		return []serverTools{
			{id: 2, name: "A", tools: defs("x y", "x.y", "x-y", "run", "dup")},
			{id: 9, name: "B", tools: defs("x y", "run", "dup", "mcp_2_run")},
		}
	}
	first := modelNames(assemble(servers(), map[string]bool{"x-y": true}))
	for range 50 {
		if again := modelNames(assemble(servers(), map[string]bool{"x-y": true})); !reflect.DeepEqual(first, again) {
			t.Fatalf("two builds disagree:\n%v\n%v", first, again)
		}
	}
}

func TestADescriptionSaysWhichServerAndFitsAProvider(t *testing.T) {
	if got := describe("Notes", "  Finds notes. "); got != "[MCP server: Notes] Finds notes." {
		t.Errorf("description = %q", got)
	}
	if got := describe("Notes", ""); got != "[MCP server: Notes] No description provided." {
		t.Errorf("a tool with no description = %q", got)
	}
	if got := describe("Notes", strings.Repeat("é", 5000)); len([]rune(got)) != descriptionRunes {
		t.Errorf("a long description is %d characters", len([]rune(got)))
	}
}

func TestASchemaThatIsNotAnObjectCostsTheToolItsArgumentsNotTheTurn(t *testing.T) {
	for _, bad := range []string{``, `null`, `[]`, `"object"`, `{`, `{}`, ` `} {
		if got := objectSchema(json.RawMessage(bad)); string(got) != string(emptySchema) {
			t.Errorf("objectSchema(%q) = %s", bad, got)
		}
	}
	good := `{"type":"object","properties":{"q":{"type":"string"}}}`
	if got := objectSchema(json.RawMessage("  " + good + "\n")); string(got) != good {
		t.Errorf("a good schema was changed: %s", got)
	}
}

func TestToolsThatFetchAnAddressAlwaysAsk(t *testing.T) {
	always := []string{
		"navigate", "goto", "open_url", "web_fetch_exa", "crawling_exa", "tavily_extract", "tavily_crawl", "tavily_map",
		"GoTo", "fetch_page", "site_crawler", "browser_navigate_back", "ScrapeUrl", "browse", "prefetch",
	}
	for _, name := range always {
		if !alwaysAsks(name) {
			t.Errorf("%q can be trusted, and it fetches an address the model chose", name)
		}
	}
	for _, name := range []string{"search", "web_search_exa", "tavily_search", "create_note", "extract", "observe", "goto_line", ""} {
		if alwaysAsks(name) {
			t.Errorf("%q always asks, and should be trustable", name)
		}
	}
}

func TestWhichNamesCanBeSavedInATrustedList(t *testing.T) {
	for _, name := range []string{"search", "list.all", "été", "a b", strings.Repeat("x", 255)} {
		if !storable(name) {
			t.Errorf("%q is refused", name)
		}
	}
	for label, name := range map[string]string{
		"empty": "", "a NUL": "a\x00b", "a control character": "a\nb", "a replaced surrogate": "a�b",
		"broken UTF-8": "a\xffb", "too long": strings.Repeat("x", 256),
	} {
		if storable(name) {
			t.Errorf("a name with %s can be saved", label)
		}
	}
}

func TestWhatIsReadFromAConnectionsConfig(t *testing.T) {
	where, err := readTarget([]byte(`{"server_config": {"transport": "sse", "url": " https://tools.example.com/mcp ", "headers": {"Authorization": "Bearer k"}}, "server_origin": "member"}`))
	if err != nil || where.origin != originMember || where.headers["Authorization"] != "Bearer k" || !strings.Contains(where.address, "tools.example.com") {
		t.Errorf("target = %+v, %v", where, err)
	}
	// No marker, or any marker but "member", is a site server.
	for _, marker := range []string{``, `, "server_origin": "site"`, `, "server_origin": 5`, `, "server_origin": "MEMBER"`} {
		where, err := readTarget([]byte(`{"server_config": {"transport": "http", "url": "https://mcp.example.com/"}` + marker + `}`))
		if err != nil || where.origin != originSite || where.headers != nil {
			t.Errorf("marker %q: %+v, %v", marker, where, err)
		}
	}
	for label, config := range map[string]string{
		"not an object":       `[]`,
		"no server_config":    `{"trusted_tools": []}`,
		"a local command":     `{"server_config": {"transport": "stdio", "command": "npx"}}`,
		"no transport at all": `{"server_config": {"url": "https://mcp.example.com/"}}`,
		"no address":          `{"server_config": {"transport": "http", "url": "  "}}`,
		"an address not text": `{"server_config": {"transport": "http", "url": 5}}`,
		"a header not text":   `{"server_config": {"transport": "http", "url": "https://mcp.example.com/", "headers": {"x-api-key": 12345678}}}`,
	} {
		if _, err := readTarget([]byte(config)); err == nil {
			t.Errorf("%s: a target was read", label)
		} else if strings.Contains(err.Error(), "example.com") {
			t.Errorf("%s: the refusal repeats the address: %v", label, err)
		}
	}

	if got := readTrusted([]byte(`{"trusted_tools": ["lookup", 5, null, "search", {"a": 1}]}`)); !reflect.DeepEqual(got, []string{"lookup", "search"}) {
		t.Errorf("trusted = %v", got)
	}
	for _, config := range []string{`{}`, `{"trusted_tools": "lookup"}`, `{"trusted_tools": null}`, `[]`} {
		if got := readTrusted([]byte(config)); len(got) != 0 {
			t.Errorf("%s: trusted = %v", config, got)
		}
	}

	if _, found := readCached([]byte(`{"cached_tools": {"tools": [{"description": "nameless"}]}}`)); found {
		t.Error("a saved list with a nameless tool was accepted")
	}
	if _, found := readCached([]byte(`{"cached_tools": {"discovered_at": "x"}}`)); found {
		t.Error("a saved list with no tools key was accepted")
	}
	if tools, found := readCached([]byte(`{"cached_tools": {"tools": []}}`)); !found || len(tools) != 0 {
		t.Error("a server with no tools is a saved answer, not a missing one")
	}
}
