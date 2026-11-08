// Package routing decides which queue a task goes on, as
// app/celery_app.py's task_routes decides it.
//
// A producer has to agree with that table exactly: a task published to the
// default queue when Celery would have routed it to the heavy one is a
// minutes-long job holding up file uploads, and one published to a queue no
// worker consumes is a job that never runs. The table is small and it is
// configuration, not logic, so it is mirrored here and compared against the
// running app's own conf in routing's interop test.
package routing

// Heavy is app/celery_app.py's HEAVY_TASKS: minutes-long generation, given a
// queue of its own so a burst of it never holds up the fast queue.
var Heavy = []string{
	"podcast.draft_transcript",
	"podcast.render_audio",
	"generate_video_presentation",
	"deliverables.execute_queued",
}

// Connectors is the two tasks task_routes names one at a time: connector
// indexing, and the whole-workspace rebuild that embeds every document. The
// per-save index task is deliberately not here — search freshness is
// user-facing, so it stays on the fast queue.
var Connectors = []string{
	"index_google_drive_files",
	"reindex_knowledge_store",
}

// The suffixes CONNECTORS_QUEUE and HEAVY_QUEUE are built from the default
// queue name, so a deployment that renames its default queue renames all
// three.
const (
	connectorsSuffix = ".connectors"
	heavySuffix      = ".heavy"
)

// Table is the routing of one deployment, named by its default queue
// (CELERY_TASK_DEFAULT_QUEUE).
type Table struct {
	Default string
}

// ConnectorsQueue is CONNECTORS_QUEUE.
func (t Table) ConnectorsQueue() string { return t.Default + connectorsSuffix }

// HeavyQueue is HEAVY_QUEUE.
func (t Table) HeavyQueue() string { return t.Default + heavySuffix }

// QueueFor is task_routes' own answer for one task name, falling through to
// task_default_queue exactly as Celery does for a task the table does not
// name.
func (t Table) QueueFor(task string) string {
	for _, name := range Heavy {
		if name == task {
			return t.HeavyQueue()
		}
	}
	for _, name := range Connectors {
		if name == task {
			return t.ConnectorsQueue()
		}
	}
	return t.Default
}

// Queues is every queue this table routes to, in the order the deployment's
// own workers consume them (docker/docker-compose.yml).
func (t Table) Queues() []string {
	return []string{t.Default, t.ConnectorsQueue(), t.HeavyQueue()}
}
