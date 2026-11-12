package accountprofile

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// DeleteRoute serves DELETE /users/me (delete_current_user_account).
//
// It has the mark-commit-enqueue-and-revert shape the document delete has:
// the account is deactivated and every
// sign-in ended, that is committed, and *then* the erasure is enqueued —
// because the task reads the row it is about. If the enqueue fails, `is_active`
// goes back and the answer is 503, since an account locked out of a deletion
// that never runs is worse than one that is still signed in. That is not a
// `database.Effects` case: an effect runs after a commit and cannot fail the
// request, and this one has to.
//
// The row deletion itself is the task's. This route erases nothing.
type DeleteRoute struct {
	Pool *pgxpool.Pool
	// Queue is the broker the erasure is published on.
	Queue celeryqueue.Enqueuer
	// Cookies is `clear_session`, which writes the two Set-Cookie headers that
	// end the browser's own session beside the server's
	// (session_cookies.go).
	Cookies CookiePolicy
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
	// Now is the clock the revocation stamp and the cookies' expiry come from.
	// Overridable for tests.
	Now func() time.Time
}

func (r DeleteRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-own-account",
		Method:      http.MethodDelete,
		Path:        DeletePath,
		Summary:     "Lock the caller's own account out and queue its erasure",
		Description: "The same response as the Python tier's " +
			"DELETE /users/me, byte for byte, the same rows, and the " +
			"same message queued.",
		Tags: []string{"users"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r DeleteRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	// Session-only, and the Python route says why in a comment: a leaked
	// personal access token must not be able to destroy the account it was
	// issued from.
	caller, ok := requireSession(ctx, r.Errors, r.Logger, response, requestID, deleteUnserved)
	if !ok {
		return
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	at := now().UTC()

	if _, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
			return struct{}{}, LockOut(ctx, tx, caller.UserID, at)
		}); err != nil {
		r.fail(ctx, response, requestID, "could not deactivate the caller's account", err)
		return
	}

	// After the commit, as there: the task reads the account row it is about,
	// and the argument is `str(auth.user.id)` — a string and not a uuid,
	// because that is what the Python call site passes.
	if _, err := r.Queue.Enqueue(ctx.Context(), ErasureOf(caller.UserID,
		celeryqueue.LogContextFor(pathOf(ctx), requestID, caller.UserID))); err != nil {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError,
			"could not queue an account's erasure",
			slog.String("request_id", requestID),
			slog.String("error", err.Error()))
		// The Python route's own compensation. Only `is_active` goes back: the
		// refresh tokens stay revoked and the push registrations stay gone, as
		// they do there, so the account is signed in nowhere and can sign in
		// again.
		if _, revertErr := database.Write(ctx.Context(), r.Pool,
			func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
				return struct{}{}, Reactivate(ctx, tx, caller.UserID)
			}); revertErr != nil {
			r.fail(ctx, response, requestID,
				"could not reactivate an account whose erasure was not queued", revertErr)
			return
		}
		r.Errors.Write(response, http.StatusServiceUnavailable, queueFailed, requestID)
		return
	}

	// `clear_session(response, request)` runs after the enqueue, so a request
	// that answers 503 carries no Set-Cookie at all — which is the Python
	// order and not an obvious one: the account is deactivated either way.
	r.Cookies.Clear(response, ctx.Header("X-Forwarded-Proto"), at)
	pythonjson.SendNoContent(response)
}

// ErasureOf is the Celery message `erase_account_task.delay(str(user_id))`
// publishes: one positional string argument, the publishing request's log
// context, and nothing else, so every other option is Celery's default.
func ErasureOf(userID string, context produce.LogContext) produce.Message {
	message := produce.Delay(EraseTask, userID)
	message.Context = context
	return message
}

// pathOf is the request's own path, which the log-context header is built
// from: for this route it carries the request id and the account and nothing
// else (internal/celeryqueue.LogContextFor).
func pathOf(ctx huma.Context) string {
	asked := ctx.URL()
	return asked.Path
}

func (r DeleteRoute) fail(ctx huma.Context, response http.ResponseWriter,
	requestID, reason string, cause error) {
	writeFailure(ctx, r.Errors, r.Logger, response, requestID, reason, cause, deleteUnserved)
}
