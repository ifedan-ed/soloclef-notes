package docker

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// ExecOptions is one process to run in a running container.
type ExecOptions struct {
	Cmd        []string
	User       string
	WorkingDir string
	// Stdin, when not nil, is copied to the process's standard input, which
	// is then closed: how a file's bytes go into a sandbox.
	Stdin io.Reader
	// Stdout and Stderr receive the process's two streams as they arrive.
	// Both must be set; they may be the same writer.
	Stdout, Stderr io.Writer
}

// Exec runs a process in a running container and waits for it, returning
// its exit status. Its output is written to the options' writers as it
// arrives. A container that is gone or not running is IsNotFound or
// IsConflict.
//
// When ctx ends first, the connection is closed and ctx's error returned:
// the daemon has no call that kills an exec, so stopping the process itself
// is the caller's to arrange (sandboxes/commands.go does it by a marker in
// the command line).
func (c *Client) Exec(ctx context.Context, container string, options ExecOptions) (int, error) {
	if options.Stdout == nil || options.Stderr == nil {
		return 0, errors.New("docker: exec needs both output writers")
	}
	var created struct {
		ID string `json:"Id"`
	}
	err := c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/exec", nil, map[string]any{
		"AttachStdin":  options.Stdin != nil,
		"AttachStdout": true,
		"AttachStderr": true,
		"Tty":          false,
		"Cmd":          options.Cmd,
		"User":         options.User,
		"WorkingDir":   options.WorkingDir,
	}, &created)
	if err != nil {
		return 0, err
	}
	if err := c.attach(ctx, created.ID, options); err != nil {
		return 0, err
	}
	return c.exitCode(ctx, created.ID)
}

// attach starts the exec on a connection of its own and copies its streams
// until the process ends.
//
// Starting an exec "hijacks" the connection: after the HTTP answer the same
// socket carries the process's standard input one way and its output the
// other, which net/http's client cannot express. So the request is written
// by hand on a raw connection, the answer's header is read with net/http's
// parser, and the rest of the connection is the stream.
func (c *Client) attach(ctx context.Context, id string, options ExecOptions) error {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return fmt.Errorf("docker: the daemon could not be reached on %s: %w", c.socket, err)
	}
	defer conn.Close()
	// The daemon cannot be told to stop an exec, but closing its connection
	// at least stops this side waiting on it.
	stopWatching := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopWatching()

	body := []byte(`{"Detach":false,"Tty":false}`)
	request := fmt.Sprintf("POST /%s/exec/%s/start HTTP/1.1\r\nHost: docker\r\nContent-Type: application/json\r\n"+
		"Connection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: %d\r\n\r\n", apiVersion, url.PathEscape(id), len(body))
	if _, err := conn.Write(append([]byte(request), body...)); err != nil {
		return c.cut(ctx, err)
	}
	reader := bufio.NewReaderSize(conn, 64<<10)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		return c.cut(ctx, err)
	}
	// 101 is the upgrade granted; a daemon that ignores the upgrade header
	// answers 200 and streams the same frames on the same connection.
	if response.StatusCode != http.StatusSwitchingProtocols && response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return readError(response)
	}

	copied := make(chan error, 1)
	if options.Stdin != nil {
		go func() {
			_, err := io.Copy(conn, options.Stdin)
			// Closing our half is the process's end of input; the output
			// half stays open until the process exits.
			if unix, ok := conn.(*net.UnixConn); ok {
				if closeErr := unix.CloseWrite(); err == nil {
					err = closeErr
				}
			}
			copied <- err
		}()
	} else {
		copied <- nil
	}
	if err := demultiplex(reader, options.Stdout, options.Stderr); err != nil {
		return c.cut(ctx, err)
	}
	if err := <-copied; err != nil {
		return c.cut(ctx, fmt.Errorf("docker: sending standard input: %w", err))
	}
	return nil
}

// cut reports a broken stream as ctx's error when ctx ended it.
func (c *Client) cut(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("docker: exec stream: %w", err)
}

// exitCode asks the daemon how the exec ended. The stream closes when the
// process's output does, which is a moment before the daemon records the
// exit, so a still-running answer is asked again shortly.
func (c *Client) exitCode(ctx context.Context, id string) (int, error) {
	for wait := time.Millisecond; ; wait *= 2 {
		var state struct {
			Running  bool `json:"Running"`
			ExitCode int  `json:"ExitCode"`
		}
		if err := c.call(ctx, http.MethodGet, "/exec/"+url.PathEscape(id)+"/json", nil, nil, &state); err != nil {
			return 0, err
		}
		if !state.Running {
			return state.ExitCode, nil
		}
		if wait > time.Second {
			return 0, errors.New("docker: the exec's output ended but the daemon still says it is running")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// demultiplex splits an exec's stream into its two outputs. Without a
// terminal the daemon frames each write: one byte naming the stream
// (1 standard output, 2 standard error), three zero bytes, the payload's
// length as four big-endian bytes, then the payload.
func demultiplex(stream io.Reader, stdout, stderr io.Writer) error {
	var header [8]byte
	for {
		if _, err := io.ReadFull(stream, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		size := int64(header[4])<<24 | int64(header[5])<<16 | int64(header[6])<<8 | int64(header[7])
		var target io.Writer
		switch header[0] {
		case 1:
			target = stdout
		case 2:
			target = stderr
		case 3:
			// The daemon's own error about the exec, sent in-band.
			message, _ := io.ReadAll(io.LimitReader(stream, size))
			return fmt.Errorf("docker: %s", bytes.TrimSpace(message))
		default:
			return fmt.Errorf("docker: unknown stream %d in exec output", header[0])
		}
		if _, err := io.CopyN(target, stream, size); err != nil {
			return err
		}
	}
}
