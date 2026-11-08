package markdownmemory

import (
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
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
