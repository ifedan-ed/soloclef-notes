package logsummary

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"

	"git.danvics.com/danvics/surfsense/soloclef_api/internal/errorenvelope"
)

// defaultWindowHours is the Python route's `hours: int = 24`.
const defaultWindowHours = 24

// ErrNumberOutOfRange is a parameter Python parses as an int and then fails on:
// a workspace id no column can hold, which makes its query raise, or a window
// its arithmetic cannot express. Both end as the route's sanitised 500.
var ErrNumberOutOfRange = errors.New("the request's numbers are out of range")

// wholeNumber is what Python accepts as an int literal for a parameter.
var wholeNumber = regexp.MustCompile(`^[+-]?\d+$`)

// parameters are the two numbers this route takes.
type parameters struct {
	WorkspaceID int64
	Hours       int64
}

// readParameters parses the path and query the way FastAPI's int coercion does,
// and otherwise reports the failures in the order FastAPI reports them: the
// path parameter first, then the query.
func readParameters(workspaceID string, query url.Values) (parameters, []errorenvelope.Failure, error) {
	var (
		parsed   parameters
		failures []errorenvelope.Failure
	)

	id, err := parseWholeNumber(workspaceID)
	switch {
	case err != nil && isOutOfRange(err):
		return parameters{}, nil, ErrNumberOutOfRange
	case err != nil:
		failures = append(failures, errorenvelope.Failure{
			Location: []string{"path", "workspace_id"},
			Message:  errorenvelope.IntegerExpected,
		})
	default:
		parsed.WorkspaceID = id
	}

	parsed.Hours = defaultWindowHours
	if query.Has("hours") {
		hours, err := parseWholeNumber(query.Get("hours"))
		switch {
		case err != nil && isOutOfRange(err):
			return parameters{}, nil, ErrNumberOutOfRange
		case err != nil:
			failures = append(failures, errorenvelope.Failure{
				Location: []string{"query", "hours"},
				Message:  errorenvelope.IntegerExpected,
			})
		default:
			parsed.Hours = hours
		}
	}

	return parsed, failures, nil
}

func parseWholeNumber(text string) (int64, error) {
	if !wholeNumber.MatchString(text) {
		return 0, strconv.ErrSyntax
	}
	return strconv.ParseInt(text, 10, 64)
}

func isOutOfRange(err error) bool {
	var numberError *strconv.NumError
	return errors.As(err, &numberError) && errors.Is(numberError.Err, strconv.ErrRange)
}
