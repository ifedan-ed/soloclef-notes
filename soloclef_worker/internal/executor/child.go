package executor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// reportLimit bounds the report read into memory. Celery's own results are
	// small; a process that writes more than this has gone wrong, and reading
	// it all would make the task's fault the worker's.
	reportLimit = 1 << 20

	// stderrTailLines are kept for the row a process that died without
	// reporting gets, so an OOM kill is readable from AsyncResult.
	stderrTailLines = 20

	// logLineLimit bounds one line of a child's logs.
	logLineLimit = 64 << 10

	// drainGrace bounds the wait for a dead process's output: something that
	// escaped the process group could hold the pipe open indefinitely.
	drainGrace = 2 * time.Second
)

// child is one task process: in a process group of its own, its call on stdin,
// its logs in this worker's logs, and its stdout kept for the one line it
// reports.
type child struct {
	cmd  *exec.Cmd
	log  *slog.Logger
	done chan struct{}

	mu     sync.Mutex
	stdout []byte
	tail   []string

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

	c := &child{cmd: cmd, log: log, done: make(chan struct{})}

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
	go func() { defer reading.Done(); c.streamLogs(stderrRead) }()

	go func() {
		defer close(c.done)
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
	case <-c.done:
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

// conclude decides what happened. The body's own report wins whenever there is
// one: a task that caught a soft limit and returned a value succeeded, here as
// in Celery. fallback is what the worker decided -- a limit or a shutdown --
// and stands in when the process never said anything usable.
func (c *child) conclude(fallback *outcome) outcome {
	report, err := parseReport(c.report())
	switch {
	case err == nil:
		return report.outcome()
	case fallback != nil:
		return fallback.withLogs(c.logTail())
	case !errors.Is(err, errNoReport):
		return workerFailure(excUnreadable, err).withLogs(c.logTail())
	default:
		return c.exitOutcome().withLogs(c.logTail())
	}
}

// exitOutcome reads the failure out of how the process ended.
func (c *child) exitOutcome() outcome {
	if number, name, killed := terminationSignal(c.cmd.ProcessState); killed {
		// Celery's own wording for a prefork child that disappeared, and what
		// an OOM kill looks like: nothing of the body's own ran, so nothing
		// released whatever it was holding.
		return workerFailure(excWorkerLost,
			fmt.Errorf("worker exited prematurely: signal %d (%s)", number, name))
	}
	code := c.cmd.ProcessState.ExitCode()
	if code == 0 {
		return workerFailure(excUnreadable, errNoReport)
	}
	return workerFailure(excWorkerLost,
		fmt.Errorf("worker exited prematurely: exitcode %d: %v", code, c.waitErr))
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

// streamLogs puts the task's own logging in this worker's logs as it happens,
// and keeps the last lines for a process that dies without reporting.
func (c *child) streamLogs(stderr io.Reader) {
	lines := bufio.NewScanner(stderr)
	lines.Buffer(make([]byte, 0, 4096), logLineLimit)
	for lines.Scan() {
		line := lines.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		c.log.Info("task", "line", line)
		c.mu.Lock()
		c.tail = append(c.tail, line)
		if len(c.tail) > stderrTailLines {
			c.tail = c.tail[len(c.tail)-stderrTailLines:]
		}
		c.mu.Unlock()
	}
	if err := lines.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		c.log.Warn("stopped reading the task process's logs", "error", err)
	}
}

func (c *child) report() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stdout
}

func (c *child) logTail() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.tail, "\n")
}

func closeAll(files ...*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}
