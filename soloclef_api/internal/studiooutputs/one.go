package studiooutputs

import "context"

// One is a single output as the list answers it, for the route that starts
// one (internal/studiogenerate): what it answers must be the very shape the
// panel then polls, so it is rendered here and not written a second time.
// found is false when the notebook has no such output of the caller's.
func One(ctx context.Context, db DBTX, userID string, notebookID, outputID int64) (value any, found bool, err error) {
	rows, err := New(db).Outputs(ctx, OutputsParams{NotebookID: notebookID, UserID: userID, OnlyID: outputID})
	if err != nil || len(rows) == 0 {
		return nil, false, err
	}
	return outputOf(rows[0]), true, nil
}
