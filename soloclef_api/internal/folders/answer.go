package folders

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// TaskName is the job a folder delete queues: it removes the marked
// documents and then the folders (soloclef_worker/internal/folderdeletion).
// A worker looks the job up by this name. It is in no routing rule, so it
// goes on the default queue.
const TaskName = "delete_folder_documents_background"

// DeletionAnswer is the delete's whole answer.
func DeletionAnswer(queued int) *pythonjson.Object {
	body := pythonjson.NewObject()
	body.Set("message", DeletionStarted)
	body.Set("documents_queued_for_deletion", int64(queued))
	return body
}

// DeletionOf is the job's message: the documents to remove as its one
// positional argument, and by keyword the folders to remove after them and
// the account they all belong to. The job names that account in every
// statement, so an id in either list that is not the account's removes
// nothing; the Python job was given ids alone and deleted whatever they
// named.
//
// Both lists are sent even when empty: a folder with no documents still has
// to be deleted, and the job is what deletes it.
func DeletionOf(userID string, documentIDs, subtreeIDs []int64, context produce.LogContext) produce.Message {
	message := produce.Delay(TaskName, numbers(documentIDs))
	message.Kwargs = map[string]any{"folder_subtree_ids": numbers(subtreeIDs), "user_id": userID}
	message.Context = context
	return message
}

// numbers is a JSON list of the ids, in order; an empty list and not null
// when there are none, because the job iterates it.
func numbers(ids []int64) []any {
	list := make([]any, 0, len(ids))
	for _, id := range ids {
		list = append(list, id)
	}
	return list
}
