package beat

import (
	"testing"
	"time"
)

// parse_schedule_interval in app/celery_app.py, quirks included: it strips the
// characters of "min"/"hour" from the right rather than a suffix, and falls
// back to every minute for anything with neither unit.
func TestCheckerIntervalMirrorsThePythonParser(t *testing.T) {
	cases := []struct {
		interval string
		want     Crontab
	}{
		{"1m", Crontab{Minute: "*", Hour: "*"}},
		{"2m", Crontab{Minute: "*/2", Hour: "*"}},
		{"5min", Crontab{Minute: "*/5", Hour: "*"}},
		{"15m", Crontab{Minute: "*/15", Hour: "*"}},
		{"1h", Crontab{Minute: "0", Hour: "*"}},
		{"2h", Crontab{Minute: "0", Hour: "*/2"}},
		{"1hour", Crontab{Minute: "0", Hour: "*"}},
		{"", Crontab{Minute: "*", Hour: "*"}},
		{"nonsense", Crontab{Minute: "*", Hour: "*"}},
		{" 10M ", Crontab{Minute: "*/10", Hour: "*"}},
	}
	for _, tc := range cases {
		got, err := CheckerCrontab(tc.interval)
		if err != nil {
			t.Fatalf("CheckerCrontab(%q): %v", tc.interval, err)
		}
		if got != tc.want {
			t.Errorf("CheckerCrontab(%q) = %+v, want %+v", tc.interval, got, tc.want)
		}
	}
}

func TestCheckerIntervalRefusesANumberPythonWouldRaiseOn(t *testing.T) {
	if _, err := CheckerCrontab("xm"); err == nil {
		t.Fatal("expected an error: int('x') raises in the Python parser too")
	}
}

// The table is the schedule, so its shape is pinned: every entry Celery Beat
// has, each with the task, cadence, expiry and queue Celery would give it.
func TestTheScheduleIsEveryEntryCeleryBeatHas(t *testing.T) {
	entries, err := SoloclefSchedule("surfsense", "2m")
	if err != nil {
		t.Fatalf("SoloclefSchedule: %v", err)
	}
	byName := map[string]Entry{}
	for _, entry := range entries {
		if _, duplicate := byName[entry.Name]; duplicate {
			t.Fatalf("two entries named %q", entry.Name)
		}
		byName[entry.Name] = entry
	}
	if len(byName) != 17 {
		t.Errorf("the schedule has %d entries, want the 17 in app/celery_app.py", len(byName))
	}

	want := map[string]struct {
		task    string
		expires time.Duration
	}{
		"check-periodic-connector-schedules":   {"check_periodic_schedules", 30 * time.Second},
		"push-receipts":                        {"push_receipts", 600 * time.Second},
		"cleanup-stale-indexing-notifications": {"cleanup_stale_indexing_notifications", 60 * time.Second},
		"billing-roll-periods":                 {"billing.roll_periods", 3000 * time.Second},
		"billing-release-stale-holds":          {"billing.release_stale_holds", 800 * time.Second},
		"billing-reconcile-payments":           {"billing.reconcile_payments", 500 * time.Second},
		"billing-check-ledger":                 {"billing.check_ledger", 3000 * time.Second},
		"reconcile-queued-deliverables":        {"deliverables.reconcile_queued", 55 * time.Second},
		"purge-refresh-tokens":                 {"purge_refresh_tokens", 600 * time.Second},
		"evict-etl-cache":                      {"evict_etl_cache", 600 * time.Second},
		"evict-embedding-cache":                {"evict_embedding_cache", 600 * time.Second},
		"prune-knowledge-store-working-copies": {"prune_knowledge_store_working_copies", 600 * time.Second},
		"reindex-drifted-workspaces":           {"reindex_drifted_workspaces", 600 * time.Second},
		"check-knowledge-store-drift":          {"check_knowledge_store_drift", 600 * time.Second},
		"sweep-model-compatibility":            {"sweep_model_compatibility", 3600 * time.Second},
		"automation-schedule-select":           {"automation_schedule_select", 50 * time.Second},
		"automation-runs-sweep-overdue":        {"automation_runs_sweep_overdue", 50 * time.Second},
	}
	for name, expected := range want {
		entry, present := byName[name]
		if !present {
			t.Errorf("the schedule has no %q", name)
			continue
		}
		if entry.Task != expected.task {
			t.Errorf("%s runs %q, want %q", name, entry.Task, expected.task)
		}
		if entry.Expires != expected.expires {
			t.Errorf("%s expires after %v, want %v", name, entry.Expires, expected.expires)
		}
		if entry.Queue != "surfsense" {
			t.Errorf("%s goes to %q; every beat task is on the default queue", name, entry.Queue)
		}
	}
}

// The checker's cadence is the one thing in the schedule that is configured,
// so it has to come through rather than be baked in.
func TestTheCheckerEntryTakesItsCadenceFromTheInterval(t *testing.T) {
	entries, err := SoloclefSchedule("surfsense", "15m")
	if err != nil {
		t.Fatalf("SoloclefSchedule: %v", err)
	}
	for _, entry := range entries {
		if entry.Name != "check-periodic-connector-schedules" {
			continue
		}
		cron := compile(t, entry.Cron)
		if !cron.Matches(at(t, "2026-11-08T13:15:00Z")) || cron.Matches(at(t, "2026-11-08T13:16:00Z")) {
			t.Errorf("the checker's cadence is %s, not every 15 minutes", cron)
		}
		return
	}
	t.Fatal("the checker entry is missing")
}

// A beat task routed to another queue by task_routes must be published there,
// or the consumer that was given that queue never sees it.
func TestTheQueueComesFromTaskRoutes(t *testing.T) {
	cases := map[string]string{
		"check_periodic_schedules":      "surfsense",
		"index_google_drive_files":      "surfsense.connectors",
		"reindex_knowledge_store":       "surfsense.connectors",
		"podcast.render_audio":          "surfsense.heavy",
		"generate_video_presentation":   "surfsense.heavy",
		"deliverables.execute_queued":   "surfsense.heavy",
		"deliverables.reconcile_queued": "surfsense",
	}
	for task, want := range cases {
		if got := QueueFor(task, "surfsense"); got != want {
			t.Errorf("QueueFor(%q) = %q, want %q", task, got, want)
		}
	}
}

// Every entry compiles, or the beat refuses to start rather than silently
// skipping one.
func TestTheScheduleCompiles(t *testing.T) {
	entries, err := SoloclefSchedule("surfsense", "2m")
	if err != nil {
		t.Fatalf("SoloclefSchedule: %v", err)
	}
	for _, entry := range entries {
		if _, err := entry.Cron.Compile(); err != nil {
			t.Errorf("%s: %v", entry.Name, err)
		}
		if entry.Expires <= 0 {
			t.Errorf("%s has no expiry; every entry in app/celery_app.py sets one", entry.Name)
		}
	}
}
