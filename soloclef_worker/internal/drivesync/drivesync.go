// Package drivesync keeps the Google Drive files an account imported the
// same here as they are in Drive.
//
// # The job
//
// `index_google_drive_files` (soloclef_connectors/drivesyncjob), published
// by the API when a person imports picked files or asks for a sync now, and
// by this package's own tick for each connection whose schedule is due
// (tick.go). It names a connection and the account it belongs to; every
// statement names both, so a message naming another account's connection
// finds nothing.
//
// With files named, it fetches those files: the ones just picked, whose
// documents the API has already made and left waiting for their bytes. With
// none, it asks Drive's change feed what happened since last time and
// fetches what changed.
//
// # One file
//
// A file is read from Drive in one stream into the file store, exactly as
// an upload is kept, and its document handed to the indexer
// (index_document), which parses it with the parser the document names, or
// its connection's. A Google Doc, Sheet or Slides deck has no bytes of its
// own and is exported (soloclef_connectors/drivefile says as what).
//
// A file is fetched only when it is owed (its document is waiting for it)
// or when Drive says its content is not what was last stored: by MD5 for a
// file with bytes, by modification time for a Google-native one, which has
// no checksum. A file that was only renamed is renamed here.
//
// # The change feed
//
// Drive hands out a page token that stands for "now"; asking for changes
// since a token answers what changed and the next token. The token is kept
// on the connection (`start_page_token` in its config).
//
// Connecting stores no token, so the first job that finds none fetches one
// BEFORE it does anything else, so that nothing that changes while it works
// is missed, and, if it was asked to sync everything, checks every
// imported file once, since there is no feed position to ask from. The same
// happens when Drive says a kept token is too old to use.
//
// The feed is the account's whole Drive (under the `drive.file` scope,
// whatever of it this app may see). A change is acted on only when the
// account has a document for that file: the documents are the list of what
// was picked. A file trashed or removed in Drive keeps its document: it
// was imported into the notebook and stays there. The document's metadata
// records when the file went (google_drive.gone_from_drive_at), so the UI
// can say so, and the file is no longer synced, until Drive has it again
// (restored from the bin, or picked again).
//
// # Better than the code it replaces
//
//   - Python read every picked file's metadata on every sync, one request a
//     file. The change feed is one request when nothing changed.
//   - Python downloaded each file to a temporary file and parsed it in the
//     job. A download here is one stream into the store, and the parsing is
//     the indexer's, as for every other document.
//   - Picks were remembered twice, as documents and as a list in the
//     connection's config, and the two could disagree. The documents are
//     the only list now.
//   - A document the person deleted here came back on the next sync, since
//     the config still listed its file. It stays deleted.
//   - A file that failed to download was "previously failed" for ever. It
//     is fetched again when it is next picked or next changes.
//   - Google's reply to a redirect is never followed, and a token is sent
//     only to the two addresses the deployment names.
//
// # What is deliberately not carried over
//
// Folder sync. The grant is `drive.file`: the app reaches only files a
// person picked in Google's Picker, and picking a folder gives access to
// nothing inside it, so there is no folder to walk.
package drivesync

import (
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivesyncjob"
)

// Task is the job's name.
const Task = drivesyncjob.Task

// StaleAfter is how long a document may wait for its download before the
// job that owed it is taken to be gone (its message lost, its worker
// killed). A sync of everything then fetches it. It is far longer than a
// queued import waits in any ordinary hour, so a download that is about to
// start is not started twice.
const StaleAfter = 15 * time.Minute

// tokenMargin is how much life an access token must have left to be used
// without renewing it: longer than one file's download is allowed to take,
// so a token does not run out in the middle of a file.
const tokenMargin = 10 * time.Minute

// The reasons a failed file carries, written for the person looking at the
// document list. The technical error goes to the log.
const (
	reasonAuth        = "Google no longer accepts this connection. Reconnect Google Drive and import the file again."
	reasonGone        = "The file is no longer in Google Drive, or this app can no longer open it. Pick it again in Google Drive."
	reasonRefused     = "Google Drive would not hand over this file. A Google Doc, Sheet or Slides deck that is too large to export cannot be imported."
	reasonUnavailable = "Google Drive could not be reached, or stopped sending the file. Import it again in a moment."
	reasonStorage     = "The file could not be stored right now. Import it again in a moment."
	reasonEmpty       = "The file is empty."
	reasonKind        = "This kind of Google Drive item cannot be imported."
	reasonQueue       = "Indexing could not be started because the job queue did not answer. Re-index the document to try again."
)
