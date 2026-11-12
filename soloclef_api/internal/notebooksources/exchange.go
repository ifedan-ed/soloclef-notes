package notebooksources

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Routes is the five routes and what they share.
type Routes struct {
	Pool   *pgxpool.Pool
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Naming publishes the worker's notebook_autoname when added sources
	// give a self-named notebook something to be named from
	// (celeryqueue.Queue).
	Naming Enqueuer
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

// Register mounts every route.
func (r Routes) Register(api huma.API) {
	mount[notebookPath](api, r, http.MethodGet, SourcesPath, "list-notebook-sources", "A notebook's sources", r.list)
	mount[notebookPath](api, r, http.MethodPost, SourcesPath, "add-notebook-sources", "Add documents and folders to a notebook", r.add)
	mount[sourcePath](api, r, http.MethodPatch, SourcePath, "toggle-notebook-source", "Tick or untick one source", r.toggleOne)
	mount[notebookPath](api, r, http.MethodPost, TogglePath, "toggle-notebook-sources", "Tick or untick a notebook's sources", r.toggleAll)
	mount[sourcePath](api, r, http.MethodDelete, SourcePath, "remove-notebook-source", "Remove a source from a notebook", r.remove)
}

func (r Routes) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

// Enqueuer publishes one task for the worker tier.
type Enqueuer interface {
	Enqueue(ctx context.Context, message produce.Message) (string, error)
}

type (
	notebookPath struct {
		NotebookID string `path:"notebook_id"`
	}
	sourcePath struct {
		NotebookID string `path:"notebook_id"`
		SourceID   string `path:"source_id"`
	}
)

// exchange is one request being answered.
type exchange struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	userID    string
	routes    Routes
}

func mount[P any](api huma.API, r Routes, method, path, id, summary string, serve func(*exchange)) {
	huma.Register(api, huma.Operation{
		OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"notebooks"},
	}, func(_ context.Context, _ *P) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			x := &exchange{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context()), routes: r}
			caller, ok := principal.FromContext(ctx.Context())
			if !ok {
				x.fail("the route ran without a caller", nil)
				return
			}
			x.userID = caller.UserID
			serve(x)
		}}, nil
	})
}

// ids reads the named path parameters as whole numbers. One that is not is
// a 422 naming it; one too wide for a bigint names no row and is a 404.
func (x *exchange) ids(names ...string) ([]int64, bool) {
	values := make([]int64, 0, len(names))
	var failures []errorenvelope.Failure
	for _, name := range names {
		text := x.ctx.Param(name)
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			if numeric(text) {
				// Digits too many for a bigint: no row has that id.
				x.notFound(notebookAbsent)
				return nil, false
			}
			failures = append(failures, errorenvelope.Failure{
				Location: []string{"path", name}, Message: "Input should be a valid integer"})
		}
		values = append(values, value)
	}
	if len(failures) > 0 {
		x.routes.Errors.WriteValidation(x.response, x.requestID, failures)
		return nil, false
	}
	return values, true
}

func numeric(text string) bool {
	if text == "" {
		return false
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// body decodes the request's JSON body into value, or answers the 422.
func (x *exchange) body(value any) bool {
	raw, err := io.ReadAll(io.LimitReader(x.ctx.BodyReader(), 1<<20))
	if err == nil {
		err = json.Unmarshal(raw, value)
	}
	if err != nil {
		x.invalid("body", "The body should be a JSON object of the documented shape")
		return false
	}
	return true
}

func (x *exchange) invalid(field, message string) {
	location := []string{"body"}
	if field != "body" {
		location = append(location, field)
	}
	x.routes.Errors.WriteValidation(x.response, x.requestID, []errorenvelope.Failure{{Location: location, Message: message}})
}

func (x *exchange) notFound(message string) {
	x.routes.Errors.Write(x.response, http.StatusNotFound, message, x.requestID)
}

func (x *exchange) send(status int, value any) {
	x.response.Header().Set("Content-Type", "application/json")
	x.response.Header().Set("Cache-Control", "private, no-store")
	x.response.WriteHeader(status)
	_ = json.NewEncoder(x.response).Encode(value)
}

func (x *exchange) sendNothing() {
	x.response.WriteHeader(http.StatusNoContent)
}

// fail logs the cause and answers the 500. A cancelled request is not a
// fault and is not logged as one.
func (x *exchange) fail(reason string, cause error) {
	if !errors.Is(cause, context.Canceled) {
		attributes := []any{slog.String("request_id", x.requestID), slog.String("reason", reason)}
		if cause != nil {
			attributes = append(attributes, slog.String("error", cause.Error()))
		}
		x.routes.Logger.ErrorContext(x.ctx.Context(), "a notebook source route could not be served", attributes...)
	}
	x.routes.Errors.Write(x.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, x.requestID)
}
