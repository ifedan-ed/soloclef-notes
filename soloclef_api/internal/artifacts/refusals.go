package artifacts

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// Refusal is an answer a route gives instead of its result: a status and
// the sentence the caller is shown.
type Refusal struct {
	Status  int
	Message string
}

// number is one integer parameter: a value, or the fact that it is wider than
// any integer this tier can carry. The second is kept apart from the 422s
// because it *is* a whole number; it simply names no row, and the statement
// that would have carried it is where the route stops.
type number struct {
	value   int64
	tooWide bool
}

// readNumber reads one parameter as FastAPI coerces it. A value that is not a
// whole number is appended to failures, in the caller's order, because FastAPI
// reports every bad parameter of a request at once.
func readNumber(text string, failures *[]errorenvelope.Failure, location ...string) number {
	value, failure, rangeErr := requestnumber.Parse(text, location...)
	if failure != nil {
		*failures = append(*failures, *failure)
	}
	return number{value: value, tooWide: rangeErr != nil}
}

// writeFailure logs what went wrong and answers the tier's sanitised 500.
// The CORS headers are left on it, so the web app can read the envelope and
// show its request id.
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
	errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}

// The reasons a parameter too wide to carry is refused with, named once
// because all three routes give them.
const (
	artifactTooWide = "the artifact id is wider than any statement can carry"
	threadTooWide   = "the thread id is wider than any statement can carry"
	noCaller        = "the route ran without an authenticated caller"
)
