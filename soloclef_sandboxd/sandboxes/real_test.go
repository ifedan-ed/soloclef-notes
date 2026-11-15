package sandboxes

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_sandboxd/docker"
)

// The variables that point these tests at a real Docker daemon and the real
// sandbox image. The daemon may be a shared one: everything the tests make
// carries a stack name of the run's own and is removed at the end.
const (
	realSocketVariable = "SOLOCLEF_TEST_DOCKER_SOCKET"
	realImageVariable  = "SOLOCLEF_TEST_SANDBOX_IMAGE"
)

// The image's facts, as the Compose file gives them to the service.
const (
	realPath = "/opt/python/versions/cpython-3.12-linux-x86_64-gnu/bin:/opt/node/v22.2.0/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	realUser = "1000:1000"
)

func realManager(t *testing.T, stack string) (*Manager, *docker.Client) {
	t.Helper()
	socket, image := os.Getenv(realSocketVariable), os.Getenv(realImageVariable)
	if socket == "" || image == "" {
		t.Skipf("%s and %s are not set; these tests need a Docker daemon and the sandbox image", realSocketVariable, realImageVariable)
	}
	engine := docker.New(socket)
	manager, err := New(engine, stack, Image{Reference: image, Path: realPath, User: realUser}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		left, _ := engine.List(ctx, labelStack+"="+stack)
		for _, container := range left {
			_ = engine.Remove(ctx, container.ID)
		}
	})
	return manager, engine
}

func runStack(t *testing.T) string {
	return fmt.Sprintf("test-sandboxd-%d", time.Now().UnixNano())
}

func threadRequest(key, account string) Request {
	return Request{Kind: Thread, Key: key, Account: account, NanoCPUs: 2e9, MemoryBytes: 1 << 30,
		IdleTTL: 5 * time.Minute, MaxPerAccount: 2}
}

// must runs a command that must succeed and answers its output.
func must(t *testing.T, m *Manager, id, command string) string {
	t.Helper()
	result, err := m.Run(context.Background(), id, command, time.Minute)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("%s: %+v (%v)", command, result, err)
	}
	return result.Output
}

// TestARealSandbox is the whole contract on the real daemon and image:
// created and found again, commands with their exact output, exit codes and
// time limit, files both ways, and removal. It logs each step's time.
func TestARealSandbox(t *testing.T) {
	m, _ := realManager(t, runStack(t))
	ctx := context.Background()

	started := time.Now()
	id, created, err := m.Acquire(ctx, threadRequest("1", "acct-a"))
	if err != nil || !created {
		t.Fatalf("creating: %v (created %v)", err, created)
	}
	t.Logf("create: %s", time.Since(started).Round(time.Millisecond))

	started = time.Now()
	again, created, err := m.Acquire(ctx, threadRequest("1", "acct-a"))
	if err != nil || created || again != id {
		t.Fatalf("found %s (created %v, %v), want %s", again, created, err, id)
	}
	t.Logf("find: %s", time.Since(started).Round(time.Microsecond))

	// The first exec in a fresh container pays for the daemon setting up
	// exec at all; the steady cost is what each tool call pays.
	must(t, m, id, "true")
	const rounds = 20
	started = time.Now()
	for range rounds {
		must(t, m, id, "true")
	}
	t.Logf("command round trip: %s (mean of %d)", (time.Since(started) / rounds).Round(time.Microsecond), rounds)

	if out := must(t, m, id, `printf 'a\n\nb  \nno-newline'`); out != "a\n\nb  \nno-newline" {
		t.Errorf("output %q is not exactly what was printed", out)
	}
	if out := must(t, m, id, "echo err >&2"); out != "err\n" {
		t.Errorf("standard error %q", out)
	}
	if out := must(t, m, id, "pwd; python3 -c 'import sys, docx, pptx, openpyxl, weasyprint; print(sys.version_info[:2])'"); out != "/workspace\n(3, 12)\n" {
		t.Errorf("the working directory and interpreter: %q", out)
	}
	if result, err := m.Run(ctx, id, "exit 3", time.Minute); err != nil || result.ExitCode != 3 || result.TimedOut {
		t.Errorf("exit 3 = %+v (%v)", result, err)
	}

	data := make([]byte, 5_000_000)
	_, _ = rand.Read(data)
	started = time.Now()
	if err := m.WriteFile(ctx, id, "/workspace/deep/dir/file.bin", bytes.NewReader(data)); err != nil {
		t.Fatalf("writing: %v", err)
	}
	t.Logf("5 MB in: %s", time.Since(started).Round(time.Millisecond))
	started = time.Now()
	var got bytes.Buffer
	var announced int64
	if err := m.ReadFile(ctx, id, "/workspace/deep/dir/file.bin", func(size int64) (io.Writer, error) {
		announced = size
		return &got, nil
	}); err != nil || announced != int64(len(data)) || !bytes.Equal(got.Bytes(), data) {
		t.Fatalf("read back %d bytes, announced %d (%v)", got.Len(), announced, err)
	}
	t.Logf("5 MB out: %s", time.Since(started).Round(time.Millisecond))
	small := []byte("hello\x00binary")
	if err := m.WriteFile(ctx, id, "/tmp/small.bin", bytes.NewReader(small)); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	got.Reset()
	if err := m.ReadFile(ctx, id, "/tmp/small.bin", func(int64) (io.Writer, error) { return &got, nil }); err != nil || got.String() != string(small) {
		t.Errorf("a small file from /tmp: %q (%v)", got.String(), err)
	}
	t.Logf("small file read: %s", time.Since(started).Round(time.Microsecond))
	for _, missing := range []string{"/workspace/missing", "/workspace/deep"} {
		if err := m.ReadFile(ctx, id, missing, func(int64) (io.Writer, error) { return io.Discard, nil }); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err = %v, want not found", missing, err)
		}
	}
	tooLarge := errors.New("too large")
	if err := m.ReadFile(ctx, id, "/workspace/deep/dir/file.bin", func(int64) (io.Writer, error) { return nil, tooLarge }); !errors.Is(err, tooLarge) {
		t.Errorf("a refused file: err = %v", err)
	}
	if err := m.WriteFile(ctx, id, "relative/x", strings.NewReader("x")); !errors.Is(err, ErrBadPath) {
		t.Errorf("a relative path: err = %v", err)
	}
	if out := must(t, m, id, "stat -c '%U %a' /workspace/deep/dir/file.bin"); out != "ubuntu 644\n" {
		t.Errorf("a written file's owner and mode: %q", out)
	}

	started = time.Now()
	if err := m.Release(ctx, Thread, "1"); err != nil {
		t.Fatal(err)
	}
	t.Logf("remove: %s", time.Since(started).Round(time.Millisecond))
	if _, err := m.Run(ctx, id, "true", time.Minute); !errors.Is(err, ErrGone) {
		t.Errorf("a command in a removed sandbox: err = %v", err)
	}
}

// TestARealSandboxIsConfined checks every protection from inside: the
// limits, the user, the read-only root, the capabilities, and that the
// network does not exist.
func TestARealSandboxIsConfined(t *testing.T) {
	m, engine := realManager(t, runStack(t))
	ctx := context.Background()
	id, _, err := m.Acquire(ctx, threadRequest("1", "acct-a"))
	if err != nil {
		t.Fatal(err)
	}

	checks := []struct{ command, want string }{
		{"cat /sys/fs/cgroup/memory.max", strconv.Itoa(1 << 30)},
		{"cat /sys/fs/cgroup/memory.swap.max", "0"},
		{"cat /sys/fs/cgroup/cpu.max", "200000 100000"},
		{"cat /sys/fs/cgroup/pids.max", strconv.Itoa(pidsLimit)},
		{"ulimit -n", strconv.Itoa(openFiles)},
		{"id -u; id -g", "1000\n1000"},
		{"grep -E '^(CapEff|CapPrm|NoNewPrivs)' /proc/self/status | tr -s '\\t ' ' '", "CapPrm: 0000000000000000\nCapEff: 0000000000000000\nNoNewPrivs: 1"},
		{"touch /etc/x 2>&1; touch /opt/x 2>&1; true", "touch: cannot touch '/etc/x': Read-only file system\ntouch: cannot touch '/opt/x': Read-only file system"},
		{"ls /sys/class/net", "lo"},
	}
	for _, check := range checks {
		if got := strings.TrimSpace(must(t, m, id, check.command)); got != check.want {
			t.Errorf("%s:\n got %q\nwant %q", check.command, got, check.want)
		}
	}

	// A model's code cannot end its own sandbox: the keeper is root's.
	// `kill -9 -1` kills every process the user may signal, the command's
	// own shell among them, so its exit status is not asked.
	if _, err := m.Run(ctx, id, "kill -9 1; pkill -9 sleep; kill -9 -1", time.Minute); err != nil {
		t.Fatal(err)
	}
	if out := must(t, m, id, "echo alive"); out != "alive\n" {
		t.Errorf("after trying to kill the keeper: %q", out)
	}

	// Memory is a hard limit: a command that takes more than the sandbox
	// has is killed, and the sandbox carries on.
	result, err := m.Run(ctx, id, `python3 -c 'b = bytearray(3 << 30); print("allocated")'`, time.Minute)
	if err != nil || result.ExitCode == 0 || strings.Contains(result.Output, "allocated") {
		t.Errorf("a 3 GiB allocation in a 1 GiB sandbox: %+v (%v)", result, err)
	}
	// Files live in the same memory, held to their share of it: a file
	// that fills /workspace and one that fills /tmp are refused with "No
	// space left", and the sandbox still has the memory to run the next
	// command and remove them.
	result, err = m.Run(ctx, id, `head -c 2147483648 /dev/zero > /workspace/big; head -c 2147483648 /dev/zero > /tmp/big; ls -l /workspace/big /tmp/big`, time.Minute)
	if err != nil || strings.Count(result.Output, "No space left on device") != 2 ||
		!strings.Contains(result.Output, strconv.Itoa(1<<29)+" ") || !strings.Contains(result.Output, strconv.Itoa(1<<28)+" ") {
		t.Errorf("filling /workspace and /tmp in a 1 GiB sandbox: %+v (%v)", result, err)
	}
	must(t, m, id, "rm /workspace/big /tmp/big")
	if out := must(t, m, id, "echo still here"); out != "still here\n" {
		t.Errorf("after the memory limit: %q", out)
	}

	// No network: the stack's database by name, the cloud's metadata
	// address, the private ranges, the internet, and a service that is
	// really listening on Docker's bridge right now.
	listener := startListener(t, engine)
	probe := `
import socket, sys
target = sys.argv[1]
try:
    if ":" in target:
        host, port = target.rsplit(":", 1)
        socket.create_connection((host, int(port)), timeout=3).close()
    else:
        socket.getaddrinfo(target, 5432)
    print("REACHED")
except OSError as e:
    print("blocked:", type(e).__name__)
`
	if err := m.WriteFile(ctx, id, "/tmp/probe.py", strings.NewReader(probe)); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"postgres", "169.254.169.254:80", "10.0.0.1:5432", "172.17.0.1:2375", "1.1.1.1:443", "example.com", listener} {
		out := must(t, m, id, "python3 /tmp/probe.py "+target)
		if !strings.HasPrefix(out, "blocked:") {
			t.Errorf("the sandbox reached %s: %q", target, out)
		}
	}
}

// startListener starts a container on Docker's default bridge listening on
// a port, proves another container on the bridge can reach it (so a sandbox
// failing to is the sandbox's confinement, not a dead listener), and
// answers its address.
func startListener(t *testing.T, engine *docker.Client) string {
	t.Helper()
	ctx := context.Background()
	stack := "test-listener-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	start := func(name string, command ...string) string {
		id, err := engine.Create(ctx, name, docker.ContainerSpec{
			Image: os.Getenv(realImageVariable), Entrypoint: command, Cmd: []string{},
			Env: []string{"PATH=" + realPath}, Labels: map[string]string{labelStack: stack},
			HostConfig: docker.HostConfig{NetworkMode: "bridge", Memory: 256 << 20, MemorySwap: 256 << 20, PidsLimit: 64,
				LogConfig: docker.LogConfig{Type: "none"}, RestartPolicy: docker.RestartPolicy{Name: "no"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = engine.Remove(context.Background(), id) })
		if err := engine.Start(ctx, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	listener := start(stack+"-listener", "python3", "-m", "http.server", "5432")
	client := start(stack+"-client", "sleep", "infinity")
	var address bytes.Buffer
	if _, err := engine.Exec(ctx, listener, docker.ExecOptions{Cmd: []string{"hostname", "-I"}, Stdout: &address, Stderr: io.Discard}); err != nil {
		t.Fatal(err)
	}
	// The bridge may give an IPv6 address too; the IPv4 one is the
	// private range the stack's own services are on.
	var ip string
	for _, field := range strings.Fields(address.String()) {
		if strings.Count(field, ".") == 3 {
			ip = field
			break
		}
	}
	if ip == "" {
		t.Fatalf("the listener has no IPv4 address: %q", address.String())
	}
	target := ip + ":5432"
	reach := `import socket, sys, time
for _ in range(50):
    try:
        socket.create_connection((sys.argv[1], 5432), timeout=1).close(); print("REACHED"); break
    except OSError:
        time.sleep(0.1)`
	var out bytes.Buffer
	if _, err := engine.Exec(ctx, client, docker.ExecOptions{Cmd: []string{"python3", "-c", reach, ip}, Stdout: &out, Stderr: &out}); err != nil || !strings.Contains(out.String(), "REACHED") {
		t.Fatalf("the control container could not reach the listener at %s: %q (%v)", target, out.String(), err)
	}
	return target
}

// TestARealCommandIsStoppedWithEverythingItStarted holds a command to its
// limit and to its caller: at the limit, or when the caller goes away, the
// command and the processes it started in the background are gone.
func TestARealCommandIsStoppedWithEverythingItStarted(t *testing.T) {
	m, _ := realManager(t, runStack(t))
	ctx := context.Background()
	id, _, err := m.Acquire(ctx, threadRequest("1", "acct-a"))
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	result, err := m.Run(ctx, id, "sleep 301 & sleep 302", 2*time.Second)
	if err != nil || !result.TimedOut || result.ExitCode != -1 {
		t.Fatalf("a command past its limit = %+v (%v)", result, err)
	}
	if took := time.Since(started); took > 4*time.Second {
		t.Errorf("the limit of 2s took %s to apply", took)
	}
	if out := must(t, m, id, "pgrep -f 'sleep 30[12]' || echo none"); out != "none\n" {
		t.Errorf("left running after the limit: %q", out)
	}

	cancelled, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if _, err := m.Run(cancelled, id, "sleep 303 & sleep 304", time.Minute); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a command whose caller left: err = %v", err)
	}
	// The stop is a signal sent as the call ends; the processes take a
	// moment to go.
	if out := must(t, m, id, "for i in $(seq 50); do pgrep -f 'sleep 30[34]' >/dev/null || break; sleep 0.1; done; pgrep -f 'sleep 30[34]' || echo none"); out != "none\n" {
		t.Errorf("left running after the caller left: %q", out)
	}
}

// TestRealSandboxesAreCountedExpiredAndAdopted is the registry on the real
// daemon: the account's limit, another account's thread, removal after the idle
// time to live, a restarted service taking its sandboxes back, and another
// stack never seeing them.
func TestRealSandboxesAreCountedExpiredAndAdopted(t *testing.T) {
	stack := runStack(t)
	m, engine := realManager(t, stack)
	ctx := context.Background()
	limited := threadRequest("1", "acct-b")
	limited.MaxPerAccount = 1
	first, _, err := m.Acquire(ctx, limited)
	if err != nil {
		t.Fatal(err)
	}
	limited.Key = "2"
	if _, _, err := m.Acquire(ctx, limited); !errors.Is(err, ErrAccountFull) {
		t.Fatalf("a second thread over the limit: err = %v", err)
	}
	// Another account's thread, with the shortest time to live.
	brief := threadRequest("7", "acct-c")
	brief.IdleTTL = time.Minute
	short, _, err := m.Acquire(ctx, brief)
	if err != nil {
		t.Fatalf("another account's thread: %v", err)
	}

	// A restarted service: a new manager on the same stack adopts both.
	restarted, _ := realManager(t, stack)
	if err := restarted.Adopt(ctx); err != nil || restarted.Count() != 2 {
		t.Fatalf("adopted %d (%v), want 2", restarted.Count(), err)
	}
	if id, created, err := restarted.Acquire(ctx, threadRequest("1", "acct-b")); err != nil || created || id != first {
		t.Fatalf("the restarted service found %s (created %v, %v), want %s", id, created, err, first)
	}
	// Another stack sees none of them, and its thread 1 is its own.
	other, _ := realManager(t, stack+"-other")
	if err := other.Adopt(ctx); err != nil || other.Count() != 0 {
		t.Fatalf("another stack adopted %d (%v)", other.Count(), err)
	}
	if id, _, err := other.Acquire(ctx, threadRequest("1", "acct-b")); err != nil || id == first {
		t.Fatalf("another stack's thread 1: %s (%v)", id, err)
	}

	// The time to live passes for the brief thread (one minute) but not the
	// first (five): the sweep removes only the brief one's container.
	clock := time.Now().Add(90 * time.Second)
	restarted.now = func() time.Time { return clock }
	restarted.Sweep(ctx)
	left, err := engine.List(ctx, labelStack+"="+stack)
	if err != nil || len(left) != 1 || left[0].ID != first {
		t.Fatalf("after the brief thread's time to live, left %+v (%v)", left, err)
	}
	if _, err := restarted.Run(ctx, short, "true", time.Minute); !errors.Is(err, ErrGone) {
		t.Errorf("a command in an expired sandbox: err = %v", err)
	}
}
