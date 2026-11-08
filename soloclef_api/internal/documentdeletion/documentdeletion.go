// Package documentdeletion serves DELETE /api/v1/documents/{document_id}.
//
// It is the first write handed over that owes a Celery message
// (internal/celeryqueue). The route itself is small — three refusals, one
// column written, one task enqueued — and the reason it is interesting is the
// order of the last two: the Python route commits the marker first and
// enqueues after, and reverts the marker when the enqueue fails. A document
// marked "deleting" with no task to delete it is invisible to search and
// deleted by nothing, so the revert is the route, not a nicety.
package documentdeletion

import (
	"encoding/json"
	"errors"
)

// The path, as app/routes/documents_routes.py mounts it under /api/v1.
const Path = "/api/v1/documents/{document_id}"

// DocumentsDelete is Permission.DOCUMENTS_DELETE.
const DocumentsDelete = "documents:delete"

// TaskName is the Celery task the route enqueues. The task function is
// delete_document_task and the name it is registered under is this one
// (app/tasks/celery_tasks/document_tasks.py), which is the name that has to
// travel: a worker looks the task up by it.
const TaskName = "delete_document_background"

// The Python route's own wording, word for word.
const (
	DeleteDenied   = "You don't have permission to delete documents in this workspace"
	BeingProcessed = "Cannot delete document while it is pending or being processed. " +
		"Please wait for processing to complete."
	AlreadyDeleting = "Document is already being deleted."
	QueueFailed     = "Failed to queue background deletion. Please try again."
	Deleted         = "Document deleted successfully"
)

// ErrStatusIsNotAnObject is a status column holding something a dict's .get
// cannot be called on, which is a 500 on the Python tier for the same row.
var ErrStatusIsNotAnObject = errors.New("documentdeletion: status is not an object")

// StateOf is `document.status.get("state") if document.status else None`.
//
// It answers the empty string for every status Python reads as no state at
// all, which includes the falsy ones it never calls .get on — {} and the
// handful of non-objects that are falsy — and for a state that is not a
// string, because such a value equals none of the three the route compares
// against. A status that is truthy and not an object is the one case Python
// raises on, and this returns that rather than inventing a state.
func StateOf(raw []byte) (string, error) {
	var status any
	if len(raw) == 0 {
		return "", nil
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return "", err
	}
	switch value := status.(type) {
	case nil:
		return "", nil
	case map[string]any:
		state, isString := value["state"].(string)
		if !isString {
			return "", nil
		}
		return state, nil
	case bool:
		if value {
			return "", ErrStatusIsNotAnObject
		}
		return "", nil
	case float64:
		if value != 0 {
			return "", ErrStatusIsNotAnObject
		}
		return "", nil
	case string:
		if value != "" {
			return "", ErrStatusIsNotAnObject
		}
		return "", nil
	case []any:
		if len(value) > 0 {
			return "", ErrStatusIsNotAnObject
		}
		return "", nil
	default:
		return "", ErrStatusIsNotAnObject
	}
}

// RefusalForState is the two 409s, in the order the Python route raises them.
// It returns the empty string when the state does not refuse the delete.
func RefusalForState(state string) string {
	switch state {
	case "pending", "processing":
		return BeingProcessed
	case "deleting":
		return AlreadyDeleting
	}
	return ""
}
