// Package prompts serves the prompt library a person keeps: the templates
// the chat composer offers on "/".
//
//	GET    /api/v1/prompts              list_prompts
//	POST   /api/v1/prompts              create_prompt
//	PUT    /api/v1/prompts/{prompt_id}  update_prompt
//	DELETE /api/v1/prompts/{prompt_id}  delete_prompt
//
// There is no sharing. Soloclef is one person's notes, so on 2026-11-13 the
// community shelf, the copy from it and the publish flag were removed
// (soloclef_schema migration 00015): a prompt is its owner's and nobody
// else's, and a body that still names is_public changes nothing.
//
// The four are one slice: one table, one renderer, one gate, and nothing
// outside their own rows — no Celery message, no notification, no push, no
// email, no PostHog capture and no model.
//
// Three things about the module are worth reading before the routes.
//
// **The gate is require_session_context, as a dependency**, on all four. So an
// API key is refused in that tier's own words before anything else is looked
// at — including before a body that would not validate, because FastAPI solves
// a route's dependencies before it validates the route's own parameters. The
// one thing that comes earlier is a body that will not *decode*, which is a
// 422 FastAPI raises before any dependency runs and which this tier forwards
// (internal/requestbody).
//
// **The caller's own id is the predicate of every read and every write**, so
// the routes answer about nothing else: another account's prompt is the same
// "Prompt not found" as one that was never made.
//
// **No route of this module catches anything.** There is no `except Exception`
// anywhere in prompts_routes.py, so every 500 is one Starlette's outermost
// ServerErrorMiddleware writes, from *outside* the CORS middleware, and
// carries no CORS headers. That is one rule for the whole slice rather than a
// per-route decision, and failure.go is the single place it is applied.
package prompts

import (
	"regexp"
	"time"
)

// The four paths, as the Python router mounted them.
const (
	ListPath   = "/api/v1/prompts"
	CreatePath = "/api/v1/prompts"
	UpdatePath = "/api/v1/prompts/{prompt_id}"
	DeletePath = "/api/v1/prompts/{prompt_id}"
)

// ModePattern is app/schemas/prompts.py's own constraint on a prompt's mode,
// written exactly as it is there because the refusal quotes it
// (internal/requestbody.TextMatching).
var ModePattern = regexp.MustCompile("^(transform|explore)$")

// The lengths PromptCreate and PromptUpdate declare. `prompt` has a minimum
// and no maximum, which is the column's own `text`.
const (
	nameMinLength   = 1
	nameMaxLength   = 200
	promptMinLength = 1
)

// The refusals, word for word.
const (
	// sessionRequired is require_session_context's, shared with the other
	// routes behind it (the key list and the account's own memory).
	sessionRequired = "This action requires an interactive session"
	// promptAbsent is the 404 the update and the delete share.
	promptAbsent = "Prompt not found"
)

// deletedKey is the one key delete_prompt answers with. That route declares no
// response_model, so its dict is rendered by jsonable_encoder and Starlette's
// own json.dumps rather than by pydantic — which changes nothing in
// `{"success":true}` and is recorded because the three routes beside it are the
// other way (internal/pythonjson).
const deletedKey = "success"

// The line each route writes to the log before its sanitised 500, so the real
// reason stays in the log and never goes outward
// (app.exceptions.GENERIC_5XX_MESSAGE).
const (
	listUnserved   = "the prompt list could not be served"
	createUnserved = "the prompt could not be written"
	updateUnserved = "the prompt could not be updated"
	deleteUnserved = "the prompt could not be deleted"
)

// initialVersion is the Prompt model's `version` default, which is a
// Python-side default and so is written by whoever inserts the row.
const initialVersion int64 = 1

// refusal is an answer a write decided on inside its transaction: the status
// and the words the route then sends.
type refusal struct {
	Status  int
	Message string
}

// Prompt is a prompts row as PromptRead renders it.
type Prompt struct {
	ID        int64
	Name      string
	Prompt    string
	Mode      string
	Version   int64
	CreatedAt time.Time
}
