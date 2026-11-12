package notes

import (
	"net/http"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// pin and unpin keep a note on top of its lists, or stop.
func (r Routes) pin(x *exchange)   { r.setPinned(x, true) }
func (r Routes) unpin(x *exchange) { r.setPinned(x, false) }

func (r Routes) setPinned(x *exchange, pinned bool) {
	ids, ok := x.ids("note_id")
	if !ok {
		return
	}
	changed, err := New(r.Pool).SetNotePinned(x.ctx.Context(),
		SetNotePinnedParams{Pinned: pinned, ID: ids[0], UserID: x.caller.UserID})
	if err != nil {
		x.fail("could not pin the note", err)
		return
	}
	if changed == 0 {
		x.notFound(noteAbsent)
		return
	}
	x.sendNote(ids[0], http.StatusOK)
}

// file files a note in one of the caller's notebooks, or takes it out of
// every notebook with `{"notebook_id": null}`. Filing is where the note is
// kept, not what the notebook's chat reads: the source switch is that.
func (r Routes) file(x *exchange) {
	ids, ok := x.ids("note_id")
	if !ok {
		return
	}
	filing, failures, err := ReadFiling(x.ctx.Header("Content-Type"), requestbody.Buffered(x.ctx.Context()))
	if err != nil {
		x.fail("a body reached the route that the body reader should have turned away", err)
		return
	}
	if len(failures) > 0 {
		x.invalid(failures)
		return
	}
	if filing.OutOfRange {
		x.notFound(notebookAbsent)
		return
	}
	ctx := x.ctx.Context()
	if filing.NotebookID != nil {
		owned, err := hasNotebook(ctx, r.Pool, x.caller.UserID, *filing.NotebookID)
		if err != nil {
			x.fail("could not look for the notebook", err)
			return
		}
		if !owned {
			x.notFound(notebookAbsent)
			return
		}
	}
	// The notebook could be deleted between the check and the write; its
	// composite foreign key then fails the write, which is a 500 for a race
	// a person cannot make on purpose. Not worth a transaction to close.
	changed, err := New(r.Pool).FileNote(ctx,
		FileNoteParams{NotebookID: filing.NotebookID, ID: ids[0], UserID: x.caller.UserID})
	if err != nil {
		x.fail("could not file the note", err)
		return
	}
	if changed == 0 {
		x.notFound(noteAbsent)
		return
	}
	x.sendNote(ids[0], http.StatusOK)
}

// sourceOn and sourceOff are the source switch: whether a notebook's chat
// reads the note. The note's own document is the source, so the chat cites
// the note as it is now, edits included.
func (r Routes) sourceOn(x *exchange)  { r.switchSource(x, true) }
func (r Routes) sourceOff(x *exchange) { r.switchSource(x, false) }

func (r Routes) switchSource(x *exchange, on bool) {
	ids, ok := x.ids("note_id", "notebook_id")
	if !ok {
		return
	}
	ctx, userID := x.ctx.Context(), x.caller.UserID
	noteID, notebookID := ids[0], ids[1]
	note, err := loadNote(ctx, r.Pool, userID, noteID)
	if err != nil {
		x.fail("could not read the note", err)
		return
	}
	if note == nil {
		x.notFound(noteAbsent)
		return
	}
	owned, err := hasNotebook(ctx, r.Pool, userID, notebookID)
	if err != nil {
		x.fail("could not look for the notebook", err)
		return
	}
	if !owned {
		x.notFound(notebookAbsent)
		return
	}
	queries := New(r.Pool)
	if on {
		err = queries.SwitchSourceOn(ctx, SwitchSourceOnParams{NotebookID: notebookID, UserID: userID, DocumentID: &noteID})
	} else {
		err = queries.SwitchSourceOff(ctx, SwitchSourceOffParams{NotebookID: notebookID, UserID: userID, DocumentID: &noteID})
	}
	if err != nil {
		x.fail("could not switch the note as a source", err)
		return
	}
	x.sendNote(noteID, http.StatusOK)
}

// removalTimeout bounds one removal, which runs outside the request's
// context. It is the document delete's figure: a note is a document, and
// its removal is the same work.
const removalTimeout = 2 * time.Minute
