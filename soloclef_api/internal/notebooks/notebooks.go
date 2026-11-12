// Package notebooks serves a notebook's own row and the account's edits of
// the report templates.
//
//	GET    /api/v1/notebooks                                 the caller's notebooks
//	POST   /api/v1/notebooks                                 make one
//	GET    /api/v1/notebooks/{notebook_id}                   one notebook
//	PATCH  /api/v1/notebooks/{notebook_id}                   rename it, change its chat settings
//	DELETE /api/v1/notebooks/{notebook_id}                   remove it
//	PUT    /api/v1/notebooks/{notebook_id}/feature           pin it to the top of the list
//	DELETE /api/v1/notebooks/{notebook_id}/feature           remove the pin
//	GET    /api/v1/notebook-report-templates                 the report templates
//	PUT    /api/v1/notebook-report-templates/{template_key}  save an edit of one
//	DELETE /api/v1/notebook-report-templates/{template_key}  discard the edit
//
// # Whose notebook
//
// A notebook has one owner, the account that made it, and every statement
// here names that owner beside the id (`WHERE id = $1 AND user_id = $2`).
// That predicate is the whole of the access check. There is no membership,
// role or permission to read first, so each route is one statement where it
// used to be a gate of two reads and then its own, and another account's
// notebook is the same `404 Notebook not found` as one that was never made:
// no route says which ids are in use.
//
// A report template's edit is the account's too, used inside any of its
// notebooks.
//
// # What the routes do well
//
// **One notebook's naming state never costs a caller their list.**
// `title_pending` reads a jsonb column; a value in it that cannot be read
// reads as "not pending" (naming.go).
//
// **A pin is a column, not a row.** A notebook has one owner, so "pinned" is
// `notebooks.featured_at`, read with the notebook and written in one guarded
// UPDATE. Pinning twice keeps the first instant.
//
// **A write is one statement.** Create answers the row its INSERT returned;
// delete, pin and unpin are each a guarded statement whose result says
// whether there was a notebook; a template is saved with an upsert, so two
// saves that race cannot both insert.
//
// **A string Postgres cannot store is a 422 that names its field**, not a
// 500 after validation passed (requests.go).
//
// **A write that changes nothing writes nothing**, so a template's
// `updated_at` moves only when it actually changed.
//
// **Only a default title lets a new notebook name itself**, whatever its
// case or spacing (default_title.go).
//
// **A failure nobody expected keeps its CORS headers**, so a browser can
// read the envelope and show its request id (exchange.go).
//
// **`GET /api/v1/notebooks/search` is not this package's.** It is the search
// across notebooks (internal/notebooksearch), registered as a literal path,
// which ServeMux prefers to `{notebook_id}`.
//
// # What is not here
//
// A notebook's notes are not served by this package any more. They were rows
// of a table of their own, scratch text the assistant could not read; a note
// is now a document (internal/notes), and the routes that file one in a
// notebook belong to that package.
//
// Still to be written in Go: copying a notebook, its sources,
// its chat threads, and the studio's generate, outputs and suggestions.
package notebooks

import "net/http"

// The paths.
const (
	NotebooksPath = "/api/v1/notebooks"
	NotebookPath  = "/api/v1/notebooks/{notebook_id}"
	FeaturePath   = "/api/v1/notebooks/{notebook_id}/feature"
	TemplatesPath = "/api/v1/notebook-report-templates"
	TemplatePath  = "/api/v1/notebook-report-templates/{template_key}"
)

// Served is every route of this package as a "METHOD /path" pair, in the
// order of the package comment: what cmd/api's servedRoutes() lists.
func Served() []string {
	return []string{
		http.MethodGet + " " + NotebooksPath,
		http.MethodPost + " " + NotebooksPath,
		http.MethodGet + " " + NotebookPath,
		http.MethodPatch + " " + NotebookPath,
		http.MethodDelete + " " + NotebookPath,
		http.MethodPut + " " + FeaturePath,
		http.MethodDelete + " " + FeaturePath,
		http.MethodGet + " " + TemplatesPath,
		http.MethodPut + " " + TemplatePath,
		http.MethodDelete + " " + TemplatePath,
	}
}

// WithBodies is the routes that read a body, which cmd/api's body reader
// must cover so that an undecodable body is refused before the credential
// is looked at (internal/requestbody.Middleware).
//
// The two feature routes and the template reset are deliberately absent:
// they declare no body, so a request carrying nonsense is answered as if it
// carried nothing.
func WithBodies() []string {
	return []string{
		http.MethodPost + " " + NotebooksPath,
		http.MethodPatch + " " + NotebookPath,
		http.MethodPut + " " + TemplatePath,
	}
}

// The refusals the routes raise themselves.
const (
	notebookAbsent = "Notebook not found"
	templateAbsent = "Template not found"
)

// privateNoStore is the Cache-Control the two reads of a notebook set on the
// answer they return. A refusal does not carry it.
const privateNoStore = "private, no-store"
