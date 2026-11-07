package executor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"git.danvics.com/danvics/surfsense/soloclef_worker/internal/celery"
)

// fakeChild writes a shell script that stands in for `python -m app.tasks.run`.
// A script is used rather than a Go helper binary because the hard cases here
// are about signals, process groups and grandchildren, which a shell states
// plainly: `trap ” TERM` is a child that ignores a polite stop.
func fakeChild(t *testing.T, body string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-task-process")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("writing the fake task process: %v", err)
	}
	return []string{path}
}

// recordedRows is the result backend, remembered in order: the order is the
// contract, because a client polling a task must never see STARTED after it
// has seen a terminal state.
type recordedRows struct {
	mu   sync.Mutex
	rows []celery.Result
	err  error
}

func (r *recordedRows) Write(_ context.Context, row celery.Result) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, row)
	return r.err
}

func (r *recordedRows) all() []celery.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]celery.Result(nil), r.rows...)
}

// terminal is the row a client finally reads, and the one every test asserts on.
func (r *recordedRows) terminal(t *testing.T) celery.Result {
	t.Helper()
	rows := r.all()
	if len(rows) == 0 {
		t.Fatal("no result row was written at all")
	}
	last := rows[len(rows)-1]
	if last.State == celery.StateStarted {
		t.Fatalf("the last row is still STARTED; the task never got a terminal row: %#v", rows)
	}
	return last
}

func testConfig(t *testing.T, command []string) Config {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Command = command
	cfg.Worker = "go-worker-test"
	cfg.HardTimeLimit = 20 * time.Second
	cfg.SoftTimeLimit = 0
	cfg.KillGrace = 200 * time.Millisecond
	cfg.StartFailureDelay = 10 * time.Millisecond
	return cfg
}

func testExecutor(t *testing.T, cfg Config) (*Executor, *recordedRows) {
	t.Helper()
	rows := &recordedRows{}
	exec, err := New(cfg, rows)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return exec, rows
}

// probeMessage is a decoded delivery with the fields the entrypoint is given.
func probeMessage(t *testing.T) *celery.Message {
	t.Helper()
	return &celery.Message{
		Task:        "probe.exec",
		ID:          "11111111-2222-3333-4444-555555555555",
		RootID:      "11111111-2222-3333-4444-555555555555",
		Retries:     2,
		Queue:       "probe.exec.queue",
		DeliveryTag: "tag-1",
		Args:        []any{1.0, "two"},
		Kwargs:      map[string]any{"k": true},
	}
}

func errorField(t *testing.T, row celery.Result, field string) string {
	t.Helper()
	value, ok := row.Value.(map[string]any)
	if !ok {
		t.Fatalf("the failure row's value is %#v, not an exception shape", row.Value)
	}
	text, _ := value[field].(string)
	return text
}

// waitGone fails unless a pid disappears, which is how a test proves a kill
// reached something this worker is not the parent of.
func waitGone(t *testing.T, pid int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d is still running", pid)
}

// pidFrom reads a pid a fake task process wrote down for the test.
func pidFrom(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no pid was ever written to %s", path)
	return 0
}
