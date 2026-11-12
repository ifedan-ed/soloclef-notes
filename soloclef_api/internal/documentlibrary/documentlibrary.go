// Package documentlibrary serves an account's documents as a library: the
// list, a title search over it, one document, adding a note, and replacing a
// document's text.
//
//	GET  /api/v1/documents                       a page of the caller's documents
//	GET  /api/v1/documents/search                the same, for titles containing a term
//	GET  /api/v1/documents/{document_id}         one document, as JSON or as an OKF concept
//	POST /api/v1/documents                       add a note
//	POST /api/v1/documents/{document_id}/save    replace a document's text
//
// The MCP server's knowledge-base tools are the main callers; the web app
// reads the list and one document through the same routes.
//
// # What it does better than the Python
//
//   - **One statement lists a page and counts it.** The Python ran a count
//     and then the page, each joined through workspaces and memberships, and
//     then loaded each row's creator. Here the page and its total come from
//     one statement that names the owner.
//   - **Adding a note takes a note.** The Python route accepted only the
//     browser extension's envelope, so the MCP tool invented a browsing
//     session, a visit time and a made-up address to wrap a title and a text
//     in. The body is now the title and the text; the note is a note (a
//     NOTE document and its `notes` row, written by the notes package's own
//     statements), it is answered with its id, and it can be edited in the
//     app like any other.
//   - **A save keeps what it replaces.** The Python save overwrote the text
//     and kept a copy only when the git store was on. Here every save that
//     changes something first keeps the current text as a version, by the
//     same steps a restore uses, so a replace through the API can be undone
//     from the document's history.
//   - **A save that changes nothing does nothing**: no version, no write, no
//     indexing job.
//   - **A save cannot write over a document being deleted**, and reads and
//     writes its row under one lock, so two saves cannot interleave.
//   - **The title is optional in a save.** The MCP tool read the document
//     first only to send its title back; leaving it out now keeps it.
//   - **A search term is a term**: `%` and `_` in it match themselves.
//   - **A page size is bounded** (1 to 200). `page_size=-1` used to return
//     every document an account has in one response.
//   - **A document type that does not exist is a 422**, not an empty page.
//   - **No stored path, hash of another account or owner id is answered**;
//     a document being deleted is not listed.
package documentlibrary

import "net/http"

// The paths.
const (
	ListPath   = "/api/v1/documents"
	SearchPath = "/api/v1/documents/search"
	OnePath    = "/api/v1/documents/{document_id}"
	SavePath   = "/api/v1/documents/{document_id}/save"
)

// Served is every route as a "METHOD /path" pair.
func Served() []string {
	return []string{
		http.MethodGet + " " + ListPath,
		http.MethodGet + " " + SearchPath,
		http.MethodGet + " " + OnePath,
		http.MethodPost + " " + ListPath,
		http.MethodPost + " " + SavePath,
	}
}

// What a caller is told.
const (
	folderAbsent     = "Folder not found"
	artifactReadOnly = "Artifact documents are read-only; revise them through the artifact tools."
	savedMessage     = "Document saved and will be reindexed in the background"
	unchangedMessage = "Document already has this text; nothing was changed"
)

// The sizes. A page is bounded because one response should not be an
// account's whole library; a text is bounded at the size the notes take, so
// a note added here is one the editor can open.
const (
	defaultPageSize = 50
	maxPageSize     = 200
	previewChars    = 300
	maxTitleChars   = 500
	maxTextBytes    = 2_000_000
	maxURLChars     = 2000
	// maxBodyBytes is the text, JSON-escaped at worst twice its size, and
	// the rest of the body.
	maxBodyBytes = 2*maxTextBytes + 64<<10
)

// documentTypes is the `documenttype` enum: what `document_types` may name.
var documentTypes = map[string]bool{
	"EXTENSION": true, "CRAWLED_URL": true, "FILE": true, "YOUTUBE_VIDEO": true,
	"GOOGLE_DRIVE_FILE": true, "NOTE": true, "ARTIFACT": true, "LOCAL_FOLDER_FILE": true,
}

func absent(id string) string { return "Document with id " + id + " not found" }
