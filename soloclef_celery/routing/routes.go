// Package routing decides which queue a task goes on.
//
// A producer has to agree with the consumers exactly: a task published to
// the default queue when the heavy one is where it belongs is a
// minutes-long job holding up a file upload, and one published to a queue
// no worker consumes is a job that never runs. The table is small and it is
// configuration, not logic, so it is written here once and every producer
// (the API tier, the worker tier's own publishes) routes by it.
//
// It began as app/celery_app.py's task_routes. The Python tier is retired,
// so the table is now the Go stack's own: the tasks a Go job runs, on the
// queue the Go worker consumes them from.
package routing

// Heavy is the minutes-long generation, given a queue of its own so a burst
// of it never holds up the fast queue: the podcast (draft and render in one
// job) and the video.
var Heavy = []string{
	"podcast.draft_transcript",
	"deliverables.execute_queued",
}

// heavySuffix builds the heavy queue's name from the default queue's, so a
// deployment that renames its default queue renames both.
const heavySuffix = ".heavy"

// Table is the routing of one deployment, named by its default queue
// (CELERY_TASK_DEFAULT_QUEUE).
type Table struct {
	Default string
}

// HeavyQueue is the heavy queue's name.
func (t Table) HeavyQueue() string { return t.Default + heavySuffix }

// QueueFor is the queue a task goes on: the heavy queue for a heavy task,
// the default queue for every other.
func (t Table) QueueFor(task string) string {
	for _, name := range Heavy {
		if name == task {
			return t.HeavyQueue()
		}
	}
	return t.Default
}

// Queues is every queue this table routes to, in the order the Go worker
// consumes them.
func (t Table) Queues() []string {
	return []string{t.Default, t.HeavyQueue()}
}
