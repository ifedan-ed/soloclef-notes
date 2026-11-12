package activitysummary

import (
	"net/url"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// defaultWindowHours is the window when the request names none.
const defaultWindowHours = 24

// readHours parses the one parameter the route takes, the window in hours.
// An absent parameter is the default; an empty one is a value that cannot be
// read and is refused, so the two are not confused.
func readHours(query url.Values) (int64, []errorenvelope.Failure, error) {
	// The last value a query string names, for a parameter named twice
	// (internal/requestnumber.QueryValue).
	text, named := requestnumber.QueryValue(query, "hours")
	if !named {
		return defaultWindowHours, nil, nil
	}
	hours, failure, err := requestnumber.Parse(text, "query", "hours")
	if err != nil {
		return 0, nil, err
	}
	if failure != nil {
		return 0, []errorenvelope.Failure{*failure}, nil
	}
	return hours, nil, nil
}
