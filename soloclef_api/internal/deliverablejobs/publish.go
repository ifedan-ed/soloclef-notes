package deliverablejobs

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
)

// PublishVideo puts a queued video's message on this tier's task queue: the
// one message every tier sends for a video (deliverables.VideoMessage), with
// this tier's log context added. path is the route the job was asked for
// on, which is what that context is read from.
//
// It is here and not beside the message because the queue is this tier's:
// the worker publishes the same message through its own producer.
func PublishVideo(queue celeryqueue.Enqueuer, path string) func(ctx context.Context, jobID int64, taskID string) error {
	return func(ctx context.Context, jobID int64, taskID string) error {
		message := deliverables.VideoMessage(jobID, taskID)
		message.Context = celeryqueue.LogContextFor(path, "", "")
		_, err := queue.Enqueue(ctx, message)
		return err
	}
}
