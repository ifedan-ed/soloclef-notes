// Package documentdeletion serves DELETE /api/v1/documents/{document_id},
// and is the removal of a document and everything stored for it.
//
// # What a delete removes, and in what order
//
//  1. The document is marked "deleting" and that is committed. From this
//     instant it is out of search, and the indexing worker will not start
//     on it or finish a job it had begun.
//  2. Its chunks, a batch at a time: the batch's points are removed from the
//     vector store, then the batch's rows are deleted.
//  3. Its stored files, each from the store its record names: the
//     document's own, and, when it is an artifact's document, the
//     artifact's files and a video's narration (queries.sql, FilesOf).
//  4. The document row. Its file rows, its artifact, versions and notebook
//     links cascade.
//
// (removal.go says why each step is where it is.)
//
// # Safe to retry
//
// Every step can be repeated: removing a point or an object that is already
// gone is not an error, and a batch of chunks that is already deleted is
// simply not found by the next read. A delete that fails partway leaves the
// document marked "deleting", holding whatever was not yet removed, and
// deleting it again carries on from there. Nothing is ever left that no row
// points at: the row that knows a file's key is deleted only after the file
// is.
//
// # Better than the route it replaces
//
// The Python route marked the document and enqueued a Celery task that did
// the work, so "deleted successfully" meant "queued", and a document that
// was pending or processing could not be deleted at all: one stuck in
// processing had to be repaired in the database. Here the answer is given
// when the document is gone, and a document in any state can be deleted,
// because the indexing worker checks the marker before it writes
// (soloclef_worker/internal/indexstore). The Python task also left every
// vector in the vector store for a sweeper to find.
//
// # Whose document
//
// The route removes a document only for the account that owns it: it asks
// the database for the row by id and owner together, and another account's
// document is answered exactly as an id that names nothing, so ids cannot
// be probed for.
//
// # One removal, four callers
//
// A note and an artifact are documents too, and their deletes
// (internal/notes, internal/artifacts) remove them with the same Removal,
// as does the removal of a connection with its documents. Nothing in this
// tier hands a single document's deletion to a queue any more. Removal
// takes a document id alone: each caller has already read the row with its
// owner, and the removal's own statements cannot reach another document
// (every one names the document id). StateOf is what the note and artifact
// routes read a document's state with.
package documentdeletion

import (
	"encoding/json"
	"errors"
)

// Path is the route's path.
const Path = "/api/v1/documents/{document_id}"

// What a caller is told.
const (
	Deleted = "Document deleted successfully"
	// Unfinished is the answer when a removal stopped partway. The document
	// stays marked, out of search, and the same request finishes the job.
	Unfinished = "The document could not be fully deleted right now. Delete it again to finish."
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
