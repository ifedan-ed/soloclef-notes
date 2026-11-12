// Package notes serves the notes: what a person writes in the app, and the
// answers they keep from a chat.
//
// There is one kind of note. Every note is a `documents` row of type NOTE,
// holding its title and its Markdown, and a `notes` row beside it holding
// what a document does not: the notebook it is filed in (or none), where it
// came from (written by hand, kept from a chat answer, written by an
// automation), the thread and message it was kept from, and when it was
// pinned. Because a note is a document, the indexer chunks and embeds it
// like any other, so the assistant can find and cite what a person wrote.
//
//	GET    /api/v1/notes                                list, search, filter
//	POST   /api/v1/notes                                write one, or keep a chat answer
//	GET    /api/v1/notes/{note_id}                      read one, with its text
//	PATCH  /api/v1/notes/{note_id}                      save its title, its text, or both
//	DELETE /api/v1/notes/{note_id}                      remove it, vectors and all
//	PUT    /api/v1/notes/{note_id}/pin                  keep it on top
//	DELETE /api/v1/notes/{note_id}/pin                  stop keeping it on top
//	PUT    /api/v1/notes/{note_id}/notebook             file it in a notebook, or in none
//	PUT    /api/v1/notes/{note_id}/sources/{notebook_id}  switch it on as the notebook's source
//	DELETE /api/v1/notes/{note_id}/sources/{notebook_id}  switch it off
//
// # A source switch, not a copy
//
// A notebook's chat reads its sources. A note is made readable to a notebook
// by a `notebook_sources` row naming the note's own document, and nothing
// else: there is no copy, so what the chat cites is what the note says now.
// Filing a note in a notebook and switching it on as that notebook's source
// are separate, because a note can be worth keeping beside a notebook
// without the chat treating it as evidence.
//
// # Saving and indexing
//
// The text is Markdown, the format the web editor reads and writes and the
// indexer reads. A save writes the text at once and, when the text changed,
// marks the note pending and queues ONE indexing job, delayed by IndexDelay.
// While that job waits the note stays pending, and the saves made meanwhile
// queue nothing: the job reads the row when it runs, so it indexes the
// newest text. An autosave every second of typing is therefore at most one
// indexing run per note per IndexDelay, and the indexer keeps every chunk
// whose text did not change, so a run after a small edit embeds only the
// passages that moved.
//
// # Whose note
//
// Every statement names the caller as the owner, so another account's note,
// a document that is not a note, and an id never used are all one answer:
// 404 Note not found. The same holds for the notebook, thread and message a
// request names.
package notes

import (
	"net/http"
	"time"
)

// The paths.
const (
	NotesPath    = "/api/v1/notes"
	NotePath     = "/api/v1/notes/{note_id}"
	PinPath      = "/api/v1/notes/{note_id}/pin"
	NotebookPath = "/api/v1/notes/{note_id}/notebook"
	SourcePath   = "/api/v1/notes/{note_id}/sources/{notebook_id}"
)

// Served is every route of this package as a "METHOD /path" pair, in the
// order of the package comment: what cmd/api's servedRoutes() lists.
func Served() []string {
	return []string{
		http.MethodGet + " " + NotesPath,
		http.MethodPost + " " + NotesPath,
		http.MethodGet + " " + NotePath,
		http.MethodPatch + " " + NotePath,
		http.MethodDelete + " " + NotePath,
		http.MethodPut + " " + PinPath,
		http.MethodDelete + " " + PinPath,
		http.MethodPut + " " + NotebookPath,
		http.MethodPut + " " + SourcePath,
		http.MethodDelete + " " + SourcePath,
	}
}

// WithBodies is the routes that read a body, which cmd/api's body reader
// must cover (internal/requestbody.Middleware).
func WithBodies() []string {
	return []string{
		http.MethodPost + " " + NotesPath,
		http.MethodPatch + " " + NotePath,
		http.MethodPut + " " + NotebookPath,
	}
}

// DefaultIndexDelay is how long a note's indexing job waits before it runs
// when the routes are built with no delay of their own. The running API is
// always given the deployment's (SOLOCLEF_API_NOTE_INDEX_DELAY_SECONDS).
//
// Long enough that a burst of typing is one run rather than one per
// autosave, short enough that a note just written is in the chat's search
// within the minute. A waiting job costs the indexer nothing: the worker
// parks a message taken before its time in a scheduled set and frees the
// slot (soloclef_worker/internal/broker/scheduled.go).
const DefaultIndexDelay = 15 * time.Second

// The origins a note can have, as the `notes.origin` CHECK spells them.
const (
	OriginHand       = "hand"
	OriginChat       = "chat"
	OriginAutomation = "automation"
)

// The ways a list can be ordered after its pinned notes.
const (
	SortEdited  = "edited"
	SortCreated = "created"
	SortTitle   = "title"
)

// Paging limits for the list.
const (
	DefaultPageSize int64 = 50
	MaxPageSize     int64 = 200
)

// Limits on what a request may write.
const (
	// MaxTitleLength is in characters. The column has no limit; a title
	// longer than a line of text is a paragraph in the wrong field.
	MaxTitleLength = 300
	// MaxTextBytes bounds one note's Markdown. A note is something a person
	// writes or keeps; a book belongs in an upload.
	MaxTextBytes = 2 << 20
	// MaxQueryLength bounds the search text.
	MaxQueryLength = 200
	// previewLength is how much of a note's text the list sends for its
	// two-line preview. The apps reduce it to plain words, which takes some
	// of it, so it is more than two lines' worth.
	previewLength = 400
)

// The refusals the routes raise themselves.
const (
	noteAbsent     = "Note not found"
	notebookAbsent = "Notebook not found"
	threadAbsent   = "Chat not found"
	messageAbsent  = "Message not found"
)

// untitled is the title of a note given none and having no text to take one
// from.
const untitled = "Untitled note"
