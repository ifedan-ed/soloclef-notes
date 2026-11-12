package documentlibrary

import (
	"bytes"
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

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// Indexing queues a document's indexing job (indexqueue.Indexing). On a
// queue that does not answer it marks the document failed with a reason the
// app shows, so nothing waits on a job that was never sent.
type Indexing interface {
	Enqueue(ctx context.Context, documentID int64, logContext produce.LogContext) error
}

// Routes is the five routes and what they share.
type Routes struct {
	Pool     *pgxpool.Pool
	Indexing Indexing
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
	// Now is nil for the system clock; only a test names another. It is the
	// application's clock because a version is stamped and aged with it
	// (documents.ReplaceText).
	Now func() time.Time
}

// Register mounts every route. The literal `/documents/search` sits beside
// `/documents/{document_id}`; ServeMux prefers the literal.
func (r Routes) Register(api huma.API) {
	mount[noPath](api, r, http.MethodGet, ListPath, "list-documents", "A page of the caller's documents", r.list)
	mount[noPath](api, r, http.MethodGet, SearchPath, "search-documents", "The caller's documents whose title contains a term", r.search)
	mount[documentPath](api, r, http.MethodGet, OnePath, "read-document", "One document", r.read)
	mount[noPath](api, r, http.MethodPost, ListPath, "add-document", "Add a note", r.add)
	mount[documentPath](api, r, http.MethodPost, SavePath, "save-document", "Replace a document's text", r.save)
}

func (r Routes) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

type (
	noPath       struct{}
	documentPath struct {
		DocumentID string `path:"document_id"`
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
		OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"documents"},
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

// documentID reads the path's id. One that is not a whole number is a 422;
// one too wide for a bigint names no row and is the 404 an unknown id is.
func (x *exchange) documentID() (int64, bool) {
	text := x.ctx.Param("document_id")
	value, err := strconv.ParseInt(text, 10, 64)
	if err == nil && value > 0 {
		return value, true
	}
	if digits(text) {
		x.refuse(http.StatusNotFound, absent(text))
		return 0, false
	}
	x.invalid(errorenvelope.Failure{Location: []string{"path", "document_id"}, Message: "Input should be a valid integer"})
	return 0, false
}

func digits(text string) bool {
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

// body decodes the request's JSON object into value, refusing a field the
// route does not take: a caller that sends `content` to the save, or the old
// extension envelope to the add, is told so instead of having it dropped.
func (x *exchange) body(value any) bool {
	raw, err := io.ReadAll(io.LimitReader(x.ctx.BodyReader(), maxBodyBytes+1))
	if err != nil {
		x.invalid(errorenvelope.Failure{Location: []string{"body"}, Message: "The body could not be read"})
		return false
	}
	if len(raw) > maxBodyBytes {
		x.refuse(http.StatusRequestEntityTooLarge, "The body is larger than this route accepts")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		x.invalid(errorenvelope.Failure{Location: []string{"body"}, Message: "The body should be a JSON object of the documented shape: " + err.Error()})
		return false
	}
	return true
}

func (x *exchange) invalid(failures ...errorenvelope.Failure) {
	x.routes.Errors.WriteValidation(x.response, x.requestID, failures)
}

func (x *exchange) refuse(status int, message string) {
	x.routes.Errors.Write(x.response, status, message, x.requestID)
}

func (x *exchange) send(status int, value any) {
	x.response.Header().Set("Content-Type", "application/json")
	x.response.Header().Set("Cache-Control", "private, no-store")
	x.response.WriteHeader(status)
	_ = json.NewEncoder(x.response).Encode(value)
}

// fail logs the cause and answers the 500. A cancelled request is not a
// fault and is not logged as one.
func (x *exchange) fail(reason string, cause error) {
	if !errors.Is(cause, context.Canceled) {
		attributes := []any{slog.String("request_id", x.requestID), slog.String("reason", reason)}
		if cause != nil {
			attributes = append(attributes, slog.String("error", cause.Error()))
		}
		x.routes.Logger.ErrorContext(x.ctx.Context(), "a document library route could not be served", attributes...)
	}
	x.refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}

// logContext is what a queued job carries for its log lines.
func (x *exchange) logContext() produce.LogContext {
	return produce.LogContext{RequestID: x.requestID, UserID: x.userID}
}
