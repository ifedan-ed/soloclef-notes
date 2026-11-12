// Package drivesync asks for an account's Google Drive files to be
// brought in: the import of files picked in the Google Picker, and "sync
// now" on the connection.
//
//	POST /api/v1/connectors/{connector_id}/google-drive/import     import the picked files
//	POST /api/v1/search-source-connectors/{connector_id}/index     bring everything imported up to date now
//
// Neither route talks to Google. The files are fetched by the worker's job
// (soloclef_worker/internal/drivesync), which holds the store's writer and
// renews the grant; these routes write the documents a person should see
// at once and publish that job (soloclef_connectors/drivesyncjob).
//
// # Whose rows
//
// The connection, the documents an import makes and the notebook it links
// them to are the caller's own. Every statement names the account beside
// the row's id (queries.sql), so another account's connection or notebook
// is answered with the same 404 as an id that was never used.
//
// # What an import does
//
//  1. The connection is read for its owner. One Google has refused is
//     answered with the code the web offers "connect again" for.
//  2. Each pick is checked without asking Google: a folder, a shortcut or
//     a Google-native kind with no export is not importable, and neither
//     is a file the connection's parser does not read. A Google Doc is
//     judged by the name it will be stored under ("Plan.docx").
//  3. One transaction makes a document per importable pick, pending and
//     waiting for its file, or finds the one an earlier import of the same
//     Drive file made, and links the notebook. It commits before anything
//     is published, so the library shows the files at once.
//  4. The job is published, naming the picked files. A queue that is down
//     marks the waiting documents failed with a reason and answers 503: a
//     document "downloading" with no job behind it would spin for ever.
//
// A document that exists and is ready is left as it is here: whether its
// file changed in Drive is the worker's to find out, and it fetches only
// what did.
//
// # The parser
//
// A Drive document names no parser of its own. It is read with its
// connection's (internal/connectorparser), or the deployment's default
// when the connection names none, so changing the connection's parser
// changes how every later sync of its files is read.
//
// # What changed from the Python
//
//   - The picks were also remembered in the connection's config, a second
//     list that could disagree with the documents. The documents are the
//     only list.
//   - "Sync now" took a date range and a folder selection that the Drive
//     sync never used; the route takes neither, and its answer no longer
//     reports a range nothing was read from.
//   - A queue that was down left the pending documents pending. They are
//     marked failed, with a reason.
package drivesync

import (
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivesyncjob"
)

// The paths.
const (
	ImportPath = "/api/v1/connectors/{connector_id}/google-drive/import"
	IndexPath  = "/api/v1/search-source-connectors/{connector_id}/index"
)

// ServedRoutes is every route of this package as "METHOD path".
func ServedRoutes() []string {
	return []string{http.MethodPost + " " + ImportPath, http.MethodPost + " " + IndexPath}
}

// MaxPickedFiles is the most files one import takes; the web's picker
// refuses a larger selection before it is sent.
const MaxPickedFiles = drivesyncjob.MaxFiles

// bodyLimit bounds the import's body: fifty picks of a 512-character name
// at the very most.
const bodyLimit = 256 << 10

// What the routes say, and the codes the web tells them apart by.
const (
	connectionAbsent  = "Google Drive connection not found"
	connectorAbsent   = "Connector not found"
	notebookAbsent    = "Notebook not found"
	notConfiguredCode = "google_drive_not_configured"
	notConfigured     = "Google Drive is not set up on this server"
	reconnectCode     = "google_drive_reconnect"
	reconnect         = "Google no longer accepts this connection; connect again"
	switchedOff       = "This connection is switched off"
	nothingToSync     = "This kind of connection has nothing to sync"
	queueDown         = "The import could not be started because the job queue did not answer. Try again in a moment."
	syncStarted       = "Google Drive sync started in the background."
)

// QueueingFailed is the reason a picked file's document carries when its
// job could not be published. It is written for the person looking at the
// document list.
const QueueingFailed = "The import could not be started because the job queue did not answer. Import the file again."
