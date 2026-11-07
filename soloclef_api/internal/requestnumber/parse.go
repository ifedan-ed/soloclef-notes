// Package requestnumber reads a request parameter as an integer the way
// FastAPI's own coercion reads it, and reports a value it cannot read as the
// Python tier's 422 field rather than in words of this tier's own.
//
// Every route handed over so far takes its workspace id in the path and its
// window in the query, and a client that sends either of those wrong must get
// the same refusal from both tiers. That is why the parsing lives here and not
// in a route: two routes parsing "the same" number two ways is two refusals.
package requestnumber

import (
	"errors"
	"regexp"
	"strconv"

	"git.danvics.com/danvics/surfsense/soloclef_api/internal/errorenvelope"
)

// ErrOutOfRange is a parameter Python parses as an int and then fails on: a
// workspace id no column can hold, which makes its query raise, or a window its
// arithmetic cannot express. Both end as a route's sanitised 500.
var ErrOutOfRange = errors.New("the request's numbers are out of range")

// wholeNumber is what Python accepts as an int literal for a parameter.
var wholeNumber = regexp.MustCompile(`^[+-]?\d+$`)

// Parse reads one parameter. location is the path pydantic reports for it, such
// as ("path", "workspace_id"): a value that is not a whole number comes back as
// the failure for that location, and the caller decides whether to report it
// with the others, as FastAPI reports every bad parameter of a request at once.
func Parse(text string, location ...string) (int64, *errorenvelope.Failure, error) {
	if !wholeNumber.MatchString(text) {
		return 0, &errorenvelope.Failure{
			Location: location,
			Message:  errorenvelope.IntegerExpected,
		}, nil
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		// Only ErrRange is reachable here; the syntax was just checked.
		return 0, nil, ErrOutOfRange
	}
	return value, nil, nil
}
