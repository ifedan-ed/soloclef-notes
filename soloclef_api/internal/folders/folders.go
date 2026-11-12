// Package folders is the document browser's folder tree: the folders of an
// account, and where its documents are filed in them.
//
//	POST   /api/v1/folders                        create a folder
//	GET    /api/v1/folders                        the account's folders, flat
//	GET    /api/v1/folders/{folder_id}            one folder
//	GET    /api/v1/folders/{folder_id}/breadcrumb its ancestors, root first
//	PATCH  /api/v1/folders/{folder_id}/watched    stop watching a folder
//	PUT    /api/v1/folders/{folder_id}            rename
//	PUT    /api/v1/folders/{folder_id}/move       move under another parent
//	PUT    /api/v1/folders/{folder_id}/reorder    reorder among its siblings
//	DELETE /api/v1/folders/{folder_id}            delete, with its documents
//	PUT    /api/v1/documents/{document_id}/move   file one document
//	PUT    /api/v1/documents/bulk-move            file several
//
// # Whose folders
//
// A folder belongs to one account, and a route answers about the caller's
// folders and nobody else's. The account comes from the request's principal
// and is never read from the request. Every statement names it
// (queries.sql), so a folder or a document of another account is answered
// exactly as one that was never made: the same 404, on every route and every
// method. That includes the folder a write points at. A parent, a move's
// target and the folder a document is filed in must each be the caller's
// own, and one that is not is "not found", never "elsewhere".
//
// # How a route is built
//
// A route reads and writes through Rows, which Store implements over
// Postgres. The routes hold the rules (what is refused, in what order, what
// is answered) and the statements hold whose rows they are. The tests run
// the routes over an in-memory Rows, and the same cases again over a real
// database when one is named (database_test.go).
//
// # What a write owes
//
// A document that arrives in a folder is something an automation can be
// waiting for, so the two document moves announce it once the move is
// stored (arrival.go). The delete removes nothing itself: it marks the
// subtree's documents, and queues the job that removes them and then the
// folders (delete_route.go).
package folders

import "time"

// The paths. CreatePath and ListPath are one path and two methods, as are
// ReadPath, RenamePath and DeletePath.
const (
	CreatePath       = "/api/v1/folders"
	ListPath         = "/api/v1/folders"
	ReadPath         = "/api/v1/folders/{folder_id}"
	BreadcrumbPath   = "/api/v1/folders/{folder_id}/breadcrumb"
	WatchedPath      = "/api/v1/folders/{folder_id}/watched"
	RenamePath       = "/api/v1/folders/{folder_id}"
	MovePath         = "/api/v1/folders/{folder_id}/move"
	ReorderPath      = "/api/v1/folders/{folder_id}/reorder"
	DeletePath       = "/api/v1/folders/{folder_id}"
	DocumentMovePath = "/api/v1/documents/{document_id}/move"
	BulkMovePath     = "/api/v1/documents/bulk-move"
)

// What the folder routes say when they refuse, and when they succeed with
// nothing but a sentence.
const (
	NotFound           = "Folder not found"
	ParentNotFound     = "Parent folder not found"
	TargetNotFound     = "Target parent folder not found"
	MovedIntoItself    = "A folder cannot be moved into itself."
	MovedIntoOwnSubree = "Cannot move a folder into one of its own descendants."
	InvalidPosition    = "The neighbouring positions are not ones a folder can be placed between."
	QueueFailed        = "Could not queue folder deletion. Documents have been restored."
	WatchUpdated       = "Folder watch status updated"
	DeletionStarted    = "Folder deletion started"
	NameTakenHere      = "A folder with this name already exists at this location"
	NameTakenAtTarget  = "A folder with this name already exists at the target location"
)

// The same for the two document moves.
const (
	DocumentNotFound     = "Document not found"
	TargetFolderNotFound = "Target folder not found"
	NoDocumentIDs        = "No document IDs provided"
	NoDocumentsFound     = "No documents found"
	DocumentMoved        = "Document moved successfully"
)

// MaxNameLength is the longest name a folder may carry, which is the
// column's width.
const MaxNameLength = 255

// Folder is one folder as the routes answer it. It does not carry its
// owner: every Folder a route holds was read for the caller, so the owner is
// the caller.
type Folder struct {
	ID        int64
	Name      string
	Position  string
	ParentID  *int64
	CreatedAt time.Time
	UpdatedAt time.Time
	// Metadata is the `metadata` column's own bytes: jsonb, so Postgres has
	// already decided the key order and the spelling of every number.
	Metadata []byte
}

// Breadcrumb is one link of a folder's ancestor chain.
type Breadcrumb struct {
	ID   int64
	Name string
}

// Document is what a move needs to know about a document: where it is
// filed now, whether it is ready, and what an arrival says about it.
type Document struct {
	ID          int64
	FolderID    *int64
	Title       string
	ConnectorID *int64
	// Status is the `status` column's own bytes. One thing is read from it:
	// whether the state is "ready".
	Status []byte
	// DocumentType is the `documenttype` value as text.
	DocumentType string
}

// refusal is a request the route will not carry out, and what it says.
type refusal struct {
	Status  int
	Message string
}

// nowOr reads the clock a write's `updated_at` comes from: the route's own,
// when a test holds one still, and the system's otherwise.
func nowOr(now func() time.Time) time.Time {
	if now == nil {
		return time.Now().UTC()
	}
	return now().UTC()
}
