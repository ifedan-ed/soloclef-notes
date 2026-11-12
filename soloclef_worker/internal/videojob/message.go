package videojob

import "git.danvics.com/danvics/soloclef/soloclef_celery/produce"

// Message is the message a job is published as, for a producer in this
// tier (the reconciler, internal/jobreconcile). It is the message the API
// tier publishes (soloclef_assistant/deliverables/video.go,
// PublishVideo), field for field: the task, the one keyword argument Run
// reads, and the row's task id, which is what the claim accepts. The queue
// is left to the routing table, which puts this task on the heavy queue.
func Message(jobID int64, taskID string) produce.Message {
	return produce.Message{
		Task: Task, Kwargs: map[string]any{"job_id": jobID}, ID: taskID,
		// Keyword arguments only: the (empty) positional arguments are
		// written as a tuple, as Celery's send_task writes them.
		ArgsAsTuple: true,
	}
}
