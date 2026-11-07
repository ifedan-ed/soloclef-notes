package broker

import (
	"errors"
	"testing"
	"time"
)

// The default that matters most: a build that was never told which queues to
// take must take none, so running this worker on a developer's machine against
// the live Redis cannot steal a user's job.
func TestDefaultConfigConsumesNothing(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.Queues) != 0 {
		t.Fatalf("DefaultConfig consumes %q; it must consume nothing", cfg.Queues)
	}
	if err := cfg.Validate(); !errors.Is(err, ErrNoQueues) {
		t.Fatalf("Validate() = %v, want ErrNoQueues", err)
	}
}

func TestLoadConfigWithNoEnvironmentConsumesNothing(t *testing.T) {
	cfg, err := LoadConfig(func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Queues) != 0 {
		t.Fatalf("an empty environment configured queues %q", cfg.Queues)
	}
}

func TestLoadConfigReadsQueuesAndLimits(t *testing.T) {
	env := map[string]string{
		"SOLOCLEF_WORKER_QUEUES":            " surfsense.heavy , surfsense ",
		"SOLOCLEF_WORKER_QUEUE_CONCURRENCY": "surfsense.heavy=6,surfsense=16",
		"SOLOCLEF_WORKER_TASK_CONCURRENCY":  "podcast.render_audio=2",
		"SOLOCLEF_WORKER_VISIBILITY_WINDOW": "9h",
	}
	cfg, err := LoadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Queues) != 2 || cfg.Queues[0] != "surfsense.heavy" || cfg.Queues[1] != "surfsense" {
		t.Errorf("Queues = %q", cfg.Queues)
	}
	if cfg.QueueConcurrency["surfsense"] != 16 || cfg.QueueConcurrency["surfsense.heavy"] != 6 {
		t.Errorf("QueueConcurrency = %v", cfg.QueueConcurrency)
	}
	if cfg.TaskConcurrency["podcast.render_audio"] != 2 {
		t.Errorf("TaskConcurrency = %v", cfg.TaskConcurrency)
	}
	if cfg.VisibilityWindow != 9*time.Hour {
		t.Errorf("VisibilityWindow = %v", cfg.VisibilityWindow)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// Nothing is guessed: a queue this worker is told to consume must also be told
// how much of it may run at once.
func TestValidateRejectsAQueueWithNoConcurrencyLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Queues = []string{"surfsense.heavy"}
	if err := cfg.Validate(); !errors.Is(err, ErrNoQueueLimit) {
		t.Fatalf("Validate() = %v, want ErrNoQueueLimit", err)
	}
}

// A visibility window shorter than the one Celery's own workers use would let
// this worker restore a message a live Python worker is still running, because
// a Python worker never refreshes its claim.
func TestValidateRejectsAWindowShorterThanCelerysOwn(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Queues = []string{"surfsense"}
	cfg.QueueConcurrency = map[string]int{"surfsense": 4}
	cfg.VisibilityWindow = KombuVisibilityWindow - time.Second
	if err := cfg.Validate(); !errors.Is(err, ErrVisibilityWindowTooShort) {
		t.Fatalf("Validate() = %v, want ErrVisibilityWindowTooShort", err)
	}
}

func TestDefaultVisibilityWindowOutlivesTheLongestTask(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.VisibilityWindow <= CeleryHardTimeLimit {
		t.Errorf("VisibilityWindow %v does not outlive the 8h task_time_limit", cfg.VisibilityWindow)
	}
	if cfg.RenewInterval >= KombuVisibilityWindow/2 {
		t.Errorf("RenewInterval %v is too slow to keep a claim fresh for a Python worker", cfg.RenewInterval)
	}
}

// BRPOP's timeout is expressed in whole seconds, so a shorter block would be
// silently rounded up and the setting would be a lie.
func TestValidateRejectsAPopTimeoutRedisCannotExpress(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Queues = []string{"surfsense"}
	cfg.QueueConcurrency = map[string]int{"surfsense": 4}
	cfg.PopTimeout = 50 * time.Millisecond
	if err := cfg.Validate(); !errors.Is(err, ErrPopTimeoutTooShort) {
		t.Fatalf("Validate() = %v, want ErrPopTimeoutTooShort", err)
	}
}

func TestLoadConfigRefusesAnUnreadableLimit(t *testing.T) {
	env := map[string]string{
		"SOLOCLEF_WORKER_QUEUES":            "surfsense",
		"SOLOCLEF_WORKER_QUEUE_CONCURRENCY": "surfsense=plenty",
	}
	if _, err := LoadConfig(func(k string) string { return env[k] }); err == nil {
		t.Fatal("LoadConfig accepted a non-numeric limit")
	}
}

// A queue limit below the sum of the task classes routed to it cannot hold
// them: one class filling the queue starves the others, so the limits as
// written are not the limits that apply.
func TestValidateRejectsAQueueTooSmallForTheClassesItCarries(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Queues = []string{"surfsense.heavy"}
	cfg.QueueConcurrency = map[string]int{"surfsense.heavy": 3}
	cfg.TaskConcurrency = map[string]int{
		"podcast.render_audio":        2,
		"generate_video_presentation": 2,
	}
	cfg.TaskQueue = map[string]string{
		"podcast.render_audio":        "surfsense.heavy",
		"generate_video_presentation": "surfsense.heavy",
	}
	if err := cfg.Validate(); !errors.Is(err, ErrQueueLimitBelowClassSum) {
		t.Fatalf("Validate() = %v, want ErrQueueLimitBelowClassSum", err)
	}

	cfg.QueueConcurrency["surfsense.heavy"] = 4
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a queue sized for its classes was refused: %v", err)
	}
}

// A class routed to a queue this worker does not consume says the routing and
// the queue list disagree, which is a mistake rather than a limit.
func TestValidateRejectsAClassRoutedToAQueueThisWorkerDoesNotTake(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Queues = []string{"surfsense"}
	cfg.QueueConcurrency = map[string]int{"surfsense": 8}
	cfg.TaskConcurrency = map[string]int{"podcast.render_audio": 2}
	cfg.TaskQueue = map[string]string{"podcast.render_audio": "surfsense.heavy"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("a class routed outside the consumed queues was accepted")
	}
}

func TestLoadConfigReadsWhichQueueCarriesAClass(t *testing.T) {
	env := map[string]string{
		"SOLOCLEF_WORKER_QUEUES":            "surfsense.heavy",
		"SOLOCLEF_WORKER_QUEUE_CONCURRENCY": "surfsense.heavy=6",
		"SOLOCLEF_WORKER_TASK_CONCURRENCY":  "podcast.render_audio=2",
		"SOLOCLEF_WORKER_TASK_QUEUE":        "podcast.render_audio=surfsense.heavy",
	}
	cfg, err := LoadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.TaskQueue["podcast.render_audio"] != "surfsense.heavy" {
		t.Errorf("TaskQueue = %v", cfg.TaskQueue)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}
