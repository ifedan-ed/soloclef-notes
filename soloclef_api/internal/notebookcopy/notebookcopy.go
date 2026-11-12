// Package notebookcopy duplicates one of the caller's notebooks:
//
//	POST /api/v1/notebooks/{notebook_id}/copy   {"title": "..."}  -> 201, the new notebook
//
// # What is copied
//
// The notebook's row -- its icon, chat goal, chat instructions and response
// length, under the title asked for (or the original's with " (copy)") --
// and its source links, each with its tick and the moment it was added, so
// the copy lists them in the same order. Documents and folders are linked,
// never duplicated: the copy reads the same sources.
//
// # What is not
//
// Chats, studio outputs, the public link and the pin stay with the
// original: a copy starts a fresh conversation over the same sources and is
// shared with nobody. Notes filed in the original stay filed there. A note
// is one document filed in one notebook, so "copying" one would make a
// second document to index and two texts to drift apart; a note switched on
// as a source is a source link like any other and does come across.
//
// # What it does better than the Python
//
//   - **One statement** makes the notebook, links its sources and answers
//     the new row with its counts. The Python flushed the row, read the
//     sources, added them one by one, read the notes, committed and read
//     the notebook back; a failure partway rolled back, but the answer cost
//     seven round trips.
//   - **The statement names the owner**, so another account's notebook is
//     the same 404 as one never made, with nothing written.
//   - **A title is cut by characters**, by the database that enforces the
//     limit, so a long title in any script fits instead of failing.
package notebookcopy

import "net/http"

// Path is the route's path.
const Path = "/api/v1/notebooks/{notebook_id}/copy"

// Served is the route as a "METHOD /path" pair.
func Served() []string { return []string{http.MethodPost + " " + Path} }

const (
	notebookAbsent = "Notebook not found"
	// maxBodyBytes is far more than a 200-character title in any script
	// needs; a larger body is not a title.
	maxBodyBytes = 16 << 10
)
