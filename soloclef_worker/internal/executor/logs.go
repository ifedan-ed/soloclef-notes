package executor

import (
	"bufio"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

const (
	// stderrTailLines are kept for the row a process that died without
	// reporting gets, so an OOM kill is readable from AsyncResult.
	stderrTailLines = 20

	// logLineLimit bounds one line of a task's logs.
	logLineLimit = 64 << 10
)

// taskLogs is a task process's own logging: in this worker's logs as it
// happens, and the last lines kept for a process that dies without reporting.
// A warm process clears the tail between jobs, so a lost job's row shows that
// job's lines and not the previous one's.
type taskLogs struct {
	mu   sync.Mutex
	tail []string
}

func (l *taskLogs) stream(stderr io.Reader, log *slog.Logger) {
	lines := bufio.NewScanner(stderr)
	lines.Buffer(make([]byte, 0, 4096), logLineLimit)
	for lines.Scan() {
		line := lines.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		log.Info("task", "line", line)
		l.mu.Lock()
		l.tail = append(l.tail, line)
		if len(l.tail) > stderrTailLines {
			l.tail = l.tail[len(l.tail)-stderrTailLines:]
		}
		l.mu.Unlock()
	}
	if err := lines.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		log.Warn("stopped reading the task process's logs", "error", err)
	}
}

func (l *taskLogs) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.tail, "\n")
}

// reset starts a warm process's next job with a clean tail.
func (l *taskLogs) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tail = nil
}
