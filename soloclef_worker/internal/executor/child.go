package executor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"sync"
	"time"
)

const (
	// reportLimit bounds the report read into memory. Celery's own results are
	// small; a process that writes more than this has gone wrong, and reading
	// it all would make the task's fault the worker's.
	reportLimit = 1 << 20

	// drainGrace bounds the wait for a dead process's output: something that
	// escaped the process group could hold the pipe open indefinitely.
	drainGrace = 2 * time.Second
)

// child is one task process: in a process group of its own, its call on stdin,
// its logs in this worker's logs, and its stdout kept for the one line it
// reports.
type child struct {
	cmd      *exec.Cmd
	log      *slog.Logger
	finished chan struct{}
	logs     taskLogs

	mu     sync.Mutex
	stdout []byte

	waitErr  error
	stopOnce sync.Once
}

// startChild runs one task body. It returns an error only when the process
// could not be started at all, which is the worker's fault rather than the
// task's.
func startChild(cfg Config, task string, call Call, log *slog.Logger) (*child, error) {
	payload, err := json.Marshal(call)
	if err != nil {
		return nil, fmt.Errorf("executor: encoding the call for %s: %w", task, err)
	}

	argv := append(slices.Clone(cfg.Command), task)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cfg.Dir
	cmd.Env = cfg.Env
	ownProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("executor: stdin for %s: %w", task, err)
	}
	// Pipes of this worker's own rather than io.Writers: os/exec would copy
	// those on goroutines that Wait waits for, so a grandchild holding the
	// write end open would stop Wait from ever returning — exactly the case a
	// hard timeout has to survive.
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("executor: stdout for %s: %w", task, err)
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		closeAll(stdoutRead, stdoutWrite)
		return nil, fmt.Errorf("executor: stderr for %s: %w", task, err)
	}
	cmd.Stdout = stdoutWrite
	cmd.Stderr = stderrWrite

	if err := cmd.Start(); err != nil {
		closeAll(stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		return nil, fmt.Errorf("executor: starting %s: %w", task, err)
	}
	// The write ends belong to the child now; holding copies would keep the
	// reads below from ever seeing the end of its output.
	closeAll(stdoutWrite, stderrWrite)

	c := &child{cmd: cmd, log: log, finished: make(chan struct{})}

	// Written on a goroutine because a body that never reads its call, with a
	// payload past the pipe buffer, would otherwise block here forever.
	go func() {
		defer func() { _ = stdin.Close() }()
		if _, err := stdin.Write(append(payload, '\n')); err != nil {
			log.Warn("the task process did not take its whole call", "error", err)
		}
	}()

	var reading sync.WaitGroup
	reading.Add(2)
	go func() { defer reading.Done(); c.collectReport(stdoutRead) }()
	go func() { defer reading.Done(); c.logs.stream(stderrRead, log) }()

	go func() {
		defer close(c.finished)
		c.waitErr = cmd.Wait()
		drained := make(chan struct{})
		go func() { reading.Wait(); close(drained) }()
		select {
		case <-drained:
		case <-time.After(drainGrace):
			log.Warn("stopped reading a finished task process's output; something is still holding it open")
		}
		closeAll(stdoutRead, stderrRead)
	}()

	return c, nil
}

// done closes once the process has gone and its output has been read.
func (c *child) done() <-chan struct{} { return c.finished }

// release has nothing to give up: the process ran this job and no other.
func (c *child) release() {}

func (c *child) pid() int {
	if c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

// stop asks the process group to end, and makes it end once the grace is up.
// Safe to call more than once; only the first call signals.
func (c *child) stop(grace time.Duration) {
	c.stopOnce.Do(func() { go c.stopGroup(grace) })
}

func (c *child) stopGroup(grace time.Duration) {
	pid := c.pid()
	if err := askGroupToStop(pid); err != nil {
		c.log.Warn("could not ask the task's process group to stop", "pid", pid, "error", err)
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-c.finished:
		return
	case <-timer.C:
	}
	// Either the body ignores SIGTERM or something it started does. The group
	// is signalled, not the process: the leader may already be gone while an
	// encoder it spawned is still running under its group.
	c.log.Warn("the task's process group ignored SIGTERM; killing it", "pid", pid, "grace", grace)
	if err := killGroup(pid); err != nil {
		c.log.Error("could not kill the task's process group", "pid", pid, "error", err)
	}
}

// conclude decides what happened, by the rule both execution shapes share.
func (c *child) conclude(fallback *outcome) outcome {
	return concludeFrom(c.report(), c.logs.text(), c.exitOutcome, fallback)
}

// exitOutcome reads the failure out of how the process ended.
func (c *child) exitOutcome() outcome {
	return exitOutcomeOf(c.cmd.ProcessState, c.waitErr)
}

func (c *child) collectReport(stdout io.Reader) {
	read, err := io.ReadAll(io.LimitReader(stdout, reportLimit))
	if err != nil && !errors.Is(err, os.ErrClosed) {
		c.log.Warn("could not read the task process's report", "error", err)
	}
	c.mu.Lock()
	c.stdout = read
	c.mu.Unlock()
}

func (c *child) report() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stdout
}

func closeAll(files ...*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}
