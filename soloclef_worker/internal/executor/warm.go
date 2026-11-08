package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"sync"
	"time"
)

// handoverTimeout bounds giving a warm process its next job. A process that is
// leased is idle and blocked reading its stdin, so a write that does not
// finish means a wedged process rather than a busy one — and a job that was
// never handed over is a message that is still work.
const handoverTimeout = 10 * time.Second

// warmChild is one long-lived task process: this app's imports paid once, then
// job after job over a pipe. It is the leader of a process group of its own,
// exactly as a one-shot process is, because cancelling a job still means
// signalling a group — an ffmpeg the body started has to die with it. That is
// also why a process whose group was signalled is retired rather than given
// more work.
type warmChild struct {
	cmd   *exec.Cmd
	log   *slog.Logger
	stdin *os.File
	logs  taskLogs

	ready    chan struct{}
	finished chan struct{}
	waitErr  error
	stopOnce sync.Once

	mu       sync.Mutex
	active   *leasedJob
	jobs     int
	retiring string
}

// startWarmChild starts a process and waits for nothing: a process that is
// still importing is a process this worker holds but cannot use, which is what
// awaitReady is for.
func startWarmChild(cfg Config, log *slog.Logger) (*warmChild, error) {
	cmd := exec.Command(cfg.Command[0], slices.Clone(cfg.Command[1:])...)
	cmd.Dir = cfg.Dir
	cmd.Env = cfg.Env
	ownProcessGroup(cmd)

	// Pipes of this worker's own, as the one-shot shape uses: an io.Writer
	// would have os/exec copying on goroutines that Wait waits for, so a
	// grandchild holding one open would stop Wait from ever returning.
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("executor: stdin for a warm task process: %w", err)
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		closeAll(stdinRead, stdinWrite)
		return nil, fmt.Errorf("executor: stdout for a warm task process: %w", err)
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		closeAll(stdinRead, stdinWrite, stdoutRead, stdoutWrite)
		return nil, fmt.Errorf("executor: stderr for a warm task process: %w", err)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinRead, stdoutWrite, stderrWrite

	if err := cmd.Start(); err != nil {
		closeAll(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		return nil, fmt.Errorf("executor: starting a warm task process: %w", err)
	}
	// The ends the child holds are the child's now; copies here would keep
	// this worker from ever seeing the end of its output.
	closeAll(stdinRead, stdoutWrite, stderrWrite)

	w := &warmChild{
		cmd:      cmd,
		log:      log.With("task_process", cmd.Process.Pid),
		stdin:    stdinWrite,
		ready:    make(chan struct{}),
		finished: make(chan struct{}),
	}

	var reading sync.WaitGroup
	reading.Add(2)
	go func() { defer reading.Done(); w.readResponses(stdoutRead) }()
	go func() { defer reading.Done(); w.logs.stream(stderrRead, w.log) }()

	go func() {
		w.waitErr = cmd.Wait()
		drained := make(chan struct{})
		go func() { reading.Wait(); close(drained) }()
		select {
		case <-drained:
		case <-time.After(drainGrace):
			w.log.Warn("stopped reading a finished task process's output; something is still holding it open")
		}
		closeAll(stdoutRead, stderrRead)
		// After the drain, so a report written just before the process died is
		// still the job's answer.
		w.abandonActive()
		close(w.finished)
	}()

	return w, nil
}

// awaitReady waits for the process to say it has imported this app. A process
// that dies first, or an entrypoint that is not the warm server at all, never
// says it, and the wait ends rather than the worker pretending it has a
// process to work with.
func (w *warmChild) awaitReady(ctx context.Context) error {
	select {
	case <-w.ready:
		return nil
	case <-w.finished:
		return fmt.Errorf("executor: a warm task process ended before it was ready: %v: %s",
			w.waitErr, w.logs.text())
	case <-ctx.Done():
		return fmt.Errorf("executor: a warm task process never said it was ready (is the command the warm server?): %w", ctx.Err())
	}
}

func (w *warmChild) pid() int {
	if w.cmd.Process == nil {
		return 0
	}
	return w.cmd.Process.Pid
}

// take makes this process the one running a job, and clears the log tail so
// the job's own lines are what a lost-worker row carries.
func (w *warmChild) take(id string, log *slog.Logger) *leasedJob {
	w.logs.reset()
	w.mu.Lock()
	defer w.mu.Unlock()
	job := &leasedJob{child: w, id: id, log: log, finished: make(chan struct{})}
	w.active = job
	w.jobs++
	return job
}

// hand gives the process its next job. The write has a deadline because an
// idle process is blocked reading: one that will not take a line is wedged,
// and saying so lets the message go back as work nothing has run.
func (w *warmChild) hand(task string, call Call) error {
	line, err := encodeRequest(task, call)
	if err != nil {
		return fmt.Errorf("executor: encoding the call for %s: %w", task, err)
	}
	if err := w.stdin.SetWriteDeadline(time.Now().Add(handoverTimeout)); err != nil {
		return fmt.Errorf("executor: timing the handover of %s: %w", task, err)
	}
	defer func() { _ = w.stdin.SetWriteDeadline(time.Time{}) }()
	if _, err := w.stdin.Write(line); err != nil {
		return fmt.Errorf("executor: giving %s to a warm task process: %w", task, err)
	}
	return nil
}

// readResponses is the process's half of the protocol: one ready line, then
// one report line per job.
func (w *warmChild) readResponses(stdout *os.File) {
	lines := bufio.NewScanner(stdout)
	lines.Buffer(make([]byte, 0, 4096), reportLimit)
	for lines.Scan() {
		raw := bytes.TrimSpace(lines.Bytes())
		if len(raw) == 0 {
			continue
		}
		var line response
		if err := json.Unmarshal(raw, &line); err != nil {
			// Something wrote to descriptor 1 before the process pointed it at
			// its stderr. The logs are where it belongs.
			w.log.Warn("a task process wrote something that is not part of the warm protocol", "line", string(raw))
			continue
		}
		switch {
		case line.Ready:
			w.becameReady()
		case line.ID != "" && len(line.Report) > 0:
			w.deliver(line.ID, bytes.Clone(line.Report))
		default:
			w.log.Warn("a task process wrote a protocol line with nothing in it", "line", string(raw))
		}
	}
	if err := lines.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		w.log.Warn("stopped reading a task process's reports", "error", err)
	}
}

func (w *warmChild) becameReady() {
	select {
	case <-w.ready:
	default:
		close(w.ready)
	}
}

// deliver gives a report to the job waiting for it. A report for any other job
// is a process out of step with its work: it is retired rather than trusted,
// because the next report it writes would be attributed to the wrong task.
func (w *warmChild) deliver(id string, report []byte) {
	w.mu.Lock()
	active := w.active
	w.mu.Unlock()
	if active == nil || active.id != id {
		w.log.Error("a task process reported a job nothing was waiting for", "reported", id)
		w.markRetiring("it reported a job nothing was waiting for")
		return
	}
	active.finish(report)
}

// abandonActive ends the job of a process that died running it. The job then
// concludes from how the process ended, which is the WorkerLostError row
// Celery writes for a child that disappeared.
func (w *warmChild) abandonActive() {
	w.mu.Lock()
	active := w.active
	w.mu.Unlock()
	if active != nil {
		active.finish(nil)
	}
}

// endJob gives the process back its idleness, so the next report it writes
// with no job waiting is recognised as one.
func (w *warmChild) endJob(job *leasedJob) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.active == job {
		w.active = nil
	}
}

// cancel is a job being stopped: SIGTERM to the group now, SIGKILL once the
// grace is up, exactly as a one-shot process is stopped. A process whose group
// has been signalled can never run another body, so it is retiring from here
// on.
func (w *warmChild) cancel(grace time.Duration, why string) {
	w.markRetiring(why)
	w.stopOnce.Do(func() { go w.stopGroup(grace) })
}

// dismiss is a process being retired between jobs, which can be asked
// politely: the end of its stdin ends its loop, its own shutdown hook flushes
// what it holds, and it exits. A process that does not take the hint is
// stopped the hard way.
func (w *warmChild) dismiss(grace time.Duration, why string) {
	w.markRetiring(why)
	go func() {
		_ = w.stdin.Close()
		if w.ended(grace) {
			return
		}
		w.log.Warn("a retired task process did not end on its own; stopping its group", "reason", why)
		w.cancel(grace, why)
	}()
}

func (w *warmChild) stopGroup(grace time.Duration) {
	pid := w.pid()
	if err := askGroupToStop(pid); err != nil {
		w.log.Warn("could not ask the task's process group to stop", "pid", pid, "error", err)
	}
	if w.ended(grace) {
		return
	}
	// Either the body ignores SIGTERM or something it started does. The group
	// is signalled, not the process: the leader may already be gone while an
	// encoder it spawned is still running under its group.
	w.log.Warn("the task's process group ignored SIGTERM; killing it", "pid", pid, "grace", grace)
	if err := killGroup(pid); err != nil {
		w.log.Error("could not kill the task's process group", "pid", pid, "error", err)
	}
}

func (w *warmChild) ended(within time.Duration) bool {
	timer := time.NewTimer(within)
	defer timer.Stop()
	select {
	case <-w.finished:
		return true
	case <-timer.C:
		return false
	}
}

func (w *warmChild) markRetiring(why string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.retiring == "" {
		w.retiring = why
	}
}

// usable reports whether this process can still be given work: one that is
// retiring, or that has already gone, cannot.
func (w *warmChild) usable() (string, bool) {
	w.mu.Lock()
	retiring := w.retiring
	w.mu.Unlock()
	if retiring != "" {
		return retiring, false
	}
	select {
	case <-w.finished:
		return "it has ended", false
	default:
		return "", true
	}
}

func (w *warmChild) jobsRun() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.jobs
}

func (w *warmChild) exitOutcome() outcome {
	return exitOutcomeOf(w.cmd.ProcessState, w.waitErr)
}

// leasedJob is one body running in a warm process: the report it is waiting
// for, and the process it hands back or retires when it is done.
type leasedJob struct {
	child *warmChild
	pool  *pool
	id    string
	log   *slog.Logger

	finished chan struct{}
	once     sync.Once

	mu     sync.Mutex
	report []byte
}

func (j *leasedJob) pid() int { return j.child.pid() }

func (j *leasedJob) done() <-chan struct{} { return j.finished }

func (j *leasedJob) stop(grace time.Duration) {
	j.child.cancel(grace, "a job it was running had to be stopped")
}

func (j *leasedJob) conclude(fallback *outcome) outcome {
	return concludeFrom(j.reported(), j.child.logs.text(), j.child.exitOutcome, fallback)
}

// release ends this job's hold on the process. The pool decides whether it
// goes back warm or is replaced.
func (j *leasedJob) release() {
	j.child.endJob(j)
	if j.pool != nil {
		j.pool.put(j.child)
	}
}

// finish is the end of the wait: the report the process wrote, or nothing at
// all when the process died holding the job.
func (j *leasedJob) finish(report []byte) {
	j.once.Do(func() {
		j.mu.Lock()
		j.report = report
		j.mu.Unlock()
		close(j.finished)
	})
}

func (j *leasedJob) reported() []byte {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.report
}
