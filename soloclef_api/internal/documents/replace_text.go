package documents

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// ReplaceText keeps a document's current text as a version and writes a new
// text and title over it, inside the caller's transaction.
//
// It is the restore's own steps (applyRestore) given a text that did not
// come from the history: the snapshot, the two clean-ups, and the write that
// marks the document for re-indexing. A save through the API and a restore
// from the history therefore keep versions by one rule, and cannot drift
// into two.
//
// The caller has read `document` under a lock in the same transaction, and
// owes the document whatever a save owes beyond this: its hash, its time
// and its status.
func ReplaceText(ctx context.Context, tx pgx.Tx, work RestoreWork) error {
	return applyRestore(ctx, transactionRows{queries: New(tx)}, work)
}
