package notes

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// Indexing queues a document's indexing job (indexqueue.Indexing). On a
// failure it has already marked the document failed, so the note says why
// its text is not searchable and the person can save again.
type Indexing interface {
	EnqueueAfter(ctx context.Context, documentID int64, delay time.Duration, logContext produce.LogContext) error
}

// Remover removes a document and everything stored for it
// (documentdeletion.Removal).
type Remover interface {
	Remove(ctx context.Context, documentID int64) error
}

// Routes is the ten routes and what they share.
type Routes struct {
	Pool     *pgxpool.Pool
	Indexing Indexing
	Removal  Remover
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
	// IndexDelay is how long a note's indexing job waits. cmd/api gives it
	// the deployment's setting (config.NoteIndexDelay, required there); zero
	// is DefaultIndexDelay, for a test that builds the routes itself.
	IndexDelay time.Duration
}

// Register adds every route of the package.
func (r Routes) Register(api huma.API) {
	register[noPath](api, r, http.MethodGet, NotesPath, "list-notes",
		"The caller's notes: pinned first, searchable, filterable by notebook", r.list)
	register[noPath](api, r, http.MethodPost, NotesPath, "create-note",
		"Write a note, or keep a chat answer as one", r.create)
	register[notePath](api, r, http.MethodGet, NotePath, "read-note",
		"One note with its text", r.read)
	register[notePath](api, r, http.MethodPatch, NotePath, "update-note",
		"Save a note's title or text; a changed text is indexed again", r.update)
	register[notePath](api, r, http.MethodDelete, NotePath, "delete-note",
		"Remove a note: its vectors, passages, files and row", r.remove)
	register[notePath](api, r, http.MethodPut, PinPath, "pin-note",
		"Keep a note at the top of its lists", r.pin)
	register[notePath](api, r, http.MethodDelete, PinPath, "unpin-note",
		"Stop keeping a note at the top", r.unpin)
	register[notePath](api, r, http.MethodPut, NotebookPath, "file-note",
		"File a note in one of the caller's notebooks, or in none", r.file)
	register[sourcePath](api, r, http.MethodPut, SourcePath, "switch-note-source-on",
		"Let a notebook's chat read the note", r.sourceOn)
	register[sourcePath](api, r, http.MethodDelete, SourcePath, "switch-note-source-off",
		"Stop a notebook's chat reading the note", r.sourceOff)
}

func (r Routes) indexDelay() time.Duration {
	if r.IndexDelay > 0 {
		return r.IndexDelay
	}
	return DefaultIndexDelay
}

// The shapes of path these routes have. Every parameter is a string, so an
// id that is not a whole number is this tier's own 422 envelope rather than
// Huma's.
type (
	noPath   struct{}
	notePath struct {
		NoteID string `path:"note_id" doc:"The note's id, which is also its document's id"`
	}
	sourcePath struct {
		NoteID     string `path:"note_id" doc:"The note's id"`
		NotebookID string `path:"notebook_id" doc:"The notebook whose chat reads the note"`
	}
)

// register mounts one route. Every answer is written by hand, so every
// operation is a stream.
func register[P any](api huma.API, r Routes, method, path, id, summary string, serve func(*exchange)) {
	huma.Register(api, huma.Operation{
		OperationID: id,
		Method:      method,
		Path:        path,
		Summary:     summary,
		Description: summary + ".",
		Tags:        []string{"notes"},
	}, func(_ context.Context, _ *P) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			if x := r.begin(ctx, id); x != nil {
				serve(x)
			}
		}}, nil
	})
}

// exchange is one request being answered.
type exchange struct {
	routes    Routes
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	caller    principal.Principal
	operation string
}

// begin answers and returns nil for a request the route must not run for.
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

// ids reads the named path parameters. A malformed one is the 422, every
// bad one at once; a whole number too wide for a bigint names nothing and
// is the 404, without sending Postgres a value it would fail on.
func (x *exchange) ids(names ...string) ([]int64, bool) {
	values := make([]int64, len(names))
	var failures []errorenvelope.Failure
	tooWide := false
	for index, name := range names {
		value, failure, rangeErr := requestnumber.Parse(x.ctx.Param(name), "path", name)
		if failure != nil {
			failures = append(failures, *failure)
		}
		if rangeErr != nil {
			tooWide = true
		}
		values[index] = value
	}
	if len(failures) > 0 {
		x.invalid(failures)
		return nil, false
	}
	if tooWide {
		x.notFound(noteAbsent)
		return nil, false
	}
	return values, true
}

// logContext is what the queued job carries for its log lines.
func (x *exchange) logContext() produce.LogContext {
	return produce.LogContext{RequestID: x.requestID, UserID: x.caller.UserID}
}

func (x *exchange) invalid(failures []errorenvelope.Failure) {
	x.routes.Errors.WriteValidation(x.response, x.requestID, failures)
}

func (x *exchange) notFound(message string) {
	x.routes.Errors.Write(x.response, http.StatusNotFound, message, x.requestID)
}

// fail logs what went wrong and answers the tier's sanitised 500. The cause
// is for the log only: an error from Postgres can carry a fragment of a row.
func (x *exchange) fail(reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", x.requestID),
		slog.String("operation", x.operation),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	x.routes.Logger.LogAttrs(x.ctx.Context(), slog.LevelError, "a note route could not be served", attributes...)
	x.routes.Errors.Write(x.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, x.requestID)
}

// send writes an answer. Notes are private and change as they are typed,
// so no answer is kept by a browser or a proxy.
func (x *exchange) send(status int, value any) {
	body, err := pythonjson.Marshal(value)
	if err != nil {
		x.fail("could not render the answer", err)
		return
	}
	x.response.Header().Set("Cache-Control", "private, no-store")
	pythonjson.Send(x.response, status, body)
}

// sendNote reads the note again and sends it whole: what every write
// answers, so an app's copy is the server's after any change.
func (x *exchange) sendNote(noteID int64, status int) {
	note, err := loadNote(x.ctx.Context(), x.routes.Pool, x.caller.UserID, noteID)
	if err != nil {
		x.fail("could not read the note back", err)
		return
	}
	if note == nil {
		// Deleted between the write and the read.
		x.notFound(noteAbsent)
		return
	}
	x.send(status, renderNote(*note, true))
}
