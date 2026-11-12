package modelconnections

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// writeFailure is this slice's 500, and it is always the one without CORS
// headers.
//
// A failed statement and a column that cannot be rendered are answered alike
// (internal/corsmirror.Suppress).
//
// The log line carries a reason and the error, and never a row: a row of this
// slice still holds `extra`, which may hold a provider's credentials (package
// comment, "The key"). The errors that reach here are a driver's or
// errNotAnObject, neither of which quotes one.
func writeFailure(ctx huma.Context, errors errorenvelope.Writer, logger *slog.Logger,
	response http.ResponseWriter, requestID, message, reason string, cause error) {
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

// noCaller is the reason logged when a route runs with no caller, which the
// gate makes unreachable.
const noCaller = "the route ran without an authenticated caller"
