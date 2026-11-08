package workspacedeletion

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// Message is the route's whole response body.
func Message() *pythonjson.Object {
	body := pythonjson.NewObject()
	body.Set("message", Deleted)
	return body
}

// DeletionOf is the Celery message `delete_workspace_task.delay(workspace_id)`
// publishes: one positional argument, the publishing request's log context,
// and nothing else, so every other option is Celery's default.
func DeletionOf(workspaceID int64, context produce.LogContext) produce.Message {
	message := produce.Delay(TaskName, workspaceID)
	message.Context = context
	return message
}
