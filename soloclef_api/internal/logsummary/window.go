// Package logsummary serves GET /api/v1/logs/workspaces/{workspace_id}/summary,
// the counts the dashboard polls while a workspace is indexing.
package logsummary

import (
	"errors"
	"time"
)

// ErrWindowOutOfRange is what the Python route's arithmetic raises for an
// absurd number of hours — timedelta's own range, or a date before year 1 or
// after year 9999 — which its handler turns into a 500.
var ErrWindowOutOfRange = errors.New("the time window is out of range")

// Since is the cutoff the Python route computes:
// datetime.utcnow().replace(microsecond=0) - timedelta(hours=hours).
//
// The seconds are truncated there, so they are truncated here, or the same
// request a second apart could include a different row. The naive UTC value it
// binds is compared against a timestamptz column with the server's timezone set
// to UTC, which is what a UTC instant means here.
//
// The hours are split into days and a remainder because a Go Duration only
// reaches about 292 years, while Python's datetime reaches year 1 — a window of
// a thousand years is nonsense, but it must be the same nonsense on both tiers.
func Since(now time.Time, hours int64) (time.Time, error) {
	days, rest := hours/24, hours%24
	if days > maxDays || days < -maxDays {
		return time.Time{}, ErrWindowOutOfRange
	}

	since := now.UTC().Truncate(time.Second).
		AddDate(0, 0, int(-days)).
		Add(-time.Duration(rest) * time.Hour)
	if year := since.Year(); year < 1 || year > 9999 {
		return time.Time{}, ErrWindowOutOfRange
	}
	return since, nil
}

// maxDays is timedelta.max.days, the point at which Python itself raises.
const maxDays = 999999999
