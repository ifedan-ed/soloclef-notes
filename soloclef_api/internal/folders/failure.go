package folders

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// failures writes a route's 500: the real reason goes to the log with the
// request id, and the caller is told only that something went wrong. The
// answer is an ordinary error envelope with the CORS headers every other
// answer carries, so the web app can read it and show the request id.
type failures struct {
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
	Message string
}

func (f failures) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	f.Logger.LogAttrs(ctx.Context(), slog.LevelError, f.Message, attributes...)
	f.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
