// Package workspacedeletion serves DELETE /api/v1/workspaces/{workspace_id}.
//
// It is the second write handed over that owes a Celery message, and it is the
// same shape as internal/documentdeletion for a reason: the Python route
// writes a soft-delete marker, commits, enqueues the cascade, and puts the
// marker back when the enqueue fails. What is its own here is the marker — a
// name prefix in a column 100 characters wide — and the refusal that nobody
// deletes their only workspace.
package workspacedeletion

// The path, as app/routes/workspaces_routes.py mounts it under /api/v1.
const Path = "/api/v1/workspaces/{workspace_id}"

// SettingsDelete is Permission.SETTINGS_DELETE, which only owners hold by
// default.
const SettingsDelete = "settings:delete"

// TaskName is the task delete_workspace_task is registered under
// (app/tasks/celery_tasks/document_tasks.py). The name is older than the
// workspace wording and is the name a worker looks the task up by, so it is
// the name that travels.
const TaskName = "delete_search_space_background"

// Marker is the soft-delete prefix, and NameLimit is the width of the column
// it has to fit in (workspaces.name is varchar(100)).
const (
	Marker    = "[DELETING] "
	NameLimit = 100
)

// The Python route's own wording, word for word. The last-workspace refusal is
// app/workspace_membership/last_workspace.py's.
const (
	DeleteDenied    = "You don't have permission to delete this workspace"
	NotFound        = "Workspace not found"
	AlreadyDeleting = "Workspace is already being deleted."
	LastWorkspace   = "This is your only workspace, so it can't be deleted. Create another " +
		"workspace first, then delete this one."
	QueueFailed = "Failed to queue background deletion. Please try again."
	Deleted     = "Workspace deleted successfully"
)

// MarkedName is the name the route writes: the marker, and as much of the
// existing name as still fits.
//
// The cut is in characters, not bytes, because Python slices a str: a name of
// em dashes and curly quotes would lose three times as much to a byte cut, and
// the two tiers would leave different names in the column.
func MarkedName(name string) string {
	kept := []rune(name)
	if room := NameLimit - len(Marker); len(kept) > room {
		kept = kept[:room]
	}
	return Marker + string(kept)
}

// BeingDeleted is `(db_workspace.name or "").startswith("[DELETING] ")`.
func BeingDeleted(name string) bool {
	return len(name) >= len(Marker) && name[:len(Marker)] == Marker
}
