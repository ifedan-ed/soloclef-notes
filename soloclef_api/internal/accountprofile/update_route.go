package accountprofile

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

// UpdateRoute serves PATCH /users/me (update_current_user_profile).
//
// It owes nothing outside its own row: `UserManager.update(safe=True)` ends in
// `user_db.update`, and this deployment overrides none of fastapi-users'
// `on_after_update`, so there is no hook, no event, no capture and no mail.
// The branches of that method that would have mattered — the email change's
// uniqueness check and the password hash — are unreachable, because `UserUpdate`
// declares neither field and forbids every field it did not declare.
type UpdateRoute struct {
	Pool   *pgxpool.Pool
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r UpdateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "update-own-account",
		Method:      http.MethodPatch,
		Path:        UpdatePath,
		Summary:     "Change the caller's own profile",
		Description: "The same response as the Python tier's " +
			"PATCH /users/me, byte for byte, and the same row.",
		Tags: []string{"users"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r UpdateRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	changes, failures, decodeErr := ReadChanges(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		r.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}

	// The session is checked before the body's own failures, because
	// require_session_context is a *dependency* and FastAPI solves a route's
	// dependencies before it validates the route's parameters.
	caller, ok := requireSession(ctx, r.Errors, r.Logger, response, requestID, updateUnserved)
	if !ok {
		return
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}

	updated, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (Profile, error) {
			// A body that mentions nothing writes nothing: the Python session
			// has no changed attribute to flush, so it emits no UPDATE and
			// answers with the row as it stands.
			if !changes.Mentions() {
				return ProfileIn(ctx, tx, caller.UserID)
			}
			return Update(ctx, tx, caller.UserID, changes)
		})
	if err != nil {
		r.fail(ctx, response, requestID, "could not update the caller's account", err)
		return
	}

	body, err := pythonjson.Marshal(Build(updated))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the account", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r UpdateRoute) fail(ctx huma.Context, response http.ResponseWriter,
	requestID, reason string, cause error) {
	writeFailure(ctx, r.Errors, r.Logger, response, requestID, reason, cause, updateUnserved)
}
