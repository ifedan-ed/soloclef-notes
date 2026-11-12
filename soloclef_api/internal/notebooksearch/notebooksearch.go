// Package notebooksearch serves the search box on the notebooks home page:
//
//	GET /api/v1/notebooks/search?q=&limit=
//
// It answers the passages that match q inside the caller's notebooks,
// grouped by notebook: each notebook's card, and under it up to three
// passages from its ticked sources and its notes, each with a short snippet
// and the ranges of the snippet to highlight. Studio outputs and chats are
// never searched.
//
// The search itself is the chat's (soloclef_assistant/documents): the same
// embedding, the same vector store, the same keyword leg and the same
// fusion, confined to the documents the caller's notebooks read. This
// package decides what is in that scope, which passages are worth showing
// in a search box, and where each one opens.
//
// The path is a literal, which ServeMux prefers to
// `/notebooks/{notebook_id}`, so "search" is never read as a notebook's id.
//
// # What it does better than the Python
//
//   - **Notes are searched as documents are.** A note is a document now, so
//     it is found by meaning as well as by its words; the Python matched
//     notes by keyword only because they had no vectors.
//   - **One search, not a second implementation.** The Python wrote its own
//     hybrid query for this box, with its own arm sizes and fusion; a change
//     to how the chat searches did not reach it.
//   - **One statement reads the scope** (every notebook's documents, how
//     each is reached, and the notebook's card), where the Python ran two
//     for the links, one for notes and one more for the notebooks.
//   - **Highlights are positions the clients can use.** Both apps slice the
//     snippet as a JavaScript string, which counts UTF-16 units; the Python
//     answered code-point offsets, so every highlight after an emoji or a
//     rare CJK character was drawn one place early.
//   - **A search that cannot run says so** with a 503. The Python answered
//     keyword results alone when the embedding failed, which looked like
//     "nothing else matches".
//   - **A passage found by meaning alone is shown only when it is close**
//     (passages.go): the Python's line, 0.55 cosine similarity, and besides
//     it only from the search's three best documents.
package notebooksearch

import (
	"net/http"
	"time"
)

// Path is the route.
const Path = "/api/v1/notebooks/search"

// Served is every route as a "METHOD /path" pair.
func Served() []string { return []string{http.MethodGet + " " + Path} }

const (
	// A query shorter than two characters matches too much to mean
	// anything and is answered with no results; one longer than two hundred
	// is not something typed into a search box.
	minQueryChars = 2
	maxQueryChars = 200

	// The sizes of an answer: how many notebooks, how many passages under
	// each, how many in all. They are what the page has room for.
	maxNotebooks   = 8
	maxPerNotebook = 3
	maxPassages    = 20

	// How many documents the search is asked for. It is the most the chat's
	// search returns at all, and it has to be well over maxNotebooks
	// because one notebook's documents can fill the top of the ranking.
	searchDocuments = 20

	// A search is an embedding, a vector query and a keyword query, each
	// well under a second; ten is a store that is not answering.
	searchTimeout = 10 * time.Second

	searchUnavailable = "Search is not available right now. Please try again."
)
