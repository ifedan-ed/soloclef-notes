package metrics

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/beat"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

type fakeConsumer struct {
	snapshot broker.Snapshot
	err      error
}

func (f fakeConsumer) Snapshot(context.Context) (broker.Snapshot, error) {
	return f.snapshot, f.err
}

type fakeBeat struct{ snapshot beat.Snapshot }

func (f fakeBeat) Snapshot() beat.Snapshot { return f.snapshot }

func sampleConsumer() fakeConsumer {
	return fakeConsumer{snapshot: broker.Snapshot{
		QueueDepth:      map[string]int64{"surfsense.heavy": 7},
		InFlightByQueue: map[string]int{"surfsense.heavy": 3},
		InFlightByTask:  map[string]int{"podcast.render_audio": 2},
		HeldClaims:      4,
		Scheduled:       1,
		Wait: map[string]broker.WaitStats{
			"podcast.render_audio": {Count: 2, Total: 3 * time.Second, Max: 2 * time.Second},
		},
		Taken: 11, Deferred: 2, Expired: 1, Unreadable: 1,
		Requeued: 3, Delayed: 5, Restored: 1, WaitUnknown: 2,
	}}
}

func sampleBeat() fakeBeat {
	return fakeBeat{snapshot: beat.Snapshot{
		LeaseHeld: true,
		Entries: map[string]beat.EntrySnapshot{
			"reconcile-queued-deliverables": {
				Published: 5, Fenced: 1, Failed: 0,
				LastTick: time.Unix(1762600020, 0).UTC(),
			},
		},
		CaughtUp: 1,
	}}
}

// scrape runs the real server over a real socket, because the thing being
// tested is what a scrape sees.
func scrape(t *testing.T, collectors ...Collector) string {
	t.Helper()
	server, err := NewServer("127.0.0.1:0", collectors...)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Run: %v", err)
		}
	})

	url := "http://" + server.Address() + MetricsPath
	response, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", url, err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d\n%s", url, response.StatusCode, body)
	}
	return string(body)
}

// The names and labels are a contract with a dashboard, so renaming one has to
// break a test rather than a panel.
func TestAScrapeCarriesTheNamesADashboardWillUse(t *testing.T) {
	scraped := scrape(t, Consumer(sampleConsumer()), Beat(sampleBeat()))

	want := []string{
		`soloclef_worker_queue_depth{queue="surfsense.heavy"} 7`,
		`soloclef_worker_jobs_in_flight{queue="surfsense.heavy"} 3`,
		`soloclef_worker_task_jobs_in_flight{task="podcast.render_audio"} 2`,
		`soloclef_worker_held_claims 4`,
		`soloclef_worker_scheduled_messages 1`,
		`soloclef_worker_queue_wait_seconds_total{task="podcast.render_audio"} 3`,
		`soloclef_worker_queue_wait_measurements_total{task="podcast.render_audio"} 2`,
		`soloclef_worker_queue_wait_seconds_max{task="podcast.render_audio"} 2`,
		`soloclef_worker_messages_taken_total 11`,
		`soloclef_worker_messages_deferred_total 2`,
		`soloclef_worker_messages_expired_total 1`,
		`soloclef_worker_messages_unreadable_total 1`,
		`soloclef_worker_messages_requeued_total 3`,
		`soloclef_worker_messages_delayed_total 5`,
		`soloclef_worker_messages_restored_total 1`,
		`soloclef_worker_queue_wait_unknown_total 2`,
		`soloclef_worker_beat_lease_held 1`,
		`soloclef_worker_beat_ticks_published_total{entry="reconcile-queued-deliverables"} 5`,
		`soloclef_worker_beat_ticks_fenced_total{entry="reconcile-queued-deliverables"} 1`,
		`soloclef_worker_beat_ticks_failed_total{entry="reconcile-queued-deliverables"} 0`,
		`soloclef_worker_beat_last_tick_timestamp_seconds{entry="reconcile-queued-deliverables"} 1.76260002e+09`,
		`soloclef_worker_beat_windows_caught_up_total 1`,
	}
	for _, line := range want {
		if !strings.Contains(scraped, line) {
			t.Errorf("the scrape has no %q\n%s", line, scraped)
		}
	}
}

// Every metric needs its HELP and TYPE, once, before its samples: a scrape
// that declares a name twice is a scrape Prometheus rejects.
func TestEveryMetricIsDeclaredExactlyOnce(t *testing.T) {
	scraped := scrape(t, Consumer(sampleConsumer()), Beat(sampleBeat()))

	declared := map[string]int{}
	typed := map[string]int{}
	named := map[string]bool{}
	for _, line := range strings.Split(scraped, "\n") {
		switch {
		case strings.HasPrefix(line, "# HELP "):
			declared[strings.Fields(line)[2]]++
		case strings.HasPrefix(line, "# TYPE "):
			fields := strings.Fields(line)
			typed[fields[2]]++
			if kind := fields[3]; kind != "gauge" && kind != "counter" {
				t.Errorf("%s is a %q, which this writer does not emit", fields[2], kind)
			}
		case line == "":
		default:
			name, _, _ := strings.Cut(line, "{")
			name, _, _ = strings.Cut(name, " ")
			named[name] = true
		}
	}
	for name := range named {
		if declared[name] != 1 || typed[name] != 1 {
			t.Errorf("%s has %d HELP and %d TYPE lines, want one each", name, declared[name], typed[name])
		}
	}
	if len(named) == 0 {
		t.Fatal("the scrape carried no samples at all")
	}

	// A counter's name ends _total, which is what makes rate() right.
	counters := map[string]bool{}
	for _, line := range strings.Split(scraped, "\n") {
		if strings.HasPrefix(line, "# TYPE ") && strings.HasSuffix(line, " counter") {
			counters[strings.Fields(line)[2]] = true
		}
	}
	names := make([]string, 0, len(counters))
	for name := range counters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !strings.HasSuffix(name, "_total") {
			t.Errorf("the counter %s does not end in _total", name)
		}
	}
}

// A scrape that cannot read the broker must say so rather than serve a page of
// zeroes, which a dashboard would draw as an empty queue.
func TestAScrapeThatCannotReadTheBrokerFails(t *testing.T) {
	server, err := NewServer("127.0.0.1:0", Consumer(fakeConsumer{err: errors.New("redis is gone")}))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		stop()
		<-done
	}()

	response, err := http.Get("http://" + server.Address() + MetricsPath)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusInternalServerError {
		t.Errorf("a failed collection answered %d, want 500", response.StatusCode)
	}
}

// A label value with a quote or a newline in it would otherwise break the
// whole scrape, not just its own line.
func TestLabelValuesAreEscaped(t *testing.T) {
	source := fakeConsumer{snapshot: broker.Snapshot{
		QueueDepth: map[string]int64{`odd"queue` + "\n": 1},
	}}
	scraped := scrape(t, Consumer(source))
	if !strings.Contains(scraped, `soloclef_worker_queue_depth{queue="odd\"queue\n"} 1`) {
		t.Errorf("the label was not escaped:\n%s", scraped)
	}
}

func TestNewServerRefusesNoAddressAndNoCollectors(t *testing.T) {
	if _, err := NewServer("", Consumer(sampleConsumer())); err == nil {
		t.Error("an empty address should be refused rather than guessed at")
	}
	if _, err := NewServer("127.0.0.1:0"); err == nil {
		t.Error("a server with nothing to collect should be refused")
	}
}
