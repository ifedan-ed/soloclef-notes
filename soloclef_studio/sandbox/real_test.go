package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// The variables that point the tests below at a real sandbox service
// (soloclef_sandboxd). It may run beside a deployment's: these tests use
// thread ids and job names from the clock, and remove what they make.
const (
	realURLVariable = "SOLOCLEF_TEST_SANDBOX_URL"
	realKeyVariable = "SOLOCLEF_TEST_SANDBOX_KEY"
)

// realClient is a client of the real service, or a skip when there is none.
func realClient(tb testing.TB, commandTimeout time.Duration) *Client {
	tb.Helper()
	address := os.Getenv(realURLVariable)
	if address == "" {
		tb.Skipf("%s is not set; this test needs a running sandbox service", realURLVariable)
	}
	client, err := New(Settings{
		URL: address, APIKey: os.Getenv(realKeyVariable),
		CPU: "2", Memory: "2Gi", IdleTTL: 5 * time.Minute, CommandTimeout: commandTimeout, MaxPerAccount: 2,
	}, &http.Client{})
	if err != nil {
		tb.Fatal(err)
	}
	return client
}

// realAccount is an account of the run's own, so no other run's sandboxes
// count against its limit.
func realAccount(run int64) string {
	return fmt.Sprintf("cccccccc-0000-4000-8000-%012d", run%1_000_000_000_000)
}

// TestARealSandbox is the client's contract against the real service and
// image: create, find again, run, the output exactly, a time limit, files
// both ways, no network, and removal. It logs each step's time.
func TestARealSandbox(t *testing.T) {
	client := realClient(t, 2*time.Minute)
	ctx := context.Background()
	thread := time.Now().UnixNano()
	account := realAccount(thread)
	t.Cleanup(func() { _ = client.Terminate(context.Background(), thread) })

	started := time.Now()
	session, err := client.ForThread(ctx, account, thread)
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	t.Logf("create: %s", time.Since(started).Round(time.Millisecond))

	started = time.Now()
	again, err := client.ForThread(ctx, account, thread)
	if err != nil || again.ID != session.ID {
		t.Fatalf("found %v (%v), want %s", again, err, session.ID)
	}
	t.Logf("find: %s", time.Since(started).Round(time.Millisecond))

	started = time.Now()
	result, err := session.Run(ctx, `printf 'a\n\nb  \nno-newline'`)
	if err != nil || !result.OK() || result.Output != "a\n\nb  \nno-newline" {
		t.Errorf("run = %+v (%v)", result, err)
	}
	t.Logf("command round trip: %s", time.Since(started).Round(time.Millisecond))
	if result, err := session.Run(ctx, "echo err >&2"); err != nil || result.Output != "err\n" {
		t.Errorf("standard error = %+v (%v)", result, err)
	}
	if result, err := session.Run(ctx, "exit 3"); err != nil || result.ExitCode != 3 || result.TimedOut {
		t.Errorf("exit 3 = %+v (%v)", result, err)
	}
	// A client whose limit is two seconds finds the same sandbox, and its
	// long command is stopped at that limit.
	short, err := realClient(t, 2*time.Second).ForThread(ctx, account, thread)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := short.Run(ctx, "sleep 20"); err != nil || !result.TimedOut || result.OK() {
		t.Errorf("a command past its limit = %+v (%v)", result, err)
	}

	data := []byte("hello\x00binary")
	if err := session.WriteFile(ctx, "/workspace/deep/dir/file.bin", data); err != nil {
		t.Fatalf("writing: %v", err)
	}
	started = time.Now()
	got, err := session.ReadFile(ctx, "/workspace/deep/dir/file.bin", 1024)
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("read back %q (%v)", got, err)
	}
	t.Logf("file read: %s", time.Since(started).Round(time.Microsecond))
	if _, err := session.ReadFile(ctx, "/workspace/missing", 1024); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing file: err = %v", err)
	}
	if _, err := session.ReadFile(ctx, "/workspace/deep/dir/file.bin", 3); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a file over the limit: err = %v", err)
	}

	// The sandbox has no way out.
	if result, err := session.Run(ctx, "curl -s -m 5 -o /dev/null https://example.com"); err != nil || result.OK() {
		t.Errorf("the sandbox reached the network: %+v (%v)", result, err)
	}

	if err := client.Terminate(ctx, thread); err != nil {
		t.Fatalf("removing: %v", err)
	}
	if _, err := session.Run(ctx, "true"); !errors.Is(err, ErrGone) {
		t.Errorf("a command in a removed sandbox: err = %v", err)
	}
}

// BenchmarkRealCommand is one trivial command's round trip, sandbox found
// first, as a tool call pays for it.
func BenchmarkRealCommand(b *testing.B) {
	client := realClient(b, 2*time.Minute)
	ctx := context.Background()
	thread := time.Now().UnixNano()
	b.Cleanup(func() { _ = client.Terminate(context.Background(), thread) })
	if _, err := client.ForThread(ctx, realAccount(thread), thread); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		session, err := client.ForThread(ctx, realAccount(thread), thread)
		if err != nil {
			b.Fatal(err)
		}
		if result, err := session.Run(ctx, "true"); err != nil || !result.OK() {
			b.Fatal(result, err)
		}
	}
}
