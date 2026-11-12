package prompts

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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// DeleteRoute serves DELETE /api/v1/prompts/{prompt_id} (delete_prompt).
//
// It is the one route of the six with no response_model, so its body is
// rendered by jsonable_encoder and Starlette's own json.dumps rather than by
// pydantic — which matters for nothing in `{"success":true}` and is recorded
// because the five routes beside it are the other way
// (internal/pythonjson).
type DeleteRoute struct {
	Pool   *pgxpool.Pool
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r DeleteRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-prompt",
		Method:      http.MethodDelete,
		Path:        DeletePath,
		Summary:     "Remove one of the caller's own prompts",
		Description: "The same response as the Python tier's " +
			"DELETE /api/v1/prompts/{prompt_id}, byte for byte, and the same " +
			"row gone.",
		Tags: []string{"prompts"},
	}, func(ctx context.Context, _ *struct {
		PromptID string `path:"prompt_id" doc:"The prompt's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r DeleteRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, ok := requireSession(ctx, r.Errors, r.Logger, response, requestID, deleteUnserved)
	if !ok {
		return
	}

	id, failure, rangeErr := promptID(ctx)
	if failure != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{*failure})
		return
	}
	if rangeErr != nil {
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", rangeErr)
		return
	}

	refusal, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (*refusal, error) {
			stored, err := PromptOf(ctx, tx, caller.UserID, id)
			if err != nil {
				return nil, err
			}
			if stored == nil {
				// The owner is part of the lookup, so another account's prompt
				// and a prompt that was never written are the same answer.
				return &refusal{
					Status: http.StatusNotFound, Message: promptAbsent}, nil
			}
			return nil, Delete(ctx, tx, caller.UserID, id)
		})
	if err != nil {
		r.fail(ctx, response, requestID, "could not delete the prompt", err)
		return
	}
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}

	body, err := pythonjson.Marshal(deleted())
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the answer", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r DeleteRoute) fail(ctx huma.Context, response http.ResponseWriter,
	requestID, reason string, cause error) {
	writeFailure(ctx, r.Errors, r.Logger, response, requestID, reason, cause, deleteUnserved)
}
