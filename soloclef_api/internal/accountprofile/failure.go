package accountprofile

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// writeFailure is this slice's 500, and it is **always** the one without CORS
// headers.
//
// app/routes/users_routes.py has one `except`, and it is the delete's own
// `except Exception` around the enqueue — which does not become a 500 but a
// 503 `HTTPException`, so it is answered by ExceptionMiddleware *with* the
// headers, and this slice's 503 goes out through errorenvelope.Write like any
// other refusal. Every other failure of these three routes is uncaught, so
// ServerErrorMiddleware answers it from outside CORSMiddleware and the
// response carries no Access-Control-* headers at all
// (internal/corsmirror.Suppress; custom/docs/go-api-tier.md, divergence 5).
func writeFailure(ctx huma.Context, errors errorenvelope.Writer, logger *slog.Logger,
	response http.ResponseWriter, requestID, reason string, cause error, message string) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	logger.LogAttrs(ctx.Context(), slog.LevelError, message, attributes...)
	corsmirror.Suppress(ctx.Context())
	errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}

// requireSession is require_session_context, which gates the update and the
// delete and not the read: a leaked API key must not be able to rename or
// destroy the account it was issued from, and the Python route says so in a
// comment on the delete.
//
// Being a dependency decides when it runs. FastAPI solves a route's
// dependencies before it validates the route's own body, so an API key with a
// body that would not validate is told 403 and never 422 — which is why the
// update calls this before reporting its body's failures.
func requireSession(ctx huma.Context, errors errorenvelope.Writer, logger *slog.Logger,
	response http.ResponseWriter, requestID, message string) (principal.Principal, bool) {
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		writeFailure(ctx, errors, logger, response, requestID,
			"the route ran without an authenticated caller", nil, message)
		return principal.Principal{}, false
	}
	if !caller.IsSession() {
		errors.Write(response, http.StatusForbidden, sessionRequired, requestID)
		return principal.Principal{}, false
	}
	return caller, true
}
