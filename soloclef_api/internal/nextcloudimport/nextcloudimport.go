// Package nextcloudimport brings files a person picked in their Nextcloud
// into their documents: the preview of a pick, the import itself, and the
// refresh of one imported file.
//
//	GET  /api/v1/connectors/{connector_id}/nextcloud/browse?path=        a folder's contents (browse.go)
//	GET  /api/v1/connectors/{connector_id}/nextcloud/search?q=           files and folders by name
//	GET  /api/v1/nextcloud/refreshable-documents                         which documents can be refreshed (refreshable.go)
//	POST /api/v1/connectors/{connector_id}/nextcloud/import/preview
//	POST /api/v1/connectors/{connector_id}/nextcloud/import
//	POST /api/v1/connectors/{connector_id}/nextcloud/documents/{document_id}/refresh
//
// # Whose rows
//
// Everything here is the caller's own, and nothing else exists: the
// connection, the documents and folders an import makes, the notebook it
// links them to, the document a refresh reads again. Each statement names
// the account (queries.sql), so another account's connection, notebook or
// document is answered with the same 404 as an id that was never used.
// There is nothing further to ask about the caller.
//
// # What an import does
//
//  1. The connection is read for its owner and its app password decrypted
//     (soloclef_connectors/nextcloud, Credentials). Only the account that
//     made a connection can use it.
//  2. The pick is turned into a plan by listing Nextcloud over WebDAV
//     (plan.go, with the read-only client of soloclef_connectors/nextcloud,
//     which the chat's Nextcloud tools read with too): which files come in, which the chosen parser does not
//     read, which are too large.
//  3. One transaction creates or reuses a document per file, pending and
//     naming its parser, makes the folders, and links the notebook
//     (store.go). The rows are committed before any byte is downloaded, so
//     the library shows the files at once.
//  4. Each queued document is downloaded by a bounded pool (downloads.go):
//     the WebDAV GET is streamed straight into the file store, the
//     document_files row is written, and the index job is queued
//     (internal/indexqueue). From there the document is an upload like any
//     other: the indexing worker reads the stored original with the parser
//     on the row.
//
// # Better than the code it replaces
//
//   - The Python job downloaded each file to a temporary file, read the
//     whole of it into memory to store it, and treated a failure to keep
//     the original as something to log and carry on from. Here a download
//     is one stream into the store, counted and digested as it passes, and
//     a document is not indexed unless its original is stored.
//   - The parser was written into a metadata blob that only the Python
//     pipeline read, and could be one of two words. It is the document's
//     own `parser` column now, one of the four parsers, checked against the
//     same registry the worker routes by.
//   - A document whose job died was "in flight" for ever: picking the file
//     again linked the stuck row and did nothing. A document that has been
//     waiting for its download longer than StaleAfter is queued again
//     (store.go, "Stuck downloads").
//   - A reply to a redirect is never followed, and the app password is sent
//     only to the server the deployment names; both were true before and
//     are tests now (in soloclef_connectors/nextcloud, with the client).
//   - A failed file carries a reason written for the person looking at the
//     list; the Python reason was the exception's text.
//
// # What is deliberately not carried over
//
// The Python job compared the parsed text with every other document's and,
// on a match, deleted the new document and linked the old one. That needs
// the text, which this tier never has: parsing is the worker's. A file that
// is the same Nextcloud file (same account and path) is still recognised,
// by identity and etag, before anything is downloaded.
package nextcloudimport

import "time"

// The routes, where the web's Nextcloud picker already asks.
const (
	BrowsePath      = "/api/v1/connectors/{connector_id}/nextcloud/browse"
	SearchPath      = "/api/v1/connectors/{connector_id}/nextcloud/search"
	RefreshablePath = "/api/v1/nextcloud/refreshable-documents"
	PreviewPath     = "/api/v1/connectors/{connector_id}/nextcloud/import/preview"
	ImportPath      = "/api/v1/connectors/{connector_id}/nextcloud/import"
	RefreshPath     = "/api/v1/connectors/{connector_id}/nextcloud/documents/{document_id}/refresh"
)

// MaxImportFiles is the most files one import brings in, so one click
// cannot pull in a whole drive; the picker asks for a narrower pick.
const MaxImportFiles = 200

// maxPickedItems is the most picks one request may carry.
const maxPickedItems = 500

// maxFolderDepth is how deep a folder chain may be in an account's tree
// (the folders slice's limit). A deeper Nextcloud tree folds its extra
// levels into the deepest folder allowed.
const maxFolderDepth = 8

// StaleAfter is how long a document may wait for its download before it is
// taken to have been abandoned (the process that owed it was killed) and is
// queued again by the next import or refresh that names it. It is far
// longer than any download this tier allows to stall, so a download that is
// really running is never started twice.
const StaleAfter = 15 * time.Minute

// bodyLimit bounds a request body: 500 picks of a 4096-byte path at the
// very most, and a real one is a few hundred bytes.
const bodyLimit = 4 << 20

// What the routes say.
const (
	connectionAbsent = "Nextcloud connection not found"
	notebookAbsent   = "Notebook not found"
	importedAbsent   = "Imported Nextcloud file not found"
)

// The reasons a failed import carries, written for the person looking at
// the document list. The technical error goes to the log.
const (
	reasonAuth        = "Nextcloud refused the connection's app password. Reconnect Nextcloud and import the file again."
	reasonGone        = "The file is no longer in Nextcloud, or this account can no longer see it."
	reasonUnavailable = "Nextcloud could not be reached, or stopped sending the file. Import it again in a moment."
	reasonStorage     = "The file could not be stored right now. Import it again in a moment."
	reasonRestart     = "The server restarted before this file was downloaded. Import it again."
)
