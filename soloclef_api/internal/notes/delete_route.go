package notes

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentdeletion"
)

// remove deletes a note as a document is deleted, by
// documentdeletion.Removal: it is marked "deleting" (out of every list and
// search at once), its points leave the vector store, then its passages,
// stored files and row. A removal that stops partway answers 503 with the
// note left marked, and the same request finishes it.
//
// A note waiting to be indexed, or being indexed, is removed all the same.
// The indexing worker reads the marker before it writes and stops, and a
// note with its delayed job still waiting would otherwise be undeletable
// for the length of the delay after every keystroke.
func (r Routes) remove(x *exchange) {
	ids, ok := x.ids("note_id")
	if !ok {
		return
	}
	noteID, err := New(r.Pool).NoteToRemove(x.ctx.Context(), NoteToRemoveParams{ID: ids[0], UserID: x.caller.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		x.notFound(noteAbsent)
		return
	}
	if err != nil {
		x.fail("could not read the note", err)
		return
	}

	// Not the request's context: a delete that has begun should finish even
	// if the person closes the tab, because nobody repeats a request they
	// walked away from.
	removing, done := context.WithTimeout(context.WithoutCancel(x.ctx.Context()), removalTimeout)
	defer done()
	started := time.Now()
	if err := r.Removal.Remove(removing, noteID); err != nil {
		r.Logger.LogAttrs(x.ctx.Context(), slog.LevelError,
			"a note was not fully deleted; it is left marked and the delete can be repeated",
			slog.String("request_id", x.requestID), slog.Int64("document_id", noteID),
			slog.Duration("after", time.Since(started)), slog.String("error", err.Error()))
		r.Errors.Write(x.response, http.StatusServiceUnavailable, documentdeletion.Unfinished, x.requestID)
		return
	}
	r.Logger.LogAttrs(x.ctx.Context(), slog.LevelInfo, "note deleted",
		slog.String("request_id", x.requestID), slog.Int64("document_id", noteID),
		slog.Duration("took", time.Since(started)))
	x.response.WriteHeader(http.StatusNoContent)
}
