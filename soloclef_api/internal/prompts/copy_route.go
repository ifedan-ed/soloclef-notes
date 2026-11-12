package prompts

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// CopyRoute serves POST /api/v1/prompts/{prompt_id}/copy
// (copy_public_prompt): the community shelf's "take a copy" button.
//
// What the copy owns is the whole of the route, and it is the one thing to get
// right here. The source is looked up by `is_public` and **not** by its owner,
// so any account may copy any published prompt — including one of their own,
// which neither tier excludes. The copy is then the caller's: their user_id,
// `is_public` false whatever the source was, and version 1 even when the
// source is at 7. So copying cannot republish anything.
//
// It takes no body at all: a prompt id in the path and nothing else, so there
// is nothing for internal/requestbody to buffer and nothing to forward.
type CopyRoute struct {
	Pool   *pgxpool.Pool
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is the clock the copy's created_at comes from. Overridable for tests.
	Now func() time.Time
}

func (r CopyRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "copy-public-prompt",
		Method:      http.MethodPost,
		Path:        CopyPath,
		Summary:     "Take a copy of a published prompt",
		Description: "The same response as the Python tier's " +
			"POST /api/v1/prompts/{prompt_id}/copy, byte for byte, and the " +
			"same row.",
		Tags: []string{"prompts"},
	}, func(ctx context.Context, _ *struct {
		PromptID string `path:"prompt_id" doc:"The published prompt's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r CopyRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, ok := requireSession(ctx, r.Errors, r.Logger, response, requestID, copyUnserved)
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

	now := r.Now
	if now == nil {
		now = time.Now
	}

	type outcome struct {
		copied  Prompt
		refusal *refusal
	}
	result, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (outcome, error) {
			source, err := PublishedPrompt(ctx, tx, id)
			if err != nil {
				return outcome{}, err
			}
			if source == nil {
				// A prompt that exists but is not published is "Prompt not
				// found", the same bytes as one that was never written, so the
				// route is not an oracle for anyone's private prompts.
				return outcome{refusal: &refusal{
					Status: http.StatusNotFound, Message: promptAbsent}}, nil
			}
			copied, err := Copy(ctx, tx, caller.UserID, *source, now().UTC())
			return outcome{copied: copied}, err
		})
	if err != nil {
		r.fail(ctx, response, requestID, "could not copy the prompt", err)
		return
	}
	if result.refusal != nil {
		r.Errors.Write(response, result.refusal.Status, result.refusal.Message, requestID)
		return
	}

	body, err := pythonjson.Marshal(Build(result.copied))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the prompt", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r CopyRoute) fail(ctx huma.Context, response http.ResponseWriter,
	requestID, reason string, cause error) {
	writeFailure(ctx, r.Errors, r.Logger, response, requestID, reason, cause, copyUnserved)
}
