package sandboxes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_sandboxd/docker"
)

// MaxOutputBytes bounds what one command's output may take in this service
// and on the wire. A command that prints more has the rest dropped (and
// still drained, so it never blocks on a full pipe), and its result says so;
// the tools above cut far shorter for the model.
const MaxOutputBytes = 1 << 20

// killGrace is how long a command has, after the polite signal at its time
// limit, before it and everything it started are killed outright.
const killGrace = 5 * time.Second

// Result is how a command ended.
type Result struct {
	// Output is what the command printed, standard output and standard
	// error together, exactly as written and in the order they arrived.
	Output string `json:"output"`
	// ExitCode is the command's exit status; -1 when it was stopped at its
	// time limit.
	ExitCode  int  `json:"exitCode"`
	TimedOut  bool `json:"timedOut"`
	Truncated bool `json:"truncated"`
}

// Run runs a shell command in the sandbox, in /workspace, as the sandbox
// user, and waits for it. A command that exits non-zero is a Result, not an
// error: its output is what the caller wants. An error is the sandbox
// failing, not the command.
//
// The command is held to limit by coreutils' timeout, which puts it and
// everything it starts in a process group of their own and, at the limit,
// signals the whole group (TERM, then KILL after killGrace): a LibreOffice
// the command started does not outlive it. If ctx ends first (the caller
// hung up: a chat turn stopped, an automation's run cancelled) the same group is
// signalled at once, found by a random marker in its command line.
func (m *Manager) Run(ctx context.Context, id, command string, limit time.Duration) (Result, error) {
	if limit <= 0 {
		return Result{}, fmt.Errorf("%w: a command needs a time limit", ErrBadRequest)
	}
	entry, done, err := m.use(id)
	if err != nil {
		return Result{}, err
	}
	defer done()

	marker, err := newMarker()
	if err != nil {
		return Result{}, err
	}
	output := &cappedBuffer{max: MaxOutputBytes}
	started := m.now()
	code, err := m.engine.Exec(ctx, id, docker.ExecOptions{
		// bash, not sh: a model writes bash. The marker is bash's $0,
		// which is in the command line of timeout and of bash, and of
		// nothing else.
		Cmd:  []string{"timeout", "--kill-after=" + seconds(killGrace), seconds(limit), "bash", "-c", command, marker},
		User: m.image.User, WorkingDir: "/workspace",
		Stdout: output, Stderr: output,
	})
	if err != nil {
		if ctx.Err() != nil {
			m.stop(entry, marker)
			return Result{}, ctx.Err()
		}
		return Result{}, m.lost(entry, err)
	}
	result := Result{Output: output.String(), ExitCode: code, Truncated: output.truncated}
	// timeout answers 124 when the command ended at the polite signal and
	// 137 when it had to be killed. A command can exit 124 by itself, so the
	// clock decides.
	if (code == 124 || code == 137) && m.now().Sub(started) >= limit {
		result.TimedOut, result.ExitCode = true, -1
	}
	return result, nil
}

// stop signals a command's process group after its caller went away. timeout
// passes TERM on to the group and kills it after killGrace, as at its limit.
func (m *Manager) stop(entry *sandbox, marker string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := m.engine.Exec(ctx, entry.id, docker.ExecOptions{
		// -f matches the whole command line; the marker is hex, so it is
		// its own pattern, and it ends the line, which only the command's
		// timeout and bash do.
		Cmd:  []string{"pkill", "-TERM", "-f", marker + "$"},
		User: m.image.User, Stdout: discard, Stderr: discard,
	})
	if err != nil {
		m.log.Warn("sandbox: stopping an abandoned command failed; its time limit still stops it", "container", entry.id, "error", err)
	}
}

// discard is a writer for a stream nobody reads.
var discard io.Writer = io.Discard

// newMarker is a command's marker: random, so no command can name another's.
func newMarker() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "soloclef-command-" + hex.EncodeToString(raw[:]), nil
}

// seconds spells a duration for coreutils' timeout, which takes fractions.
func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}

// cappedBuffer keeps the first max bytes written to it and counts the rest
// as dropped. Both of an exec's streams write to one, so it is locked.
type cappedBuffer struct {
	mu        sync.Mutex
	max       int
	data      []byte
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.max - len(b.data)
	if len(p) > room {
		b.data = append(b.data, p[:max(room, 0)]...)
		b.truncated = true
	} else {
		b.data = append(b.data, p...)
	}
	// Always the whole length: a short write would end the stream, and the
	// process must be drained to finish.
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

// errorText is a failed helper's standard error, for its error message.
func errorText(code int, stderr *cappedBuffer) error {
	return fmt.Errorf("exit %d: %s", code, stderr.String())
}
