// Package prompts serves the six routes of app/routes/prompts_routes.py: the
// prompt library a person keeps, and the community shelf it can be published
// to.
//
//	GET    /api/v1/prompts              list_prompts
//	POST   /api/v1/prompts              create_prompt
//	PUT    /api/v1/prompts/{prompt_id}  update_prompt
//	DELETE /api/v1/prompts/{prompt_id}  delete_prompt
//	GET    /api/v1/prompts/public       list_public_prompts
//	POST   /api/v1/prompts/{prompt_id}/copy  copy_public_prompt
//
// They are one slice: one table, one renderer, one gate, and nothing outside
// their own rows — no Celery message, no notification, no push, no email, no
// PostHog capture and no model. The inventory in custom/docs/go-api-tier.md
// puts all four writes in the hundred with nothing in the way.
//
// Four things about the module are worth reading before the routes.
//
// **The gate is require_session_context, as a dependency**, on all six. So an
// API key is refused in that tier's own words before anything else is looked
// at — including before a body that would not validate, because FastAPI solves
// a route's dependencies before it validates the route's own parameters. The
// one thing that comes earlier is a body that will not *decode*, which is a
// 422 FastAPI raises before any dependency runs and which this tier forwards
// (internal/requestbody).
//
// **The caller's own id is the predicate of every read and every write**, so
// the routes answer about nothing else: another account's prompt is the same
// "Prompt not found" as one that was never made. The one exception is on
// purpose, the community shelf, which shows what others chose to publish.
//
// **Who may publish is the administrator.** `is_public` is refused unless
// the caller's own `is_superuser` flag is set, the same flag the web app reads
// from /users/me to show the admin pages (admin.go).
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

// The six paths, as the Python router mounts them.
const (
	ListPath   = "/api/v1/prompts"
	CreatePath = "/api/v1/prompts"
	UpdatePath = "/api/v1/prompts/{prompt_id}"
	DeletePath = "/api/v1/prompts/{prompt_id}"
	PublicPath = "/api/v1/prompts/public"
	CopyPath   = "/api/v1/prompts/{prompt_id}/copy"
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
	// publishingRefused answers an account that is not an administrator and
	// asks to publish to the community shelf.
	publishingRefused = "Sharing prompts with everyone is turned off"
	// promptAbsent is the 404 the update, the delete and the copy share.
	promptAbsent = "Prompt not found"
)

// deletedKey is the one key delete_prompt answers with. That route declares no
// response_model, so its dict is rendered by jsonable_encoder and Starlette's
// own json.dumps rather than by pydantic — which changes nothing in
// `{"success":true}` and is recorded because the five routes beside it are the
// other way (internal/pythonjson).
const deletedKey = "success"

// The line each route writes to the log before its sanitised 500, so the real
// reason stays in the log and never goes outward
// (app.exceptions.GENERIC_5XX_MESSAGE).
const (
	listUnserved   = "the prompt list could not be served"
	shelfUnserved  = "the community prompts could not be served"
	createUnserved = "the prompt could not be written"
	updateUnserved = "the prompt could not be updated"
	deleteUnserved = "the prompt could not be deleted"
	copyUnserved   = "the prompt could not be copied"
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

// Prompt is a prompts row as PromptRead renders it, with the author's display
// name the community list adds.
type Prompt struct {
	ID        int64
	Name      string
	Prompt    string
	Mode      string
	IsPublic  bool
	Version   int64
	CreatedAt time.Time
	// AuthorName is only read by the community list, where it is
	// PublicPromptRead's extra field. It stays a pointer because the Python
	// route reads it through `p.user if p.user else None` and because
	// "user".display_name is itself nullable.
	AuthorName *string
}
