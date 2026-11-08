package workspacelogs

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// ReadPath is one log by its id (app/routes/logs_routes.py:read_log).
const ReadPath = "/api/v1/logs/{log_id}"

// Logs reads the stored logs.
type Logs interface {
	Entry(ctx context.Context, callerID string, logID int64) (*Entry, error)
}

// ReadRoute serves the read.
type ReadRoute struct {
	Logs   Logs
	Access Access
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r ReadRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-log",
		Method:      http.MethodGet,
		Path:        ReadPath,
		Summary:     "One log entry",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/logs/{log_id}, byte for byte.",
		Tags: []string{"logs"},
	}, func(ctx context.Context, _ *struct {
		LogID string `path:"log_id" doc:"The log entry's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ReadRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// read if the wiring is ever wrong.
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}

	logID, failure, rangeErr := parseLogID(ctx)
	if failure != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{*failure})
		return
	}
	if rangeErr != nil {
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", rangeErr)
		return
	}

	// The caller's membership is part of the lookup, as it is in the Python
	// route's _authorised_log, so a log of a workspace the caller is not in is
	// reported absent rather than refused.
	entry, err := r.Logs.Entry(ctx.Context(), caller.UserID, logID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the log", err)
		return
	}
	if entry == nil {
		r.Errors.Write(response, http.StatusNotFound, logAbsent, requestID)
		return
	}

	refusal, err := checkGates(ctx.Context(), r.Access, caller,
		entry.WorkspaceID, workspaceaccess.LogsRead, readDenied)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's standing in the workspace", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	rendered, err := Build(*entry)
	if err != nil {
		failWhileRendering(ctx, r.Logger, r.Errors, response, requestID,
			"could not render the log", err, "the log could not be served")
		return
	}
	body, err := marshal(rendered)
	if err != nil {
		failWhileRendering(ctx, r.Logger, r.Errors, response, requestID,
			"could not render the log", err, "the log could not be served")
		return
	}
	pythonjson.Send(response, http.StatusOK, []byte(body))
}

func (r ReadRoute) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	logFailure(ctx, r.Logger, requestID, reason, cause, "the log could not be served")
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
