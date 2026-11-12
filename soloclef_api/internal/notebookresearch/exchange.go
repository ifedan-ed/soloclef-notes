package notebookresearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

type notebookPath struct {
	NotebookID string `path:"notebook_id"`
}

// Register mounts the routes.
func (r Routes) Register(api huma.API) {
	mount(api, r, http.MethodGet, SourcesPath, "research-sources", "Which research sources this account can search", r.sources)
	mount(api, r, http.MethodPost, SearchPath, "research-search", "Search for new sources, as server-sent events", r.search)
	mount(api, r, http.MethodPost, ReadPagePath, "research-read-page", "Add one web page as a source", r.readPage)
	mount(api, r, http.MethodPost, ImportPath, "research-import", "Add picked results as sources", r.importResults)
}

// exchange is one request being answered.
type exchange struct {
	ctx        huma.Context
	response   http.ResponseWriter
	requestID  string
	userID     string
	notebookID int64
	routes     Routes
}

func mount(api huma.API, r Routes, method, path, id, summary string, serve func(*exchange)) {
	huma.Register(api, huma.Operation{
		OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"notebooks"},
	}, func(_ context.Context, _ *notebookPath) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			x := &exchange{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context()), routes: r}
			caller, ok := principal.FromContext(ctx.Context())
			if !ok {
				x.fail("the route ran without a caller", nil)
				return
			}
			x.userID = caller.UserID
			// The path's id: a whole number, or a 422. One too wide for a
			// bigint names no notebook and reads as 0, which no row has.
			text := ctx.Param("notebook_id")
			if text == "" || strings.Trim(text, "0123456789") != "" {
				x.invalid([]string{"path", "notebook_id"}, "Input should be a valid integer")
				return
			}
			x.notebookID, _ = strconv.ParseInt(text, 10, 64)
			serve(x)
		}}, nil
	})
}

// owned reports whether the notebook is the caller's, answering the 404 (or
// the 500) when it is not. The two routes that write prove it again inside
// their transaction; the two that only read ask here.
func (x *exchange) owned() bool {
	owned, err := New(x.routes.Pool).NotebookIsOwned(x.ctx.Context(), NotebookIsOwnedParams{NotebookID: x.notebookID, UserID: x.userID})
	if err != nil {
		x.fail("could not look for the notebook", err)
		return false
	}
	if !owned {
		x.refuse(http.StatusNotFound, notebookAbsent)
	}
	return owned
}

// body decodes the request's JSON body into value, or answers the 422. A
// megabyte is far more than fifty results with their snippets.
func (x *exchange) body(value any) bool {
	raw, err := io.ReadAll(io.LimitReader(x.ctx.BodyReader(), 1<<20))
	if err == nil {
		err = json.Unmarshal(raw, value)
	}
	if err != nil {
		x.invalid([]string{"body"}, "The body should be a JSON object of the documented shape")
		return false
	}
	return true
}

func (x *exchange) invalid(location []string, message string) {
	x.routes.Errors.WriteValidation(x.response, x.requestID, []errorenvelope.Failure{{Location: location, Message: message}})
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
		x.routes.Logger.ErrorContext(x.ctx.Context(), "a notebook research route could not be served", attributes...)
	}
	x.refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}
