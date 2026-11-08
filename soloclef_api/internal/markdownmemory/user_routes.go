package markdownmemory

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// The three routes an account's own memory is reached by.
const (
	UserReadPath  = "/api/v1/users/me/memory"
	UserWritePath = "/api/v1/users/me/memory"
	UserResetPath = "/api/v1/users/me/memory/reset"
)

// sessionRequired is require_session_context's refusal, word for word: these
// routes are the account's own memory, so an API key may not use them.
const sessionRequired = "This action requires an interactive session"

// UserRoutes serves the account's own memory.
type UserRoutes struct {
	// Pool is taken as well as the store because a write reads the stored
	// document and replaces it in one transaction.
	Pool   *pgxpool.Pool
	Memory Store
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r UserRoutes) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-user-memory",
		Method:      http.MethodGet,
		Path:        UserReadPath,
		Summary:     "The caller's own memory document",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/users/me/memory, byte for byte.",
		Tags: []string{"memory"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveRead}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "write-user-memory",
		Method:      http.MethodPut,
		Path:        UserWritePath,
		Summary:     "Replace the caller's own memory document",
		Description: "The same response as the Python tier's " +
			"PUT /api/v1/users/me/memory, byte for byte, and the same column.",
		Tags: []string{"memory"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveWrite}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "reset-user-memory",
		Method:      http.MethodPost,
		Path:        UserResetPath,
		Summary:     "Empty the caller's own memory document",
		Description: "The same response as the Python tier's " +
			"POST /api/v1/users/me/memory/reset, byte for byte.",
		Tags: []string{"memory"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveReset}, nil
	})
}

func (r UserRoutes) serveRead(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, ok := r.session(ctx, response, requestID)
	if !ok {
		return
	}
	memory, err := r.Memory.UserMemory(ctx.Context(), caller.UserID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's memory", err)
		return
	}
	r.send(ctx, response, requestID, memory)
}

func (r UserRoutes) serveWrite(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	requested, failures, decodeErr := ReadRequest(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		r.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}

	caller, ok := r.session(ctx, response, requestID)
	if !ok {
		return
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}
	r.save(ctx, response, requestID, caller.UserID, requested)
}

func (r UserRoutes) serveReset(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, ok := r.session(ctx, response, requestID)
	if !ok {
		return
	}
	// reset_memory is save_memory with an empty document, which is the whole of
	// it there too.
	r.save(ctx, response, requestID, caller.UserID, "")
}

func (r UserRoutes) save(ctx huma.Context, response http.ResponseWriter, requestID, userID, requested string) {
	outcome, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (Outcome, error) {
			stored, err := StoredUserMemory(ctx, tx, userID)
			if err != nil {
				return Outcome{}, err
			}
			outcome := Save(UserScope, requested, stored)
			if !outcome.Write {
				return outcome, nil
			}
			return outcome, WriteUserMemory(ctx, tx, userID, outcome.Store)
		})
	if err != nil {
		r.fail(ctx, response, requestID, "could not write the caller's memory", err)
		return
	}
	if outcome.Refusal != "" {
		r.Errors.Write(response, http.StatusBadRequest, outcome.Refusal, requestID)
		return
	}
	r.send(ctx, response, requestID, outcome.Answer)
}

func (r UserRoutes) send(ctx huma.Context, response http.ResponseWriter, requestID, memory string) {
	body, err := pythonjson.Marshal(Build(memory))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the memory document", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// session is require_session_context.
func (r UserRoutes) session(ctx huma.Context, response http.ResponseWriter, requestID string) (principal.Principal, bool) {
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return principal.Principal{}, false
	}
	if !caller.IsSession() {
		r.Errors.Write(response, http.StatusForbidden, sessionRequired, requestID)
		return principal.Principal{}, false
	}
	return caller, true
}

func (r UserRoutes) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	logFailure(ctx, r.Logger, requestID, reason, cause, "the memory document could not be served")
	// app/routes/memory_routes.py catches nothing, so every failure here is
	// one Starlette's ServerErrorMiddleware would have written — and that
	// middleware is outside CORSMiddleware, so the 500 carries no CORS
	// headers (internal/corsmirror.Suppress).
	corsmirror.Suppress(ctx.Context())
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
