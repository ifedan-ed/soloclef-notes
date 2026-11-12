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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// UpdateRoute serves PUT /api/v1/prompts/{prompt_id} (update_prompt).
//
// Its refusals are in a fixed order and the order is load-bearing: the absent
// prompt before the publishing check, because the check reads `prompt.is_public`
// off the row it just loaded. A request that both names a prompt the caller
// does not own and asks to publish it is a 404 on both tiers.
type UpdateRoute struct {
	Pool   *pgxpool.Pool
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r UpdateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "update-prompt",
		Method:      http.MethodPut,
		Path:        UpdatePath,
		Summary:     "Change one of the caller's own prompts",
		Description: "The same response as the Python tier's " +
			"PUT /api/v1/prompts/{prompt_id}, byte for byte, and the same row.",
		Tags: []string{"prompts"},
	}, func(ctx context.Context, _ *struct {
		PromptID string `path:"prompt_id" doc:"The prompt's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r UpdateRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	changes, bodyFailures, decodeErr := ReadChanges(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		r.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}

	// The dependency before the parameters, as create_prompt's is.
	caller, ok := requireSession(ctx, r.Errors, r.Logger, response, requestID, updateUnserved)
	if !ok {
		return
	}

	// The path's failure and the body's are reported together, the path's
	// first: FastAPI validates a route's path parameters before its body and
	// raises one RequestValidationError carrying both.
	id, pathFailure, rangeErr := promptID(ctx)
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

	// The lookup, the publishing check and the write are one transaction: the
	// row the refusal was decided from is the row written.
	type outcome struct {
		prompt  Prompt
		refusal *refusal
	}
	result, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (outcome, error) {
			stored, err := PromptOf(ctx, tx, caller.UserID, id)
			if err != nil {
				return outcome{}, err
			}
			if stored == nil {
				return outcome{refusal: &refusal{
					Status: http.StatusNotFound, Message: promptAbsent}}, nil
			}
			if changes.AsksToPublish(stored.IsPublic) {
				administrator, err := MayPublish(ctx, tx, caller.UserID)
				if err != nil {
					return outcome{}, err
				}
				if !administrator {
					return outcome{refusal: &refusal{
						Status: http.StatusForbidden, Message: publishingRefused}}, nil
				}
			}
			if !changes.Mentions() {
				// The Python route has no changed attribute to flush, so it
				// emits no UPDATE at all and answers with the row as it
				// stands — and the version is not bumped.
				return outcome{prompt: *stored}, nil
			}
			written, err := Update(ctx, tx, caller.UserID, id, changes)
			return outcome{prompt: written}, err
		})
	if err != nil {
		r.fail(ctx, response, requestID, "could not update the prompt", err)
		return
	}
	if result.refusal != nil {
		r.Errors.Write(response, result.refusal.Status, result.refusal.Message, requestID)
		return
	}

	body, err := pythonjson.Marshal(Build(result.prompt))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the prompt", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r UpdateRoute) fail(ctx huma.Context, response http.ResponseWriter,
	requestID, reason string, cause error) {
	writeFailure(ctx, r.Errors, r.Logger, response, requestID, reason, cause, updateUnserved)
}
