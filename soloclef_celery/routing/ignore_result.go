package routing

import "slices"

// IgnoresResult reports whether a task declares `ignore_result=True`, which is
// a property of the task rather than of the call that publishes it.
//
// Celery reads it off the task object when it builds the message
// (`amqp.as_task_v2`), so `push_send.delay(payload)` carries
// `"ignore_result": true` in its headers without any call site saying so. A
// producer that did not know which tasks declare it would publish `false` for
// them — and a worker reading `false` writes a `celery-task-meta-<id>` result
// row for a task whose outcome nobody will ever read.
//
// This was found by comparing the comment routes' push against the Python
// tier's (custom/docs/go-api-tier.md, phase 8). The same mistake was already
// live in the worker tier's beat, whose `push-receipts` tick is published every
// fifteen minutes and had been carrying `false` since the beat moved to Go.
//
// The list began as a grep for ignore_result over the Python app. Of its
// three, push_send and push_receipts went with push, and the Go stack has no
// task of its own that a reader polls for: only the naming is left, whose
// outcome is the notebook's title.
var ignoresResult = []string{
	"notebook_autoname",
}

// IgnoresResult is the question a producer asks before it writes the header.
func IgnoresResult(task string) bool {
	return slices.Contains(ignoresResult, task)
}
