package workspacelogs

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// DeletePath removes one log (app/routes/logs_routes.py:delete_log).
const DeletePath = "/api/v1/logs/{log_id}"

// deletedMessage is the body the route answers with. It declares no
// response_model, so the dict is rendered by jsonable_encoder and Starlette's
// own json.dumps rather than by pydantic.
const deletedMessage = "Log deleted successfully"

// DeleteRoute serves the removal. It is the one route of the four that asks for
// logs:delete rather than logs:read.
type DeleteRoute struct {
	Pool   *pgxpool.Pool
	Access Access
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r DeleteRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-log",
		Method:      http.MethodDelete,
		Path:        DeletePath,
		Summary:     "Remove one log entry",
		Description: "The same response as the Python tier's " +
			"DELETE /api/v1/logs/{log_id}, byte for byte, and the same row gone.",
		Tags: []string{"logs"},
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

	refusal, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (*workspaceaccess.Refusal, error) {
			stored, err := EntryIn(ctx, tx, caller.UserID, logID)
			if err != nil {
				return nil, err
			}
			if stored == nil {
				return &workspaceaccess.Refusal{
					Status: http.StatusNotFound, Message: logAbsent}, nil
			}
			refusal, err := checkGates(ctx, workspaceaccess.TransactionReader{Tx: tx}, caller,
				stored.WorkspaceID, workspaceaccess.LogsDelete, deleteDenied)
			if err != nil || refusal != nil {
				return refusal, err
			}
			return nil, Delete(ctx, tx, logID)
		})
	if err != nil {
		r.fail(ctx, response, requestID, "could not delete the log", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
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
