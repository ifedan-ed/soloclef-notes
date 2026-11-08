package beat

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Entry is one line of app/celery_app.py's beat_schedule: what to publish, how
// often, and how long a tick nobody picked up stays worth running.
type Entry struct {
	// Name is the beat_schedule key. It is the fence's identity, so renaming
	// one is renaming the thing that stops it firing twice.
	Name string

	Task string
	Cron Crontab

	// Expires is the entry's options["expires"]. Every entry in this app's
	// schedule sets one, and it is what makes a tick safe to miss: a tick
	// nobody ran within its window is dropped and the next one replaces it.
	Expires time.Duration

	// Queue is where the tick is published, from task_routes.
	Queue string
}

// Queues app/celery_app.py routes to, named here so a routed beat task is
// published where its consumer is listening.
const (
	connectorsSuffix = ".connectors"
	heavySuffix      = ".heavy"
)

// connectorTasks and heavyTasks are task_routes in app/celery_app.py. No beat
// entry is routed off the default queue today; the routing is applied anyway,
// because a schedule entry added for a routed task would otherwise be
// published where nothing consumes it.
var (
	connectorTasks = map[string]bool{
		"index_google_drive_files": true,
		"reindex_knowledge_store":  true,
	}
	heavyTasks = map[string]bool{
		"podcast.draft_transcript":    true,
		"podcast.render_audio":        true,
		"generate_video_presentation": true,
		"deliverables.execute_queued": true,
	}
)

// QueueFor is task_routes: which queue carries a task name.
func QueueFor(task, defaultQueue string) string {
	switch {
	case connectorTasks[task]:
		return defaultQueue + connectorsSuffix
	case heavyTasks[task]:
		return defaultQueue + heavySuffix
	default:
		return defaultQueue
	}
}

// SoloclefSchedule is app/celery_app.py's beat_schedule, entry for entry, in
// the order it is written there. The two settings it reads are the two the
// Python beat reads — CELERY_TASK_DEFAULT_QUEUE and SCHEDULE_CHECKER_INTERVAL
// — so the two schedulers cannot disagree about the cadence or the queue.
//
// It is a table rather than a database read on purpose: Celery Beat's schedule
// is static, and the dynamic part of this app's scheduling is already inside
// check_periodic_schedules, which reads the connectors due on every tick.
func SoloclefSchedule(defaultQueue, checkerInterval string) ([]Entry, error) {
	checker, err := CheckerCrontab(checkerInterval)
	if err != nil {
		return nil, err
	}

	written := []struct {
		name    string
		task    string
		cron    Crontab
		expires time.Duration
	}{
		{"check-periodic-connector-schedules", "check_periodic_schedules", checker, 30 * time.Second},
		{"push-receipts", "push_receipts", Crontab{Minute: "*/15"}, 600 * time.Second},
		{"cleanup-stale-indexing-notifications", "cleanup_stale_indexing_notifications", Crontab{Minute: "*/5"}, 60 * time.Second},
		{"billing-roll-periods", "billing.roll_periods", Crontab{Minute: "7"}, 3000 * time.Second},
		{"billing-release-stale-holds", "billing.release_stale_holds", Crontab{Minute: "*/15"}, 800 * time.Second},
		{"billing-reconcile-payments", "billing.reconcile_payments", Crontab{Minute: "*/10"}, 500 * time.Second},
		{"billing-check-ledger", "billing.check_ledger", Crontab{Minute: "50", Hour: "5"}, 3000 * time.Second},
		{"reconcile-queued-deliverables", "deliverables.reconcile_queued", Crontab{Minute: "*"}, 55 * time.Second},
		{"purge-refresh-tokens", "purge_refresh_tokens", Crontab{Minute: "41", Hour: "3"}, 600 * time.Second},
		{"evict-etl-cache", "evict_etl_cache", Crontab{Minute: "0", Hour: "4"}, 600 * time.Second},
		{"evict-embedding-cache", "evict_embedding_cache", Crontab{Minute: "30", Hour: "4"}, 600 * time.Second},
		{"prune-knowledge-store-working-copies", "prune_knowledge_store_working_copies", Crontab{Minute: "45", Hour: "4"}, 600 * time.Second},
		{"reindex-drifted-workspaces", "reindex_drifted_workspaces", Crontab{Minute: "20"}, 600 * time.Second},
		{"check-knowledge-store-drift", "check_knowledge_store_drift", Crontab{Minute: "15", Hour: "5"}, 600 * time.Second},
		{"sweep-model-compatibility", "sweep_model_compatibility", Crontab{Minute: "0", Hour: "6", DayOfWeek: "0"}, 3600 * time.Second},
		// app/automations/triggers/builtin/schedule/source.py
		{"automation-schedule-select", "automation_schedule_select", Crontab{Minute: "*"}, 50 * time.Second},
		// app/automations/tasks/sweep_overdue_runs_beat.py
		{"automation-runs-sweep-overdue", "automation_runs_sweep_overdue", Crontab{Minute: "*"}, 50 * time.Second},
	}

	entries := make([]Entry, 0, len(written))
	for _, line := range written {
		entries = append(entries, Entry{
			Name:    line.name,
			Task:    line.task,
			Cron:    line.cron,
			Expires: line.expires,
			Queue:   QueueFor(line.task, defaultQueue),
		})
	}
	return entries, nil
}

// CheckerCrontab is parse_schedule_interval in app/celery_app.py, reproduced
// including the two things about it that surprise: it strips the *characters*
// of "min" and "hour" from the right rather than the suffix, and an interval
// with neither unit becomes every minute rather than an error.
func CheckerCrontab(interval string) (Crontab, error) {
	interval = strings.ToLower(strings.TrimSpace(interval))

	switch {
	case strings.HasSuffix(interval, "m") || strings.HasSuffix(interval, "min"):
		count, err := strconv.Atoi(strings.TrimRight(interval, "min"))
		if err != nil {
			return Crontab{}, fmt.Errorf("beat: SCHEDULE_CHECKER_INTERVAL %q: %w", interval, err)
		}
		if count == 1 {
			return Crontab{Minute: "*", Hour: "*"}, nil
		}
		return Crontab{Minute: "*/" + strconv.Itoa(count), Hour: "*"}, nil

	case strings.HasSuffix(interval, "h") || strings.HasSuffix(interval, "hour"):
		count, err := strconv.Atoi(strings.TrimRight(interval, "hour"))
		if err != nil {
			return Crontab{}, fmt.Errorf("beat: SCHEDULE_CHECKER_INTERVAL %q: %w", interval, err)
		}
		if count == 1 {
			return Crontab{Minute: "0", Hour: "*"}, nil
		}
		return Crontab{Minute: "0", Hour: "*/" + strconv.Itoa(count)}, nil

	default:
		return Crontab{Minute: "*", Hour: "*"}, nil
	}
}
