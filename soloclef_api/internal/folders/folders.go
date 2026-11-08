// Package folders serves the nine routes of app/routes/folders_routes.py that
// are about a folder:
//
//	POST   /api/v1/folders                       create_folder
//	GET    /api/v1/folders/{folder_id}            get_folder
//	GET    /api/v1/folders/{folder_id}/breadcrumb get_folder_breadcrumb
//	PATCH  /api/v1/folders/{folder_id}/watched    stop_watching_folder
//	PUT    /api/v1/folders/{folder_id}            update_folder
//	PUT    /api/v1/folders/{folder_id}/move       move_folder
//	PUT    /api/v1/folders/{folder_id}/reorder    reorder_folder
//	DELETE /api/v1/folders/{folder_id}            delete_folder
//
// `GET /api/v1/folders`, the flat list beside them, is deliberately **not**
// here, and the reason is in "The folder list's order is not a property either
// tier has" in custom/docs/go-api-tier.md: its `ORDER BY position` has ties —
// every first child of every parent is "a0" — and which order a tie comes back
// in is decided by whether Postgres is running that connection's prepared
// statement on a custom or a generic plan. The Python tier answers one order
// for a connection's first four requests and the other from the fifth
// onwards, so there is no order to mirror.
//
// The eight that are here were taken together because they are the document
// browser's left-hand pane: one table, one renderer, one pair of gates, and a screen a user touches
// constantly. The two document-move routes in the same Python module are
// deliberately **not** here — they change `documents.folder_id`, which fires
// app/session_events.py's `document.entered_folder` hook, so they are the
// event bus's slice and not this one.
//
// # What these writes owe, which is more than the inventory said
//
// The delete enqueues `delete_folder_documents_background`, which is what
// phase 6's producer unblocked. But five of these routes also call
// app/knowledge_store/service.py — record_created_folder, record_moved_folder,
// record_removed_folder — and that is a **git** content store, not a queue and
// not a row. Every one of those verbs self-guards on
// knowledge_store_enabled_for(workspace_id), which is the process-wide
// KNOWLEDGE_STORE_ENABLED and the workspace's own `knowledge_store_enabled`
// flag, both of which are off in this deployment; with the master switch off
// they are reads and no-ops, and these routes are identical here.
//
// With the master switch **on** they are not, and this tier does not pretend
// otherwise: it hands every folder write back to the Python tier for the whole
// deployment (see StoreDelegate). That is the same decision
// internal/pythondelegate exists for — where this tier cannot be sure, it
// forwards — and it costs nothing today because no workspace is flipped.
package folders

import "time"

// The paths, as app/routes/folders_routes.py mounts them under /api/v1.
const (
	CreatePath     = "/api/v1/folders"
	ReadPath       = "/api/v1/folders/{folder_id}"
	BreadcrumbPath = "/api/v1/folders/{folder_id}/breadcrumb"
	WatchedPath    = "/api/v1/folders/{folder_id}/watched"
	RenamePath     = "/api/v1/folders/{folder_id}"
	MovePath       = "/api/v1/folders/{folder_id}/move"
	ReorderPath    = "/api/v1/folders/{folder_id}/reorder"
	DeletePath     = "/api/v1/folders/{folder_id}"
)

// The permissions the nine routes gate on. All four are Permission members on
// *documents*, not on folders: a folder is part of the document tree there and
// has no permissions of its own.
const (
	DocumentsCreate = "documents:create"
	DocumentsRead   = "documents:read"
	DocumentsUpdate = "documents:update"
	DocumentsDelete = "documents:delete"
)

// The Python routes' own wording for a member who holds the workspace but not
// the permission. Each route words it differently — "create", "read",
// "update", "move", "reorder", "delete" — and three of them share the update
// permission while wording it three ways, so these are per route and not per
// permission.
const (
	CreateDenied  = "You don't have permission to create folders in this workspace"
	ReadDenied    = "You don't have permission to read folders in this workspace"
	UpdateDenied  = "You don't have permission to update folders in this workspace"
	MoveDenied    = "You don't have permission to move folders in this workspace"
	ReorderDenied = "You don't have permission to reorder folders in this workspace"
	DeleteDenied  = "You don't have permission to delete folders in this workspace"
)

// The refusals that are not about who is asking.
const (
	NotFound           = "Folder not found"
	ParentNotFound     = "Parent folder not found"
	ParentElsewhere    = "Parent folder belongs to a different workspace"
	TargetNotFound     = "Target parent folder not found"
	TargetElsewhere    = "Cannot move folder to a different workspace"
	MovedIntoItself    = "A folder cannot be moved into itself."
	MovedIntoOwnSubree = "Cannot move a folder into one of its own descendants."
	QueueFailed        = "Could not queue folder deletion. Documents have been restored."
	WatchUpdated       = "Folder watch status updated"
	DeletionStarted    = "Folder deletion started"
)

// MaxNameLength is FolderCreate's and FolderUpdate's `max_length` for `name`,
// which is also the column's width.
const MaxNameLength = 255

// Folder is a folders row as FolderRead renders it.
type Folder struct {
	ID          int64
	Name        string
	Position    string
	ParentID    *int64
	WorkspaceID int64
	CreatedByID *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// Metadata is the `metadata` column's own bytes. The column is jsonb, so
	// Postgres has already decided the key order and the numeric spelling and
	// both tiers read the same text back out of it.
	Metadata []byte
}

// Breadcrumb is one link of the ancestor chain, as FolderBreadcrumb renders it.
type Breadcrumb struct {
	ID   int64
	Name string
}

// nowOr reads the clock a write's `updated_at` comes from. It is the
// application's clock on both tiers — the column has no default and the ORM's
// onupdate is a Python lambda — so it is a seam a test can hold still.
func nowOr(now func() time.Time) time.Time {
	if now == nil {
		return time.Now().UTC()
	}
	return now().UTC()
}
