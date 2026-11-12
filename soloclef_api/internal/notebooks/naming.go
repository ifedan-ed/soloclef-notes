package notebooks

import (
	"context"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// `title_pending` tells the apps "this notebook's automatic name is seconds
// away, look again soon". A notebook still named automatically is pending
// while one of its sources is being indexed, or became ready since the
// notebook was last named. The naming itself is a background job, which
// writes `notebooks.auto_title`.

const (
	// maxRuns: a notebook is never named automatically more than twice.
	maxRuns = 2
	// refineWindow: a notebook named once is renamed only this soon after.
	refineWindow = 30 * time.Minute
	// recentlyReady: a source that became ready this recently is about to
	// be used for a name.
	recentlyReady = 2 * time.Minute
	// indexingWindow: a source stuck indexing longer than this no longer
	// keeps anyone polling.
	indexingWindow = 30 * time.Minute
)

// candidate is a notebook that may still name itself, and the instant since
// which a source becoming ready would count.
type candidate struct {
	notebookID int64
	readySince time.Time
}

// stillNaming decides whether a notebook may still be named automatically.
//
// `auto_title` is jsonb the naming task writes as
// `{"runs": n, "at": "<RFC 3339 instant>", …}`. It is read defensively,
// because it is one notebook's state and must never cost anyone the rest of
// their list: anything that is not that shape is read as the safe answer.
// A missing or unreadable `runs` is "never named"; a notebook named once
// whose `at` cannot be read is "not pending", since the window it would have
// to be inside cannot be known.
func stillNaming(notebook Notebook, now time.Time) (candidate, bool) {
	since := now.Add(-recentlyReady)
	if notebook.TitleSource != "auto" {
		return candidate{}, false
	}
	var state *pythonjson.Object
	if notebook.AutoTitle != nil {
		if decoded, err := pythonjson.Decode(notebook.AutoTitle); err == nil {
			state, _ = decoded.(*pythonjson.Object)
		}
	}
	if state == nil {
		return candidate{notebook.ID, since}, true
	}
	var namedAt *time.Time
	if text, isText := state.GetOrNil("at").(string); isText {
		if at, err := time.Parse(time.RFC3339Nano, text); err == nil {
			namedAt = &at
		}
	}
	switch runs := runsOf(state.GetOrNil("runs")); {
	case runs >= maxRuns:
		return candidate{}, false
	case runs > 0 && (namedAt == nil || now.Sub(*namedAt) > refineWindow):
		return candidate{}, false
	}
	// A source ready before the last automatic name was already used by it.
	if namedAt != nil && namedAt.After(since) {
		since = *namedAt
	}
	return candidate{notebook.ID, since}, true
}

// runsOf is how many automatic names a notebook has had: a whole,
// non-negative JSON number, and zero for anything else.
func runsOf(value any) int64 {
	raw, isWhole := value.(pythonjson.RawInt)
	if !isWhole {
		return 0
	}
	var runs int64
	for _, digit := range string(raw) {
		if digit < '0' || digit > '9' {
			return 0 // negative
		}
		if runs = runs*10 + int64(digit-'0'); runs > maxRuns {
			return maxRuns
		}
	}
	return runs
}

// namingPending is which of the account's notebooks named is about to name
// itself. One statement, and none when no notebook is a candidate.
func namingPending(ctx context.Context, db DBTX, userID string, notebooks []Notebook, now time.Time) (map[int64]bool, error) {
	pending := map[int64]bool{}
	var ids []int64
	var since []time.Time
	for _, notebook := range notebooks {
		if found, may := stillNaming(notebook, now); may {
			ids = append(ids, found.notebookID)
			since = append(since, found.readySince)
		}
	}
	if len(ids) == 0 {
		return pending, nil
	}
	rows, err := New(db).PendingAmong(ctx, PendingAmongParams{
		Candidates:    ids,
		UserID:        userID,
		ReadySince:    since,
		IndexingSince: now.Add(-indexingWindow),
	})
	if err != nil {
		return nil, err
	}
	for _, id := range rows {
		pending[id] = true
	}
	return pending, nil
}
