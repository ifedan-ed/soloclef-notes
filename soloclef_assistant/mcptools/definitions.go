package mcptools

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// definition is one tool as its server describes it.
type definition struct {
	// name is the server's own name for the tool: what a call is made with
	// and what trust is recorded under.
	name        string
	description string
	// schema is the JSON Schema of the arguments, always a JSON object.
	schema json.RawMessage
}

// emptySchema is given to the model for a tool whose server sent no usable
// schema: an object with no declared properties, so the model can still call
// the tool and the server decides what it accepts.
var emptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

// cachedTools is config.cached_tools, the shape the Python tier writes after
// a live discovery and both tiers read.
type cachedTools struct {
	DiscoveredAt  string       `json:"discovered_at"`
	ServerVersion *string      `json:"server_version"`
	ServerName    *string      `json:"server_name"`
	Transport     *string      `json:"transport"`
	Tools         []cachedTool `json:"tools"`
}

type cachedTool struct {
	Name        *string         `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// readCached returns the tool list saved on a connection's row. found is
// false when there is none or it cannot be read; the server is then asked.
// That is not a quiet substitute for the saved list: asking the server is
// where the saved list comes from, and a successful answer replaces it.
func readCached(config []byte) (tools []definition, found bool) {
	raw, present := keys(config)[keyCached]
	if !present {
		return nil, false
	}
	var cached cachedTools
	if json.Unmarshal(raw, &cached) != nil || cached.Tools == nil {
		return nil, false
	}
	tools = make([]definition, 0, len(cached.Tools))
	for _, stored := range cached.Tools {
		if stored.Name == nil {
			return nil, false
		}
		tools = append(tools, definition{name: *stored.Name, description: stored.Description, schema: objectSchema(stored.InputSchema)})
	}
	return tools, true
}

// writeCached is the value stored as config.cached_tools after a live
// discovery.
func writeCached(tools []definition, serverName, serverVersion, transport string, at time.Time) ([]byte, error) {
	cached := cachedTools{
		DiscoveredAt: at.UTC().Format(time.RFC3339Nano),
		Transport:    &transport,
		Tools:        make([]cachedTool, len(tools)),
	}
	if serverName != "" {
		cached.ServerName = &serverName
	}
	if serverVersion != "" {
		cached.ServerVersion = &serverVersion
	}
	for i, t := range tools {
		name := t.name
		cached.Tools[i] = cachedTool{Name: &name, Description: t.description, InputSchema: t.schema}
	}
	return json.Marshal(cached)
}

// objectSchema returns schema when it is a JSON object and emptySchema when
// it is anything else. The loop refuses a tool whose parameters are not an
// object, and one server's malformed schema must cost that tool its
// declared arguments, not the whole turn its tools.
func objectSchema(schema json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(schema)
	if len(trimmed) < 2 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return emptySchema
	}
	if len(trimmed) == 2 {
		// "{}" says nothing; providers want at least the type.
		return emptySchema
	}
	return json.RawMessage(trimmed)
}

// usable reports whether a tool's name can be called at all, and why not.
//
// U+FFFD is refused for a reason particular to Go: its JSON decoder replaces
// a lone surrogate, and any other broken encoding, with U+FFFD. The name
// held here would then differ from the server's, and a call made with it
// would name a tool the server does not have.
func usable(name string) (ok bool, why string) {
	switch {
	case name == "":
		return false, "it has no name"
	case !utf8.ValidString(name) || strings.ContainsRune(name, utf8.RuneError):
		return false, "its name did not survive decoding"
	case len(name) > maxServerNameBytes:
		return false, "its name is too long"
	}
	return true, ""
}

// maxServerNameBytes bounds a server's own tool name. The protocol asks
// servers for 128 characters at most; four times that leaves room for any
// script and still keeps a hostile server from handing over a megabyte.
const maxServerNameBytes = 512

// offered is one tool as a turn offers it: the definition, the connection
// it belongs to, and the name the model knows it by.
type offered struct {
	definition
	connectorID   int64
	connectorName string
	// modelName is what the model calls the tool. Nothing but the model
	// and the loop's lookup uses it: calls go out under definition.name and
	// trust is recorded under (connectorID, definition.name).
	modelName string
	alwaysAsk bool
}

// catalog is an account's MCP tools, built once and shared by every turn
// that finds it in the cache. It is never changed after it is built.
type catalog struct {
	tools  []offered
	byName map[string]int
	// complete is false when some server that had to be asked for its
	// tools did not answer. Such a catalog is kept for a shorter time.
	complete bool
}

// serverTools is one connection's tools, in the server's order.
type serverTools struct {
	id    int64
	name  string
	tools []definition
}

// descriptionRunes bounds a description. A provider refuses a request whose
// tool description is over its limit (1024 characters at the strictest),
// and the refusal does not say which tool was at fault.
const descriptionRunes = 1024

// assemble gives every tool its model-facing name. servers must be in
// connection-id order; reserved names belong to the chat's own tools.
//
// The rules, which exist so that two processes reading the same rows always
// arrive at the same names (a turn paused in one process is resumed in
// another, with the model's call already written down under its name):
//
//  1. A tool keeps its server's name when that name is one a provider
//     accepts, no other connection in the account has a tool of that
//     name, and it is not the name of one of the chat's own tools.
//  2. Every other tool is mcp_{connection id}_{name}, with each character a
//     provider would refuse replaced by "_" and the whole cut to 64.
//  3. If rule 2 produces a name already taken (two names that clean to the
//     same text, or a server that named a tool mcp_3_search itself), a
//     number is put on the end. Connections are visited oldest first and
//     tools in the server's order, so which one gets the number is fixed.
//
// Names from rule 1 are all assigned before any from rule 2, so a tool that
// earned its own name never loses it to another tool's made-up one.
func assemble(servers []serverTools, reserved map[string]bool) *catalog {
	count := map[string]int{}
	for _, server := range servers {
		for _, t := range server.tools {
			count[t.name]++
		}
	}
	total := 0
	for _, server := range servers {
		total += len(server.tools)
	}
	built := &catalog{tools: make([]offered, 0, total), byName: make(map[string]int, total), complete: true}
	for _, server := range servers {
		for _, t := range server.tools {
			entry := offered{definition: t, connectorID: server.id, connectorName: server.name, alwaysAsk: alwaysAsks(t.name)}
			entry.description = describe(server.name, t.description)
			if count[t.name] == 1 && tool.ValidName(t.name) && !reserved[t.name] {
				entry.modelName = t.name
				built.byName[t.name] = len(built.tools)
			}
			built.tools = append(built.tools, entry)
		}
	}
	for i := range built.tools {
		entry := &built.tools[i]
		if entry.modelName != "" {
			continue
		}
		taken := func(name string) bool {
			_, used := built.byName[name]
			return used || reserved[name]
		}
		entry.modelName = prefixed(entry.connectorID, entry.name, taken)
		built.byName[entry.modelName] = i
	}
	return built
}

// maxNameBytes is the longest tool name every provider accepts.
const maxNameBytes = 64

// prefixed is rule 2 and rule 3 of assemble.
func prefixed(connectorID int64, name string, taken func(string) bool) string {
	cleaned := []byte("mcp_" + strconv.FormatInt(connectorID, 10) + "_" + name)
	for i, c := range cleaned {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-') {
			cleaned[i] = '_'
		}
	}
	candidate := string(cleaned[:min(len(cleaned), maxNameBytes)])
	for n := 2; taken(candidate); n++ {
		suffix := "_" + strconv.Itoa(n)
		candidate = string(cleaned[:min(len(cleaned), maxNameBytes-len(suffix))]) + suffix
	}
	return candidate
}

// describe is the description the model reads: which server the tool is on,
// then the server's own words, cut to what a provider accepts.
func describe(serverName, description string) string {
	text := "[MCP server: " + serverName + "] " + strings.TrimSpace(description)
	if strings.TrimSpace(description) == "" {
		text = "[MCP server: " + serverName + "] No description provided."
	}
	if utf8.RuneCountInString(text) > descriptionRunes {
		text = string([]rune(text)[:descriptionRunes])
	}
	return text
}
