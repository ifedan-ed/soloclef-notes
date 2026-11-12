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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// DeletePath removes one log.
const DeletePath = "/api/v1/logs/{log_id}"

// deletedMessage is the body the route answers with.
const deletedMessage = "Log deleted successfully"

// DeleteRoute serves the removal.
type DeleteRoute struct {
	Logs   Logs
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r DeleteRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-log",
		Method:      http.MethodDelete,
		Path:        DeletePath,
		Summary:     "Remove one log entry",
		Description: "Removes one entry of the caller's own activity log.",
		Tags:        []string{"logs"},
	}, func(ctx context.Context, _ *struct {
		LogID string `path:"log_id" doc:"The log entry's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r DeleteRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
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

	removed, err := r.Logs.Delete(ctx.Context(), caller.UserID, logID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not delete the log", err)
		return
	}
	if !removed {
		r.Errors.Write(response, http.StatusNotFound, logAbsent, requestID)
		return
	}

	rendered := pythonjson.NewObject()
	rendered.Set("message", deletedMessage)
	body, err := marshal(rendered)
	if err != nil {
		failWhileRendering(ctx, r.Logger, r.Errors, response, requestID,
			"could not render the answer", err, "the log could not be deleted")
		return
	}
	pythonjson.Send(response, http.StatusOK, []byte(body))
}

func (r DeleteRoute) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	logFailure(ctx, r.Logger, requestID, reason, cause, "the log could not be deleted")
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
