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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// CreateRoute serves POST /api/v1/prompts (create_prompt).
type CreateRoute struct {
	Pool   *pgxpool.Pool
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is the clock created_at comes from, as the Python model's default is
	// evaluated in the application. Overridable for tests.
	Now func() time.Time
}

func (r CreateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-prompt",
		Method:      http.MethodPost,
		Path:        CreatePath,
		Summary:     "Write a prompt",
		Description: "A new prompt of the signed-in account's own, answered as stored.",
		Tags:        []string{"prompts"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r CreateRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	requested, failures, decodeErr := ReadRequest(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		// The middleware forwards such a body before the gate, so reaching
		// here means the two disagreed.
		r.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}

	// The session is checked before the body's own failures, because
	// require_session_context is a *dependency* and FastAPI solves a route's
	// dependencies before it validates the route's parameters. So an API key
	// with a body that would not validate is told 403 and never 422 — asked of
	// the running tier, the same way the account's own memory routes were
	// (internal/markdownmemory).
	caller, ok := requireSession(ctx, r.Errors, r.Logger, response, requestID, createUnserved)
	if !ok {
		return
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}

	created, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (Prompt, error) {
			return Insert(ctx, tx, caller.UserID, requested, now().UTC())
		})
	if err != nil {
		r.fail(ctx, response, requestID, "could not write the prompt", err)
		return
	}

	body, err := pythonjson.Marshal(Build(created))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the prompt", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r CreateRoute) fail(ctx huma.Context, response http.ResponseWriter,
	requestID, reason string, cause error) {
	writeFailure(ctx, r.Errors, r.Logger, response, requestID, reason, cause, createUnserved)
}
