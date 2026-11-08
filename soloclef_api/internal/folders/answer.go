package folders

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// TaskName is the Celery task delete_folder's enqueue publishes. The function
// is delete_folder_documents_task and the name it is registered under is this
// one (app/tasks/celery_tasks/document_tasks.py), which is the name that has to
// travel: a worker looks the task up by it. It is in no routing rule, so it
// goes on the default queue.
const TaskName = "delete_folder_documents_background"

// DeletionAnswer is the route's whole response body. It has no response model,
// so it is Starlette's json.dumps of a plain dict and the count is an integer.
func DeletionAnswer(queued int) *pythonjson.Object {
	body := pythonjson.NewObject()
	body.Set("message", DeletionStarted)
	body.Set("documents_queued_for_deletion", int64(queued))
	return body
}

// DeletionOf is the message
// `delete_folder_documents_task.delay(document_ids, folder_subtree_ids=…)`
// publishes: one positional list and one keyword list.
//
// The call shape matters. `.delay` packs its positional arguments into a tuple,
// so argsrepr reads `([100, 103, 104],)` — a tuple of one holding a list —
// where an apply_async(args=[…]) site would have written `[[100, 103, 104]]`.
// produce.Delay carries that distinction (soloclef_celery/produce).
//
// Both lists are sent even when empty: a folder with no documents still
// enqueues, with `([],)`.
func DeletionOf(documentIDs, subtreeIDs []int64, context produce.LogContext) produce.Message {
	message := produce.Delay(TaskName, numbers(documentIDs))
	message.Kwargs = map[string]any{"folder_subtree_ids": numbers(subtreeIDs)}
	message.Context = context
	return message
}

// numbers is a Python list of ints, in the order the query returned them. An
// empty result is an empty list and not null, because that is what
// `list(...)` of no rows is.
func numbers(ids []int64) []any {
	list := make([]any, 0, len(ids))
	for _, id := range ids {
		list = append(list, id)
	}
	return list
}
