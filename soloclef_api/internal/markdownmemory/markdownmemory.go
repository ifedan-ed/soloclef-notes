// Package markdownmemory serves the six routes of app/services/memory: the
// markdown document a signed-in account keeps for itself and the one a
// workspace keeps for its team.
//
//	GET  /api/v1/users/me/memory                  get_user_memory
//	PUT  /api/v1/users/me/memory                  update_user_memory
//	POST /api/v1/users/me/memory/reset            reset_user_memory
//	GET  /api/v1/workspaces/{workspace_id}/memory get_team_memory
//	PUT  /api/v1/workspaces/{workspace_id}/memory update_team_memory
//	POST /api/v1/workspaces/{workspace_id}/memory/reset reset_team_memory
//
// They are one slice: the same response model, the same save pipeline, and two
// columns — "user".memory_md and workspaces.shared_memory_md — that differ only
// in which gate decides who may write them. The routes owe nothing outside
// those columns: app/services/memory's only side effect is the LLM rewrite, and
// every route calls save_memory with llm=None, so no model is reached.
//
// What makes the port deep rather than wide is that a write *rewrites* what it
// is given. save_memory strips a preamble, refuses a document that is too long
// or has no heading, refuses a personal heading in team memory, and then
// re-renders the document through app/services/memory/document.py's parser —
// which normalises a legacy bullet and an ISO week date into a plain date. So
// the bytes a client gets back are the parser's, and the parser rests on four
// pieces of Python behaviour that Go does not share: str.strip's whitespace
// set, str.splitlines' line boundaries, str.isalnum, and
// date.fromisoformat's acceptance of a week date. Each is mirrored in its own
// file beside this one, with the vectors that came out of the running tier.
package markdownmemory

// The limits app/services/memory/validation.py declares, which the response
// carries on every one of the six routes.
const (
	SoftLimit = 18_000
	HardLimit = 25_000
)

// Scope is app/services/memory/service.py's MemoryScope. It decides the column,
// the refusal wording and whether the forbidden-heading rule applies.
type Scope string

const (
	UserScope Scope = "user"
	TeamScope Scope = "team"
)
