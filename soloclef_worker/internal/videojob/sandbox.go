package videojob

import (
	"context"
	"io"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"
)

// Sandbox is one attempt's sandbox: where the scenes are built and
// rendered, and the stills and the finished video are checked. Nothing a
// model wrote runs anywhere else.
type Sandbox interface {
	RunWithin(ctx context.Context, command string, limit time.Duration) (sandbox.Result, error)
	ReadFile(ctx context.Context, path string, limit int64) ([]byte, error)
	WriteFile(ctx context.Context, path string, data []byte) error
	OpenFile(ctx context.Context, path string) (io.ReadCloser, error)
}

// Sandboxes finds and removes attempts' sandboxes.
type Sandboxes interface {
	ForJob(ctx context.Context, userID, name string) (Sandbox, error)
	TerminateJob(ctx context.Context, name string) error
}

// SandboxClient is Sandboxes over the studio's client.
type SandboxClient struct {
	Client *sandbox.Client
}

func (c SandboxClient) ForJob(ctx context.Context, userID, name string) (Sandbox, error) {
	return c.Client.ForJob(ctx, userID, name)
}

func (c SandboxClient) TerminateJob(ctx context.Context, name string) error {
	return c.Client.TerminateJob(ctx, name)
}

// quote makes text one word of a POSIX shell command.
func quote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}
