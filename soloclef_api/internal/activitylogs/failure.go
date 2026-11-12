package activitylogs

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// logFailure writes the one line every route here writes before its sanitised
// 500: the real reason stays in the log and never goes outward
// (app.exceptions.GENERIC_5XX_MESSAGE).
func logFailure(ctx huma.Context, logger *slog.Logger, requestID, reason string, cause error, message string) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	logger.LogAttrs(ctx.Context(), slog.LevelError, message, attributes...)
}

// failWhileRendering is the same 500 without the CORS headers, and the
// difference is Starlette's two error handlers.
//
// app/routes/logs_routes.py wraps each route's body in `except Exception` and
// re-raises an HTTPException, which ExceptionMiddleware answers from *inside*
// CORSMiddleware — so fail() leaves the headers alone. Validating the response
// against LogRead happens after the route has returned, so nothing catches a
// failure there and Starlette's outermost ServerErrorMiddleware answers it from
// *outside* CORSMiddleware, with no CORS headers at all. A log_metadata column
// holding something other than an object is exactly that case, and it is
// reachable (internal/corsmirror.Suppress).
func failWhileRendering(ctx huma.Context, logger *slog.Logger, errors errorenvelope.Writer,
	response http.ResponseWriter, requestID, reason string, cause error, message string) {
	logFailure(ctx, logger, requestID, reason, cause, message)
	corsmirror.Suppress(ctx.Context())
	errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
