// Package citedsources serves the names of the sources an answer cites:
//
//	GET /api/v1/chunks/sources?ids=812,90,5
//
// An answer's text cites passages by id ("[citation:812]"). When it is
// copied or downloaded, the web app ends it with a short "Sources" list
// naming each cited document, and this route is how it learns the names:
// for each of the caller's passages, its document's id and title and the
// page the passage starts on.
//
// It answers {"sources": [{"chunk_id", "document_id", "title", "page"}]},
// ordered by chunk id. A passage the caller cannot see, or whose document
// is being deleted, is absent rather than refused: an answer can cite a
// document deleted since, and the list then says so for that one source.
//
// # Why a route of its own
//
// The by-chunk route (internal/documents) serves a whole document with a
// window of its chunks, text included, for the citation panel. Naming ten
// sources through it was ten requests, each carrying a document's full
// text. Here it is one request and one statement, reading two short
// columns per passage.
package citedsources

import "net/http"

// Path is the route.
const Path = "/api/v1/chunks/sources"

// Served is every route as a "METHOD /path" pair.
func Served() []string { return []string{http.MethodGet + " " + Path} }

// maxIDs bounds one request. An answer cites a handful of passages; a list
// far past that is not a copy, and every id is a parameter of the statement.
// The web app asks in batches of this size (features/markdown-copy).
const maxIDs = 100
