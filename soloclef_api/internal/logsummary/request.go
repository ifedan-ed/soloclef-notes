package logsummary

import (
	"net/url"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// defaultWindowHours is the Python route's `hours: int = 24`.
const defaultWindowHours = 24

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

	id, failure, err := requestnumber.Parse(workspaceID, "path", "workspace_id")
	if err != nil {
		return parameters{}, nil, err
	}
	if failure != nil {
		failures = append(failures, *failure)
	}
	parsed.WorkspaceID = id

	parsed.Hours = defaultWindowHours
	if query.Has("hours") {
		hours, failure, err := requestnumber.Parse(query.Get("hours"), "query", "hours")
		if err != nil {
			return parameters{}, nil, err
		}
		if failure != nil {
			failures = append(failures, *failure)
		} else {
			parsed.Hours = hours
		}
	}

	return parsed, failures, nil
}
