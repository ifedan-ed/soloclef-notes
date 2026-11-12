package activitylogs

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// UpdatePath is the partial update of one log.
const UpdatePath = "/api/v1/logs/{log_id}"

// UpdateRoute serves the partial update.
type UpdateRoute struct {
	Logs   Logs
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r UpdateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "update-log",
		Method:      http.MethodPut,
		Path:        UpdatePath,
		Summary:     "Change one log entry",
		Description: "Changes the fields the body names on one entry of the " +
			"caller's own activity log.",
		Tags: []string{"logs"},
	}, func(ctx context.Context, _ *struct {
		LogID string `path:"log_id" doc:"The log entry's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r UpdateRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}

	logID, pathFailure, rangeErr := parseLogID(ctx)
	changes, bodyFailures, decodeErr := ReadChanges(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		r.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}
	failures := make([]errorenvelope.Failure, 0, 1+len(bodyFailures))
	if pathFailure != nil {
		failures = append(failures, *pathFailure)
	}
	failures = append(failures, bodyFailures...)
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}
	if rangeErr != nil {
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", rangeErr)
		return
	}

	// One statement, with the owner in its predicate: no row back means the
	// log is not the caller's or is not there, and the two are one answer.
	updated, err := r.Logs.Update(ctx.Context(), caller.UserID, logID, changes)
	if err != nil {
		r.fail(ctx, response, requestID, "could not update the log", err)
		return
	}
	if updated == nil {
		r.Errors.Write(response, http.StatusNotFound, logAbsent, requestID)
		return
	}

	rendered, err := Build(*updated)
	if err != nil {
		failWhileRendering(ctx, r.Logger, r.Errors, response, requestID,
			"could not render the log", err, "the log could not be updated")
		return
	}
	body, err := marshal(rendered)
	if err != nil {
		failWhileRendering(ctx, r.Logger, r.Errors, response, requestID,
			"could not render the log", err, "the log could not be updated")
		return
	}
	pythonjson.Send(response, http.StatusOK, []byte(body))
}

func (r UpdateRoute) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	logFailure(ctx, r.Logger, requestID, reason, cause, "the log could not be updated")
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
