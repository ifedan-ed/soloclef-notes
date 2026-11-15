package sandboxes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_sandboxd/docker"
)

// Files move in and out through exec, as a command's standard input and
// output. Docker's archive endpoints (what `docker cp` uses) cannot be used:
// they refuse a read-only root filesystem and do not see tmpfs mounts, and
// every place a sandbox can write is a tmpfs on a read-only root (spec.go).

// ErrNotFound is a path that is not a readable regular file in the sandbox.
var ErrNotFound = errors.New("the file does not exist")

// ErrBadPath is a path that is not absolute and clean.
var ErrBadPath = errors.New("a sandbox path must be absolute and clean, like /workspace/report.pdf")

// notFoundCode is what the read script exits with for a path that is not a
// readable regular file: a code no tool here uses, so it cannot be
// mistaken for cat's own failure.
const notFoundCode = 44

// readScript prints the file's size on a line of its own, then the file.
// One exec instead of a stat and a cat: the size is known before the first
// byte, so the answer can carry Content-Length and a caller can refuse a
// file too large without receiving it.
const readScript = `[ -f "$1" ] && [ -r "$1" ] || exit 44; stat -L -c %s -- "$1" && exec cat -- "$1"`

// writeScript makes the file's directories and writes standard input to it.
const writeScript = `mkdir -p -- "$(dirname -- "$1")" && cat > "$1"`

func checkPath(name string) error {
	if !path.IsAbs(name) || path.Clean(name) != name || strings.ContainsRune(name, 0) {
		return ErrBadPath
	}
	return nil
}

// ReadFile streams a file out of the sandbox. open is called once the file's
// size is known and returns where its bytes go; it may refuse (a file too
// large), and that error is returned. A path that is not a readable regular
// file is ErrNotFound, and open is not called.
func (m *Manager) ReadFile(ctx context.Context, id, name string, open func(size int64) (io.Writer, error)) error {
	if err := checkPath(name); err != nil {
		return err
	}
	entry, done, err := m.use(id)
	if err != nil {
		return err
	}
	defer done()
	stdout := &sizedStream{open: open}
	stderr := &cappedBuffer{max: 4 << 10}
	// The caller's ctx bounds the whole transfer: a large file takes as long
	// as the reader takes to receive it.
	code, err := m.engine.Exec(ctx, id, docker.ExecOptions{
		Cmd:  []string{"sh", "-c", readScript, "sh", name},
		User: m.image.User, Stdout: stdout, Stderr: stderr,
	})
	switch {
	case stdout.refused != nil:
		return stdout.refused
	case err != nil:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return m.lost(entry, err)
	case code == notFoundCode:
		return ErrNotFound
	case code != 0:
		return fmt.Errorf("reading %s: %w", name, errorText(code, stderr))
	case !stdout.opened:
		return fmt.Errorf("reading %s: the file's size never came", name)
	case stdout.written != stdout.size:
		return fmt.Errorf("reading %s: %d of %d bytes came (it changed while being read)", name, stdout.written, stdout.size)
	}
	return nil
}

// sizedStream reads the size line readScript prints, calls open with it, and
// passes the rest on.
type sizedStream struct {
	open    func(int64) (io.Writer, error)
	header  []byte
	opened  bool
	size    int64
	written int64
	target  io.Writer
	refused error
}

func (s *sizedStream) Write(p []byte) (int, error) {
	whole := len(p)
	if !s.opened {
		line, rest, found := bytes.Cut(p, []byte("\n"))
		s.header = append(s.header, line...)
		if !found {
			if len(s.header) > 32 {
				return 0, errors.New("the size line is too long")
			}
			return whole, nil
		}
		size, err := strconv.ParseInt(string(s.header), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("the size line %q is not a number", s.header)
		}
		target, err := s.open(size)
		if err != nil {
			// Ending the stream here closes the connection, and cat dies
			// on its next write: nothing more of the file is sent.
			s.refused = err
			return 0, err
		}
		s.opened, s.size, s.target, p = true, size, target, rest
	}
	n, err := s.target.Write(p)
	s.written += int64(n)
	if err != nil {
		return 0, err
	}
	return whole, nil
}

// WriteFile writes data into the sandbox at name, creating its directories,
// as the sandbox user.
func (m *Manager) WriteFile(ctx context.Context, id, name string, data io.Reader) error {
	if err := checkPath(name); err != nil {
		return err
	}
	entry, done, err := m.use(id)
	if err != nil {
		return err
	}
	defer done()
	stderr := &cappedBuffer{max: 4 << 10}
	code, err := m.engine.Exec(ctx, id, docker.ExecOptions{
		Cmd:  []string{"sh", "-c", writeScript, "sh", name},
		User: m.image.User, Stdin: data, Stdout: stderr, Stderr: stderr,
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return m.lost(entry, err)
	}
	if code != 0 {
		return fmt.Errorf("writing %s: %w", name, errorText(code, stderr))
	}
	return nil
}
