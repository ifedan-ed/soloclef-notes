package notebooksources

import "context"

// Listing is a notebook's sources as both apps read them, for a route
// outside this package that changes the sources and answers the list: the
// research box's import. It is the list these routes answer, from the same
// two statements, so an import and an add can never disagree about what a
// source looks like. db may be a transaction.
func Listing(ctx context.Context, db DBTX, userID string, notebookID int64) (any, error) {
	return sourcesOf(ctx, db, userID, notebookID)
}
