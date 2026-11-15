// Package agentrules serves the person's "ask first" choices for the chat's
// tools:
//
//	GET /api/v1/agent/permissions/tools      every tool, and whether it asks first
//	PUT /api/v1/agent/permissions/ask-first  mark one tool "ask first", or unmark it
//
// # The rule
//
// The owner's rule (2026-11-13): "let default be to always allow all, then
// settings you can change it". Every tool the chat has runs without asking,
// and a person may mark single tools "ask first"; the chat then pauses for
// their yes before each call of that tool. A mark is a row of
// agent_ask_first_tools, and the chat reads it at the moment each call is
// decided (internal/chat, AsksFirst), so a change here applies from the very
// next call. Answering "always allow" on an approval card takes the mark off
// again (internal/chat, StopAsking).
//
// What a mark does not touch: an automation's unattended run, which goes by
// the tools' own declarations and the automation's own switch (assembly,
// policy), and the confirm-to-send card of a mail draft, which is the draft
// itself rather than a tool's approval.
//
// # Which tools
//
// The chat's own tools come from the catalogue (catalogue.go), each under
// the heading the page shows. A connected app's (an MCP server's) tools are
// the ones its connection last listed (config.cached_tools), under the
// app's name, and a mark on one is kept by the connection's id and the
// server's own name for the tool, so it is gone with the connection and does
// not move when the model's name for the tool does.
//
// A mark can only be set on a tool the list shows: a mark that names no
// tool would look like a safeguard and do nothing. Taking one off is never
// refused, so a person can always stop being asked.
//
// # Whose marks
//
// The caller's, always: the account comes from the request's principal and
// nowhere else, and every statement names it. Setting a mark is for a person
// in a session; an API key or an agent acting for them is refused, because
// what runs without asking is the person's decision.
package agentrules

// The paths.
const (
	ToolsPath    = "/api/v1/agent/permissions/tools"
	AskFirstPath = "/api/v1/agent/permissions/ask-first"
)

// What a person is told.
const (
	toolAbsent      = "Tool not found."
	sessionRequired = "Only you, signed in, can choose which tools ask first."
)

// maxToolName is the column's width, which is also more than any tool name a
// provider accepts.
const maxToolName = 255

// Tool is one tool as the page lists it.
type Tool struct {
	// Name is the tool's name: the model's for the chat's own tools, the
	// server's own for a connected app's.
	Name string `json:"tool"`
	// Group is the heading the page lists it under: a catalogue heading,
	// or the connected app's name.
	Group string `json:"group"`
	// ConnectorID is the connected app's connection, null for the chat's
	// own tools.
	ConnectorID *int64 `json:"connector_id"`
	AskFirst    bool   `json:"ask_first"`
}

// Mark is one "ask first" choice: a tool, and for a connected app's tool its
// connection (0 for one of the chat's own tools).
type Mark struct {
	ConnectorID int64
	Tool        string
}

// BodyRoutes are the routes whose body the tier's buffered reader holds.
func BodyRoutes() []string { return []string{"PUT " + AskFirstPath} }

// Served is every route of this package, as "METHOD path".
func Served() []string { return []string{"GET " + ToolsPath, "PUT " + AskFirstPath} }
