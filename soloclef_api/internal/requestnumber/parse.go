// Package requestnumber reads a request parameter as an integer the way
// FastAPI's own coercion reads it, and reports a value it cannot read as the
// Python tier's 422 field rather than in words of this tier's own.
//
// Every route handed over so far takes at least one id in the path, and several
// take a number in the query, and a client that sends either of those wrong
// must get the same refusal from both tiers. That is why the parsing lives here
// and not in a route: two routes parsing "the same" number two ways is two
// refusals.
package requestnumber

import (
	"errors"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// ErrOutOfRange is a parameter Python parses as an int and then fails on: an
// id no column can hold, which makes its query raise, or a window its
// arithmetic cannot express. Both end as a route's sanitised 500.
var ErrOutOfRange = errors.New("the request's numbers are out of range")

// Parse reads one parameter. location is the path pydantic reports for it, such
// as ("path", "folder_id"): a value that is not a whole number comes back as
// the failure for that location, and the caller decides whether to report it
// with the others, as FastAPI reports every bad parameter of a request at once.
func Parse(text string, location ...string) (int64, *errorenvelope.Failure, error) {
	digits, readable := WholeNumber(text)
	if !readable {
		return 0, &errorenvelope.Failure{
			Location: location,
			Message:  errorenvelope.IntegerExpected,
		}, nil
	}
	value, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		// Only ErrRange is reachable here; the syntax was just checked. Python
		// holds the integer and fails later, in the query or the arithmetic,
		// which is the same sanitised 500.
		return 0, nil, ErrOutOfRange
	}
	return value, nil, nil
}
