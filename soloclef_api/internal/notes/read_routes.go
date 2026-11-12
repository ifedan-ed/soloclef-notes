package notes

import "net/http"

// list answers a page of the caller's notes (filter.go says what can be
// asked for).
func (r Routes) list(x *exchange) {
	asked := x.ctx.URL()
	filter, failures := ReadFilter(asked.Query())
	if len(failures) > 0 {
		x.invalid(failures)
		return
	}
	page, err := readPage(x.ctx.Context(), r.Pool, x.caller.UserID, filter)
	if err != nil {
		x.fail("could not read the caller's notes", err)
		return
	}
	x.send(http.StatusOK, renderPage(page))
}

// read answers one note with its whole text.
func (r Routes) read(x *exchange) {
	ids, ok := x.ids("note_id")
	if !ok {
		return
	}
	note, err := loadNote(x.ctx.Context(), r.Pool, x.caller.UserID, ids[0])
	if err != nil {
		x.fail("could not read the note", err)
		return
	}
	if note == nil {
		x.notFound(noteAbsent)
		return
	}
	x.send(http.StatusOK, renderNote(*note, true))
}
