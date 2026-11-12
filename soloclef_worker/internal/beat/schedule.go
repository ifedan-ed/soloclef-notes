package beat

import (
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
)

// Entry is one scheduled task: what to publish, how often, and how long a
// tick nobody picked up stays worth running.
type Entry struct {
	// Name is the entry's identity. It is the fence's key, so renaming one
	// is renaming the thing that stops it firing twice.
	Name string

	Task string
	Cron Crontab

	// Expires is how long a tick stays worth running. Every entry sets one,
	// and it is what makes a tick safe to miss: a tick nobody ran within
	// its window is dropped and the next one replaces it.
	Expires time.Duration

	// Queue is where the tick is published, from the routing table every
	// producer uses (soloclef_celery/routing).
	Queue string
}

// scheduled is the Go stack's periodic work. It is empty, on purpose.
//
// app/celery_app.py scheduled seventeen entries. None of them means
// anything on the user-keyed schema, and each was removed for a reason:
//
//   - billing.roll_periods, billing.release_stale_holds,
//     billing.reconcile_payments, billing.check_ledger: billing is cut.
//   - prune_knowledge_store_working_copies, check_knowledge_store_drift,
//     reindex_drifted_workspaces: the git knowledge store is cut.
//   - check_periodic_schedules: its only target was the Google Drive sync,
//     which has no Go body and which nothing in the Go stack can connect.
//   - push_receipts: nothing in the Go stack sends a push.
//   - cleanup_stale_indexing_notifications: nothing in the Go stack writes
//     an indexing notification; a document's status is its progress.
//   - purge_refresh_tokens: the Go stack signs in through the identity
//     provider and issues no refresh tokens of its own.
//   - evict_etl_cache, evict_embedding_cache: caches of the Python indexer;
//     the Go indexer keeps none.
//   - sweep_model_compatibility: probed OpenRouter's catalogue; models are
//     now the proxy's names, chosen by the account.
//   - deliverables.reconcile_queued: republished queued videos. It still
//     runs, but not from here: it is a message that schedules its own
//     successor through the consumer's delayed delivery
//     (internal/jobreconcile), so it needs no scheduler kept single.
//   - automation_schedule_select, automation_runs_sweep_overdue: nothing in
//     the Go stack creates an automation.
//
// A worker started with SOLOCLEF_WORKER_BEAT on is refused at start-up
// (ErrNoSchedule) rather than holding a lease over nothing. An entry added
// here is published to the queue the routing table gives its task.
var scheduled = []struct {
	name    string
	task    string
	cron    Crontab
	expires time.Duration
}{}

// Schedule is the entries, each routed as every producer routes its task.
func Schedule(defaultQueue string) []Entry {
	routes := routing.Table{Default: defaultQueue}
	entries := make([]Entry, 0, len(scheduled))
	for _, line := range scheduled {
		entries = append(entries, Entry{
			Name: line.name, Task: line.task, Cron: line.cron, Expires: line.expires,
			Queue: routes.QueueFor(line.task),
		})
	}
	return entries
}
