package notebooks

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/starlettepath"
)

// Routes is the ten routes and what they share.
type Routes struct {
	// Database is where every statement runs: the pool. Each route is one
	// guarded statement, or a read followed by statements that describe what
	// was read, so none needs a transaction to hold it together.
	Database DBTX
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
	// Now is the clock every minted timestamp and `title_pending` come from.
	// Nil is the system clock; only a test names another.
	Now func() time.Time
}

// Register adds every route of the package. Each also has its own Register…
// method, for a deployment that mounts them one at a time.
func (r Routes) Register(api huma.API) {
	r.RegisterNotebookList(api)
	r.RegisterNotebookCreate(api)
	r.RegisterNotebookRead(api)
	r.RegisterNotebookUpdate(api)
	r.RegisterNotebookDelete(api)
	r.RegisterFeature(api)
	r.RegisterUnfeature(api)
	r.RegisterTemplateList(api)
	r.RegisterTemplateSave(api)
	r.RegisterTemplateReset(api)
}

func (r Routes) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

// The shapes of path these routes have. Every parameter is declared as a
// string, because the refusal for an id that is not a whole number is this
// tier's own 422 envelope, and a schema violation would be answered by Huma
// in words of its own.
type (
	noPath       struct{}
	notebookPath struct {
		NotebookID string `path:"notebook_id" doc:"The notebook's id"`
	}
	templatePath struct {
		TemplateKey string `path:"template_key" doc:"The seeded template's key"`
	}
)

// register mounts one route. Every operation is a stream, because every
// answer of this package is written by hand through internal/pythonjson or
// internal/errorenvelope and none is Huma's to serialise.
func register[P any](api huma.API, r Routes, method, path, id, summary string, serve func(*exchange)) {
	huma.Register(api, huma.Operation{
		OperationID: id,
		Method:      method,
		Path:        path,
		Summary:     summary,
		Description: summary + ".",
		Tags:        []string{"notebooks"},
	}, func(_ context.Context, _ *P) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			if x := r.begin(ctx, id); x != nil {
				serve(x)
			}
		}}, nil
	})
}

// exchange is one request being answered: what every route needs to refuse
// or fail.
type exchange struct {
	routes    Routes
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	caller    principal.Principal
	// operation names the route in the log line of a failure.
	operation string
}

// begin is what every route does before its own first line. It answers and
// returns nil for a request the route must not run for.
func (r Routes) begin(ctx huma.Context, operation string) *exchange {
	x := &exchange{
		routes:    r,
		ctx:       ctx,
		response:  humawriter.New(ctx),
		requestID: requestcontext.ID(ctx.Context()),
		operation: operation,
	}
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// answer if the wiring is ever wrong.
		x.fail("the route ran without an authenticated caller", nil)
		return nil
	}
	x.caller = caller
	return x
}

// unroutable answers the router's own 404 for a path that should not have
// matched this route at all -- a parameter holding an escaped slash -- and
// reports whether it did (internal/starlettepath).
func (x *exchange) unroutable(names ...string) bool {
	parameters := make([]string, 0, len(names))
	for _, name := range names {
		parameters = append(parameters, x.ctx.Param(name))
	}
	if !starlettepath.Unroutable(parameters...) {
		return false
	}
	starlettepath.WriteRouterNotFound(x.response, x.requestID)
	return true
}

// number is one whole-number parameter: its value, or the fact that no row
// can have it. Every id here is a Postgres `bigint`, so a whole number wider
// than that names nothing. It is answered as what it is, "not found", and
// never sent to a statement, which would fail on it and turn a typo into a
// 500.
type number struct {
	value    int64
	nameless bool
}

// pathNumbers reads the named path parameters, collecting every bad one in
// order, so that a request's failures are all reported at once.
func (x *exchange) pathNumbers(failures *[]errorenvelope.Failure, names ...string) []number {
	numbers := make([]number, 0, len(names))
	for _, name := range names {
		value, failure, rangeErr := requestnumber.Parse(x.ctx.Param(name), "path", name)
		if failure != nil {
			*failures = append(*failures, *failure)
		}
		numbers = append(numbers, number{value: value, nameless: rangeErr != nil})
	}
	return numbers
}

// invalid writes the 422.
func (x *exchange) invalid(failures []errorenvelope.Failure) {
	x.routes.Errors.WriteValidation(x.response, x.requestID, failures)
}

// notFound writes the 404 for a row the caller does not have.
func (x *exchange) notFound(message string) {
	x.routes.Errors.Write(x.response, http.StatusNotFound, message, x.requestID)
}

// fail logs what went wrong and answers the tier's sanitised 500. The cause
// is for the log only: an error from Postgres can carry a fragment of a row.
// The CORS headers are left on the answer, so the web app can read the
// envelope and show its request id.
func (x *exchange) fail(reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", x.requestID),
		slog.String("operation", x.operation),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	x.routes.Logger.LogAttrs(x.ctx.Context(), slog.LevelError,
		"a notebook route could not be served", attributes...)
	x.routes.Errors.Write(x.response, http.StatusInternalServerError,
		errorenvelope.GenericServerMessage, x.requestID)
}

// notebookParameter reads the notebook id every notebook-scoped route starts
// with, answers the router's 404 or the 422 itself, and reports whether the
// route should go on.
func (x *exchange) notebookParameter() (notebook number, ok bool) {
	if x.unroutable("notebook_id") {
		return number{}, false
	}
	var failures []errorenvelope.Failure
	ids := x.pathNumbers(&failures, "notebook_id")
	if len(failures) > 0 {
		x.invalid(failures)
		return number{}, false
	}
	return ids[0], true
}

// found is what a route does with the result of a statement that looks for
// one of the caller's notebooks: it answers the failure, or the 404 for a
// notebook the caller does not have, and reports whether there is one to go
// on with. "Does not have" is one answer for never made and for somebody
// else's, by construction: the statement cannot tell them apart either.
func (x *exchange) found(notebook *Notebook, err error, doing string) bool {
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		x.fail(doing, err)
		return false
	}
	if notebook == nil || err != nil {
		x.notFound(notebookAbsent)
		return false
	}
	return true
}

// send writes an answer.
func (x *exchange) send(status int, value any) {
	body, err := pythonjson.Marshal(value)
	if err != nil {
		x.fail("could not render the answer", err)
		return
	}
	pythonjson.Send(x.response, status, body)
}

// sendCached is send for the two reads of a notebook, which tell the
// browser to keep no copy.
func (x *exchange) sendCached(status int, value any) {
	body, err := pythonjson.Marshal(value)
	if err != nil {
		x.fail("could not render the answer", err)
		return
	}
	x.response.Header().Set("Cache-Control", privateNoStore)
	pythonjson.Send(x.response, status, body)
}

// sendNothing writes the delete's 204: no body, so no Content-Type and no
// Content-Length either.
func (x *exchange) sendNothing() {
	x.response.WriteHeader(http.StatusNoContent)
}
