package documents

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// errTooWide is an integer Python holds and this tier cannot: one past int64.
// It is never a refusal by itself -- what it means depends on what the route
// does with the number -- so it is only ever the cause logged beside a 500.
var errTooWide = errors.New("documents: an integer is wider than int64")

// number is one integer parameter as the Python route holds it.
//
// Python's integers have no width, so a parameter of twenty digits is read
// without complaint and three different things can then happen to it, all of
// which these routes do:
//
//   - it is compared with a bound, and the comparison is simply true or false
//     (`ge=0`, `le=100`) -- so its sign has to survive;
//   - it is used in arithmetic that never reaches a statement
//     (`chunk_window`), and the request succeeds;
//   - it reaches a statement, the driver cannot encode it, and the 500 lands
//     wherever that statement is in the route's order.
//
// internal/requestnumber.Parse reports only "out of range", which is enough
// for a route that does the third thing alone. These need the sign as well.
type number struct {
	// value is the number when it fits an int64.
	value int64
	// beyond is zero when it does, and the sign of the number when it does
	// not: +1 for one past the largest int64, -1 for one below the smallest.
	beyond int
}

// tooWide reports a number no statement of this tier can carry.
func (n number) tooWide() bool { return n.beyond != 0 }

// below is Python's `n < bound` for a bound that fits an int64.
func (n number) below(bound int64) bool {
	switch {
	case n.beyond < 0:
		return true
	case n.beyond > 0:
		return false
	}
	return n.value < bound
}

// above is Python's `n > bound`.
func (n number) above(bound int64) bool {
	switch {
	case n.beyond > 0:
		return true
	case n.beyond < 0:
		return false
	}
	return n.value > bound
}

// readNumber reads one parameter as FastAPI coerces it
// (internal/requestnumber.WholeNumber is pydantic's own reader). A value that
// is not a whole number is appended to failures and false is returned, so the
// caller does not go on to check a bound on a value that was never read --
// pydantic reports one failure per field, and the type's comes first.
func readNumber(text string, failures *[]errorenvelope.Failure, location ...string) (number, bool) {
	digits, readable := requestnumber.WholeNumber(text)
	if !readable {
		*failures = append(*failures, errorenvelope.Failure{
			Location: location,
			Message:  errorenvelope.IntegerExpected,
		})
		return number{}, false
	}
	value, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		// Only a range error is reachable: the syntax was just checked.
		if strings.HasPrefix(digits, "-") {
			return number{beyond: -1}, true
		}
		return number{beyond: 1}, true
	}
	return number{value: value}, true
}

// readBounded reads one query parameter declared `Query(default, ge=…)` or
// `Query(default, ge=…, le=…)`.
//
// named is false when the request did not mention it, which is the default
// and never a failure. A parameter mentioned with an empty value is
// mentioned: `?page=` is a 422. One mentioned twice is its last value
// (internal/requestnumber.QueryValue).
//
// atMost is nil for a parameter with no upper bound.
func readBounded(query url.Values, name string, atLeast int64, atMost *int64,
	failures *[]errorenvelope.Failure) (value number, named bool) {
	text, named := requestnumber.QueryValue(query, name)
	if !named {
		return number{}, false
	}
	read, readable := readNumber(text, failures, "query", name)
	if !readable {
		return number{}, true
	}
	switch {
	case read.below(atLeast):
		*failures = append(*failures, errorenvelope.Failure{
			Location: []string{"query", name},
			Message:  requestbody.AtLeast(atLeast),
		})
	case atMost != nil && read.above(*atMost):
		*failures = append(*failures, errorenvelope.Failure{
			Location: []string{"query", name},
			// The only upper bound in this slice is page_size's.
			Message: atMostOneHundred,
		})
	}
	return read, true
}
