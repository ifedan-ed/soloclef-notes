package folders

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// failures writes this slice's two kinds of 500, and the difference between
// them is which Starlette middleware would have written the Python tier's.
//
// Eight of the nine routes wrap their body in `except Exception` and re-raise
// an HTTPException, which ExceptionMiddleware answers from *inside*
// CORSMiddleware — so those 500s carry the CORS headers. Two cases do not:
//
//   - stop_watching_folder has **no try/except at all**, so every 500 of its
//     own is answered by the outermost ServerErrorMiddleware, with no CORS
//     headers. It is the one route in this slice that is wholly of the second
//     kind.
//   - validating the response against FolderRead happens after the route has
//     returned, so a `metadata` column holding something other than an object
//     is the second kind even on a route of the first. That is reachable — the
//     column is unconstrained — and it is in the comparison matrix.
//
// Both were measured with one `Origin` request per route against the running
// tier rather than read off the code.
type failures struct {
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
	Message string
	// NoCORSOnFailure is true for the route whose Python counterpart catches
	// nothing of its own.
	NoCORSOnFailure bool
}

// fail is the 500 the route itself decided on: the real reason stays in the log
// and never goes outward (app.exceptions.GENERIC_5XX_MESSAGE).
func (f failures) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	f.log(ctx, requestID, reason, cause)
	if f.NoCORSOnFailure {
		corsmirror.Suppress(ctx.Context())
	}
	f.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}

// failWhileRendering is the same 500 with the CORS headers suppressed whatever
// the route is, because a failure to render happens after the route returned.
func (f failures) failWhileRendering(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	f.log(ctx, requestID, reason, cause)
	corsmirror.Suppress(ctx.Context())
	f.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}

func (f failures) log(ctx huma.Context, requestID, reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	f.Logger.LogAttrs(ctx.Context(), slog.LevelError, f.Message, attributes...)
}
