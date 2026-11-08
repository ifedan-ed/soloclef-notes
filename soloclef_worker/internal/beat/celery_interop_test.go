package beat

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// These are the tests that decide whether this scheduler may replace Celery
// Beat: they read the schedule out of app/celery_app.py itself and compare a
// published tick, byte for byte, with one Celery's own client published.
//
// They need a container with this app installed and a URL for the test Redis
// as that container sees it. Neither is defaulted, so this never runs against
// the live broker by accident.
const (
	EnvCeleryContainer  = "SOLOCLEF_WORKER_TEST_CELERY_CONTAINER"
	EnvTestRedisInThere = "SOLOCLEF_WORKER_TEST_REDIS_FROM_CELERY"
)

// dumpBeatSchedule reads the schedule Celery Beat would run, out of the app
// that defines it, so a change to beat_schedule shows up here rather than as
// a scheduled task that quietly stopped running.
const dumpBeatSchedule = `
import json, os
from app.celery_app import celery_app
from app.config import config

out = {}
for name, entry in celery_app.conf.beat_schedule.items():
    schedule = entry["schedule"]
    options = entry.get("options") or {}
    task = entry["task"]
    routed = (celery_app.conf.task_routes or {}).get(task) or {}
    out[name] = {
        "task": task,
        "minute": str(schedule._orig_minute),
        "hour": str(schedule._orig_hour),
        "day_of_week": str(schedule._orig_day_of_week),
        "day_of_month": str(schedule._orig_day_of_month),
        "month_of_year": str(schedule._orig_month_of_year),
        "expires": options.get("expires"),
        "queue": options.get("queue") or routed.get("queue") or celery_app.conf.task_default_queue,
    }
print("SCHEDULE " + json.dumps({
    "default_queue": celery_app.conf.task_default_queue,
    "checker_interval": config.SCHEDULE_CHECKER_INTERVAL,
    "enable_utc": bool(celery_app.conf.enable_utc),
    "timezone": str(celery_app.conf.timezone),
    "beat_max_loop_interval": celery_app.conf.beat_max_loop_interval,
    "entries": out,
}))
`

// celeryBeatPublish is what Celery Beat does with one entry: send the task
// with the entry's own options, through the same producer settings
// app/celery_app.py configures. Importing the app itself would pull in the
// live broker, which these tests must not touch.
const celeryBeatPublish = `
import os, time
from celery import Celery
from celery.signals import before_task_publish

@before_task_publish.connect
def _stamp_enqueue_time(headers=None, **_kwargs):
    if headers is not None:
        headers["surfsense.enqueued_at_ns"] = str(time.monotonic_ns())

QUEUE = "probe.beat"
app = Celery("surfsense", broker=os.environ["BROKER_URL"])
assert app.conf.broker_url == os.environ["BROKER_URL"], (
    "broker_url resolved to %r, not the test broker" % (app.conf.broker_url,))
app.conf.update(
    task_serializer="json", accept_content=["json"], result_serializer="json",
    timezone="UTC", enable_utc=True,
    task_default_queue=QUEUE, task_default_exchange=QUEUE,
    task_default_routing_key=QUEUE,
    task_routes={"probe.beat.tick": {"queue": QUEUE}},
)
app.send_task("probe.beat.tick", expires=50)
print("sent")
`

func runPython(t *testing.T, script string) string {
	t.Helper()
	container := os.Getenv(EnvCeleryContainer)
	brokerURL := os.Getenv(EnvTestRedisInThere)
	if container == "" || brokerURL == "" {
		t.Skipf("set %s and %s to run the Celery interop tests", EnvCeleryContainer, EnvTestRedisInThere)
	}
	// Celery reads CELERY_BROKER_URL in preference to anything its
	// constructor is given, and in this container it names the live broker.
	cmd := exec.Command("docker", "exec", "-i",
		"-e", "BROKER_URL="+brokerURL,
		"-e", "CELERY_BROKER_URL="+brokerURL,
		"-e", "CELERY_RESULT_BACKEND="+brokerURL,
		container, "python", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python in %s failed: %v\n%s", container, err, out)
	}
	return string(out)
}

type pythonEntry struct {
	Task        string `json:"task"`
	Minute      string `json:"minute"`
	Hour        string `json:"hour"`
	DayOfWeek   string `json:"day_of_week"`
	DayOfMonth  string `json:"day_of_month"`
	MonthOfYear string `json:"month_of_year"`
	Expires     *int   `json:"expires"`
	Queue       string `json:"queue"`
}

type pythonSchedule struct {
	DefaultQueue        string                 `json:"default_queue"`
	CheckerInterval     string                 `json:"checker_interval"`
	EnableUTC           bool                   `json:"enable_utc"`
	Timezone            string                 `json:"timezone"`
	BeatMaxLoopInterval int                    `json:"beat_max_loop_interval"`
	Entries             map[string]pythonEntry `json:"entries"`
}

// The schedule this package runs has to be the schedule app/celery_app.py
// defines: every entry, the same cadence, the same expiry, the same queue.
func TestTheScheduleIsTheOneCeleryBeatWouldRun(t *testing.T) {
	out := runPython(t, dumpBeatSchedule)
	_, encoded, found := strings.Cut(out, "SCHEDULE ")
	if !found {
		t.Fatalf("the dump printed nothing usable:\n%s", out)
	}
	var python pythonSchedule
	if err := json.Unmarshal([]byte(strings.TrimSpace(encoded)), &python); err != nil {
		t.Fatalf("the dump is not JSON: %v\n%s", err, encoded)
	}

	// The schedule is read in UTC here because Celery reads it in UTC there.
	if !python.EnableUTC || python.Timezone != "UTC" {
		t.Errorf("Celery reads the schedule in %s (enable_utc %v); this package assumes UTC",
			python.Timezone, python.EnableUTC)
	}

	entries, err := SoloclefSchedule(python.DefaultQueue, python.CheckerInterval)
	if err != nil {
		t.Fatalf("SoloclefSchedule: %v", err)
	}
	mine := map[string]Entry{}
	for _, entry := range entries {
		mine[entry.Name] = entry
	}

	for name, theirs := range python.Entries {
		ours, present := mine[name]
		if !present {
			t.Errorf("Celery Beat runs %q and this scheduler does not", name)
			continue
		}
		if ours.Task != theirs.Task {
			t.Errorf("%s: task %q here, %q there", name, ours.Task, theirs.Task)
		}
		if theirs.Expires == nil {
			t.Errorf("%s: Celery sets no expiry; this scheduler requires one", name)
		} else if want := time.Duration(*theirs.Expires) * time.Second; ours.Expires != want {
			t.Errorf("%s: expires %v here, %v there", name, ours.Expires, want)
		}
		if ours.Queue != theirs.Queue {
			t.Errorf("%s: queue %q here, %q there", name, ours.Queue, theirs.Queue)
		}
		for _, field := range []struct {
			name         string
			ours, theirs string
		}{
			{"minute", ours.Cron.Minute, theirs.Minute},
			{"hour", ours.Cron.Hour, theirs.Hour},
			{"day_of_week", ours.Cron.DayOfWeek, theirs.DayOfWeek},
			{"day_of_month", ours.Cron.DayOfMonth, theirs.DayOfMonth},
			{"month_of_year", ours.Cron.MonthOfYear, theirs.MonthOfYear},
		} {
			if orStar(field.ours) != orStar(field.theirs) {
				t.Errorf("%s: %s is %q here, %q there",
					name, field.name, orStar(field.ours), orStar(field.theirs))
			}
		}
	}
	for name := range mine {
		if _, present := python.Entries[name]; !present {
			t.Errorf("this scheduler runs %q and Celery Beat does not", name)
		}
	}
}

// Celery Beat's own loop interval is the sanity check on this one: a poll
// slower than Celery's would make this scheduler later than the one it
// replaces.
func TestThePollIsNoSlowerThanCeleryBeatsLoop(t *testing.T) {
	out := runPython(t, dumpBeatSchedule)
	_, encoded, _ := strings.Cut(out, "SCHEDULE ")
	var python pythonSchedule
	if err := json.Unmarshal([]byte(strings.TrimSpace(encoded)), &python); err != nil {
		t.Fatalf("the dump is not JSON: %v", err)
	}
	celery := time.Duration(python.BeatMaxLoopInterval) * time.Second
	if mine := DefaultConfig().PollInterval; mine > celery {
		t.Errorf("this scheduler polls every %v; Celery Beat's loop is %v", mine, celery)
	}
}

// The byte comparison. A tick is consumed by whichever worker has the queue,
// which during a cutover may be a Python one, so the envelope has to be the
// one Celery's own client writes — not merely one that parses.
func TestATickIsByteForByteWhatCeleryPublishes(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)

	if out := runPython(t, celeryBeatPublish); !strings.Contains(out, "sent") {
		t.Fatalf("Celery did not publish: %s", out)
	}
	theirs, err := rdb.RPop(ctx, probeQueue).Result()
	if err != nil {
		t.Fatalf("Celery's own tick is not on the queue: %v", err)
	}

	clock := &testClock{at: time.Date(2026, 11, 8, 13, 47, 0, 0, time.UTC)}
	b := newBeat(t, rdb, probeConfig(clock), "beat-a")
	if err := b.tick(ctx, clock.now()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	clock.advance(time.Minute)
	if err := b.tick(ctx, clock.now()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	ours, err := rdb.RPop(ctx, probeQueue).Result()
	if err != nil {
		t.Fatalf("this scheduler's tick is not on the queue: %v", err)
	}

	if blanked(t, ours) != blanked(t, theirs) {
		t.Errorf("this scheduler wrote\n%s\nCelery wrote\n%s", blanked(t, ours), blanked(t, theirs))
	}
}

// blanked replaces the values that are different every time a task is
// published — the ids, the publisher and the two timestamps — so that what is
// compared is the spelling of the envelope rather than its contents. The
// replacement is textual, so the key order and the spacing survive it: those
// are the parts a byte comparison is for.
func blanked(t *testing.T, envelope string) string {
	t.Helper()
	var decoded struct {
		Headers struct {
			ID         string `json:"id"`
			Expires    string `json:"expires"`
			Origin     string `json:"origin"`
			EnqueuedAt string `json:"surfsense.enqueued_at_ns"`
		} `json:"headers"`
		Properties struct {
			ReplyTo     string `json:"reply_to"`
			DeliveryTag string `json:"delivery_tag"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(envelope), &decoded); err != nil {
		t.Fatalf("not an envelope: %v\n%s", err, envelope)
	}
	volatile := map[string]string{
		decoded.Headers.ID:             "<id>",
		decoded.Headers.Expires:        "<expires>",
		decoded.Headers.Origin:         "<origin>",
		decoded.Headers.EnqueuedAt:     "<enqueued-at>",
		decoded.Properties.ReplyTo:     "<reply-to>",
		decoded.Properties.DeliveryTag: "<delivery-tag>",
	}
	for value, placeholder := range volatile {
		if value == "" {
			t.Fatalf("the envelope has no %s:\n%s", placeholder, envelope)
		}
		envelope = strings.ReplaceAll(envelope, value, placeholder)
	}
	// The expiry is a timestamp in both, and the format is part of the
	// contract: a Celery worker parses this header with its own parser.
	if _, err := time.Parse("2006-01-02T15:04:05.999999-07:00", decoded.Headers.Expires); err != nil {
		t.Fatalf("expires %q is not the format Python's isoformat writes: %v", decoded.Headers.Expires, err)
	}
	return fmt.Sprint(envelope)
}
