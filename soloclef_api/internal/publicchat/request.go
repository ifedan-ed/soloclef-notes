package publicchat

import (
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/starlettepath"
)

// What the three routes share before any of them reads a row: the path
// parameters, read as Starlette and FastAPI read them, and the two answers
// that are not a route's own -- the router's 404 (internal/starlettepath) and
// the sanitised 500.

// integerTooLong is pydantic's refusal for an integer it gave up on for its
// length. It is a different sentence from errorenvelope.IntegerExpected, and
// integerFromText says which of the two a long value gets -- it is not as
// simple as "too many digits".
const integerTooLong = "Unable to parse input string as an integer, exceeded maximum size"

// routerNotFound is Starlette's bare 404, which is what answers a path
// parameter the Python router could not have matched as one segment. The
// body, the test for such a parameter, the answer and the decoding of a
// parameter are all internal/starlettepath's, because every route of this
// tier with a path parameter has the same two routers to reconcile; this
// name is kept for the recorded tables, which tell a router's answer from a
// route's by this body.
const routerNotFound = starlettepath.RouterNotFound

// pathInteger is one `int` path parameter as the Python route holds it: an
// integer of any width, because Python's are unbounded.
//
// Two of the three places these routes use one never reach a statement -- a
// podcast id is only ever compared with what the snapshot holds, and an
// artifact id is compared with the allowlist first -- so a twenty-digit id is
// an ordinary "not found" and must not be a 500. Only where an id is handed
// to a statement does its width matter, and narrow says so there.
type pathInteger struct {
	value *big.Int
}

// narrow is the id as a statement can carry it, and false when it is wider
// than an int64. An id that fits an int64 and not the column is sent, and the
// database refuses it at that statement, as asyncpg does on the Python tier.
func (p pathInteger) narrow() (int64, bool) {
	if !p.value.IsInt64() {
		return 0, false
	}
	return p.value.Int64(), true
}

// readPathInteger reads one parameter as FastAPI coerces it, appending the
// 422 field to failures when it is not an integer.
func readPathInteger(text string, failures *[]errorenvelope.Failure, name string) pathInteger {
	value, refusal := integerFromText(text)
	if refusal != "" {
		*failures = append(*failures, errorenvelope.Failure{
			Location: []string{"path", name},
			Message:  refusal,
		})
		return pathInteger{}
	}
	return pathInteger{value: value}
}

// integerFromText is pydantic-core's reading of a string as an `int`: the
// integer, or the sentence it refuses the string with.
//
// What is an integer at all is internal/requestnumber.WholeNumber, the mirror
// of `str_as_int` every path parameter of this tier is read with. Two things
// are added here that function has no room for, because it answers digits or
// nothing: both are about length, and both were taken from pydantic itself
// (the `id_is_…` and `id_with_…` cases of testdata/publicchat_python.json).
//
// pydantic-core reads the string twice. The first pass takes it exactly as
// written, before even the whitespace around it is trimmed -- an optional
// minus and then digits, as a JSON number is spelled -- and
// gives up, with integerTooLong, the moment that run is longer than
// maxIntegerDigits, **whatever comes after it**: 4,301 ones followed by a
// letter are "exceeded maximum size", not "unable to parse". The minus
// counts, so 4,300 digits are an integer and the same digits negated are not.
// A run that begins with a zero is not a number to that pass, however long.
//
// Only if the first pass did not settle it is the string tidied -- the
// whitespace, a plus sign, leading zeros, a fraction of zeros and underscores
// removed -- and
// read again. That second pass has the same length limit, but its refusals
// are all reported as the ordinary one. So a value made too long *by what
// tidying leaves* -- "+" and 4,301 ones, or ten zeros and 4,301 ones -- is
// "unable to parse string as an integer", 4,301 zeros are the integer 0, and
// 4,301 ones are one refusal with a space in front and the other without.
func integerFromText(text string) (*big.Int, string) {
	if asWrittenIsTooLong(text) {
		return nil, integerTooLong
	}
	digits, readable := requestnumber.WholeNumber(text)
	if !readable {
		return nil, errorenvelope.IntegerExpected
	}
	length := significantDigits(digits)
	if strings.HasPrefix(digits, "-") {
		length++
	}
	if length > maxIntegerDigits {
		return nil, errorenvelope.IntegerExpected
	}
	value, _ := wholeNumber(digits)
	return value, ""
}

// asWrittenIsTooLong is the first pass's one refusal: an optional minus and
// a run of digits not starting with zero, longer together than
// maxIntegerDigits.
func asWrittenIsTooLong(written string) bool {
	start := 0
	if strings.HasPrefix(written, "-") {
		start = 1
	}
	if start >= len(written) || written[start] == '0' {
		return false
	}
	end := start
	for end < len(written) && written[end] >= '0' && written[end] <= '9' {
		end++
	}
	return end > start && end > maxIntegerDigits
}

// clock is the moment a link's expiry is measured against. The Python routes
// let is_expired read it; the routes here take it so a test can say which.
type clock func() time.Time

func (c clock) now() time.Time {
	if c == nil {
		return time.Now()
	}
	return c()
}

// writeFailure is this package's 500, and it is always the one **without**
// CORS headers.
//
// None of the three Python routes has an `except Exception`, and neither
// does anything they call. So nothing that goes wrong in them reaches
// Starlette's ExceptionMiddleware: it is answered by ServerErrorMiddleware,
// which is the outermost middleware and therefore outside CORSMiddleware
// (internal/corsmirror.Suppress; custom/docs/go-api-tier.md, divergence 5).
// That covers a failed statement, a column Python cannot use as the route
// uses it, a page the response model refuses, and a value json.dumps will
// not write, alike -- checked per route, and all three are the same.
//
// The 404s, the 410 and the 422 are not these. They are raised on purpose
// and keep their headers.
func writeFailure(ctx huma.Context, envelope errorenvelope.Writer, logger *slog.Logger,
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
	envelope.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
