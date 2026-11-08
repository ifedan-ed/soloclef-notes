package documentdeletion

import (
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// NotFound is the 404's wording, which names the id the caller asked for.
func NotFound(documentID int64) string {
	return "Document with id " + strconv.FormatInt(documentID, 10) + " not found"
}

// Message is the route's whole response body.
func Message() *pythonjson.Object {
	body := pythonjson.NewObject()
	body.Set("message", Deleted)
	return body
}

// DeletionOf is the Celery message `delete_document_task.delay(document_id)`
// publishes: one positional argument, the publishing request's log context,
// and nothing else, so every other option is Celery's default.
func DeletionOf(documentID int64, context produce.LogContext) produce.Message {
	message := produce.Delay(TaskName, documentID)
	message.Context = context
	return message
}
