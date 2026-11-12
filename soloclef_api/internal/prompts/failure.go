package prompts

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// writeFailure is this slice's 500, and it is **always** the one without CORS
// headers.
//
// app/routes/prompts_routes.py has no `except Exception` anywhere in it, so no
// failure of any of its six routes is answered by Starlette's
// ExceptionMiddleware. Every one reaches ServerErrorMiddleware, which is the
// outermost middleware of all and therefore outside CORSMiddleware, so the
// response carries no Access-Control-* headers at all
// (internal/corsmirror.Suppress; custom/docs/go-api-tier.md, divergence 5).
//
// That is one rule for the whole slice rather than a decision per route, so
// there is one helper.
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
