package sandboxes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_sandboxd/docker"
)

// fakeEngine is Docker in memory: containers by id, and exec answered by a
// function of the test's.
type fakeEngine struct {
	mu         sync.Mutex
	containers map[string]*fakeContainer
	next       int
	// createDelay holds every create, so concurrent requests overlap.
	createDelay time.Duration
	createErr   error
	startErr    error
	// exec answers a command line; nil is exit 0 with no output.
	exec    func(cmd []string, options docker.ExecOptions) (int, error)
	execs   [][]string
	removed []string
}

type fakeContainer struct {
	name  string
	spec  docker.ContainerSpec
	state string
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{containers: map[string]*fakeContainer{}}
}

func (f *fakeEngine) Create(_ context.Context, name string, spec docker.ContainerSpec) (string, error) {
	time.Sleep(f.createDelay)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return "", f.createErr
	}
	for _, c := range f.containers {
		if c.name == name {
			return "", &docker.Error{Status: 409, Message: "name in use"}
		}
	}
	f.next++
	id := fmt.Sprintf("container-%d", f.next)
	f.containers[id] = &fakeContainer{name: name, spec: spec, state: "created"}
	return id, nil
}

func (f *fakeEngine) Start(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.containers[id].state = "running"
	return nil
}

func (f *fakeEngine) Remove(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.containers, id)
	f.removed = append(f.removed, id)
	return nil
}

func (f *fakeEngine) List(_ context.Context, labels ...string) ([]docker.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []docker.Container
	for id, c := range f.containers {
		matches := true
		for _, label := range labels {
			key, value, _ := strings.Cut(label, "=")
			matches = matches && c.spec.Labels[key] == value
		}
		if matches {
			found = append(found, docker.Container{ID: id, Labels: c.spec.Labels, State: c.state})
		}
	}
	return found, nil
}

func (f *fakeEngine) Exec(ctx context.Context, id string, options docker.ExecOptions) (int, error) {
	f.mu.Lock()
	c := f.containers[id]
	f.execs = append(f.execs, options.Cmd)
	answer := f.exec
	f.mu.Unlock()
	if c == nil {
		return 0, &docker.Error{Status: 404, Message: "no such container"}
	}
	if c.state != "running" {
		return 0, &docker.Error{Status: 409, Message: "container is not running"}
	}
	if answer == nil {
		return 0, nil
	}
	return answer(options.Cmd, options)
}

func (f *fakeEngine) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.containers)
}

const testPath = "/opt/python/bin:/usr/bin:/bin"

func newTestManager(t *testing.T, engine Engine) *Manager {
	t.Helper()
	m, err := New(engine, "teststack", Image{Reference: "sandbox:local", Path: testPath, User: "1000:1000"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func thread(key, account string, max int) Request {
	return Request{Kind: Thread, Key: key, Account: account, NanoCPUs: 2e9, MemoryBytes: 2 << 30, IdleTTL: 10 * time.Minute, MaxPerAccount: max}
}

func TestNewRefusesWhatCannotBeRight(t *testing.T) {
	engine := newFakeEngine()
	good := Image{Reference: "sandbox:local", Path: testPath, User: "1000:1000"}
	for name, try := range map[string]func() error{
		"a stack that is not a project's name": func() error { _, err := New(engine, "Bad Stack", good, nil); return err },
		"no image": func() error {
			image := good
			image.Reference = ""
			_, err := New(engine, "s", image, nil)
			return err
		},
		"commands as root": func() error {
			image := good
			image.User = "0:0"
			_, err := New(engine, "s", image, nil)
			return err
		},
		"a user by name": func() error {
			image := good
			image.User = "ubuntu"
			_, err := New(engine, "s", image, nil)
			return err
		},
	} {
		if try() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestARequestMustBeWhole(t *testing.T) {
	m := newTestManager(t, newFakeEngine())
	for name, change := range map[string]func(*Request){
		"an unknown kind":           func(r *Request) { r.Kind = "box" },
		"a key with a slash":        func(r *Request) { r.Key = "a/b" },
		"an upper-case key":         func(r *Request) { r.Key = "A" },
		"no account":                func(r *Request) { r.Account = "" },
		"a thread without a limit":  func(r *Request) { r.MaxPerAccount = 0 },
		"no memory":                 func(r *Request) { r.MemoryBytes = 0 },
		"a time to live under 1min": func(r *Request) { r.IdleTTL = 30 * time.Second },
	} {
		request := thread("1", "acct", 2)
		change(&request)
		if _, _, err := m.Acquire(context.Background(), request); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// One thread has one sandbox however many requests for it arrive at once,
// and the account's count includes sandboxes still starting.
func TestConcurrentRequestsForOneThreadMakeOneSandbox(t *testing.T) {
	engine := newFakeEngine()
	engine.createDelay = 20 * time.Millisecond
	m := newTestManager(t, engine)
	var wg sync.WaitGroup
	ids := make([]string, 8)
	created := make([]bool, 8)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			ids[i], created[i], err = m.Acquire(context.Background(), thread("7", "acct", 1))
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	creations := 0
	for i := range ids {
		if ids[i] != ids[0] {
			t.Fatalf("thread 7 got two sandboxes: %v", ids)
		}
		if created[i] {
			creations++
		}
	}
	if creations != 1 || engine.count() != 1 {
		t.Errorf("%d said created, %d containers; want 1 and 1", creations, engine.count())
	}
}

func TestTheAccountsLimitCountsItsThreadsOnly(t *testing.T) {
	engine := newFakeEngine()
	engine.createDelay = 20 * time.Millisecond
	m := newTestManager(t, engine)
	ctx := context.Background()
	// Two threads at once against a limit of one: one is refused, even
	// though neither had finished starting when the other asked.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = m.Acquire(ctx, thread(fmt.Sprint(i+1), "acct", 1))
		}()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) || !(errors.Is(errs[0], ErrAccountFull) || errors.Is(errs[1], ErrAccountFull)) {
		t.Fatalf("two threads against a limit of one: %v", errs)
	}
	// Another account is not held to it, and the old video kind is gone.
	if _, _, err := m.Acquire(ctx, thread("3", "other", 1)); err != nil {
		t.Errorf("another account: %v", err)
	}
	job := Request{Kind: "job", Key: "deliverable-job-1-attempt-1", Account: "acct", NanoCPUs: 1e9, MemoryBytes: 1 << 30, IdleTTL: time.Minute}
	if _, _, err := m.Acquire(ctx, job); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a job sandbox: %v", err)
	}
	// Removing the account's thread frees its slot.
	for _, key := range []string{"1", "2"} {
		if err := m.Release(ctx, Thread, key); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := m.Acquire(ctx, thread("4", "acct", 1)); err != nil {
		t.Errorf("after removal: %v", err)
	}
}

// A sandbox that failed to start holds nothing: not the account's slot,
// not the thread's key, not a container.
func TestASandboxThatFailedToStartLeavesNothing(t *testing.T) {
	engine := newFakeEngine()
	m := newTestManager(t, engine)
	engine.startErr = errors.New("no such image")
	if _, _, err := m.Acquire(context.Background(), thread("1", "acct", 1)); err == nil || !strings.Contains(err.Error(), "no such image") {
		t.Fatalf("err = %v", err)
	}
	if engine.count() != 0 || m.Count() != 0 {
		t.Errorf("left %d containers and %d entries", engine.count(), m.Count())
	}
	engine.startErr = nil
	if _, created, err := m.Acquire(context.Background(), thread("1", "acct", 1)); err != nil || !created {
		t.Errorf("the next try: created %v (%v)", created, err)
	}
}

// The container is the confinement the package promises: no network, a
// read-only root with sized tmpfs mounts, no capabilities, no new
// privileges, an init, hard limits, and labels the service can adopt from.
func TestTheContainerIsConfined(t *testing.T) {
	engine := newFakeEngine()
	m := newTestManager(t, engine)
	id, _, err := m.Acquire(context.Background(), thread("42", "acct", 1))
	if err != nil {
		t.Fatal(err)
	}
	c := engine.containers[id]
	spec, host := c.spec, c.spec.HostConfig
	if c.name != "teststack-sandbox-thread-42" {
		t.Errorf("name %q", c.name)
	}
	if !spec.NetworkDisabled || host.NetworkMode != "none" || !host.ReadonlyRootfs || !host.Init ||
		strings.Join(host.CapDrop, ",") != "ALL" || strings.Join(host.SecurityOpt, ",") != "no-new-privileges:true" {
		t.Errorf("confinement: %+v", host)
	}
	if host.NanoCPUs != 2e9 || host.Memory != 2<<30 || host.MemorySwap != host.Memory || host.PidsLimit != pidsLimit ||
		len(host.Ulimits) != 1 || host.Ulimits[0].Hard != openFiles {
		t.Errorf("limits: %+v", host)
	}
	wantTmpfs := map[string]string{
		"/workspace":    fmt.Sprintf("size=%d,uid=1000,gid=1000,mode=0755", 1<<30),
		"/tmp":          fmt.Sprintf("size=%d,mode=1777", 1<<29),
		"/home/sandbox": fmt.Sprintf("size=%d,uid=1000,gid=1000,mode=0755", homeBytes),
	}
	if fmt.Sprint(host.Tmpfs) != fmt.Sprint(wantTmpfs) {
		t.Errorf("tmpfs %v, want %v", host.Tmpfs, wantTmpfs)
	}
	if strings.Join(spec.Entrypoint, " ") != "sleep infinity" || spec.User != "" || spec.Env[0] != "PATH="+testPath {
		t.Errorf("keeper and environment: %+v", spec)
	}
	want := map[string]string{labelStack: "teststack", labelKind: "thread", labelKey: "42", labelAccount: "acct", labelIdleTTL: "600"}
	if fmt.Sprint(spec.Labels) != fmt.Sprint(want) {
		t.Errorf("labels %v", spec.Labels)
	}
}

func TestACommandIsHeldToItsLimitAsTheSandboxUser(t *testing.T) {
	engine := newFakeEngine()
	m := newTestManager(t, engine)
	clock := time.Now()
	m.now = func() time.Time { return clock }
	id, _, err := m.Acquire(context.Background(), thread("1", "acct", 1))
	if err != nil {
		t.Fatal(err)
	}
	var user, dir string
	engine.exec = func(cmd []string, options docker.ExecOptions) (int, error) {
		user, dir = options.User, options.WorkingDir
		fmt.Fprint(options.Stdout, "out ")
		fmt.Fprint(options.Stderr, "err")
		return 0, nil
	}
	result, err := m.Run(context.Background(), id, "echo hi", 90*time.Second)
	if err != nil || result.Output != "out err" || result.ExitCode != 0 || result.TimedOut {
		t.Fatalf("result %+v (%v)", result, err)
	}
	cmd := engine.execs[len(engine.execs)-1]
	if strings.Join(cmd[:6], " ") != "timeout --kill-after=5.000 90.000 bash -c echo hi" || !strings.HasPrefix(cmd[6], "soloclef-command-") {
		t.Errorf("command line %q", cmd)
	}
	if user != "1000:1000" || dir != "/workspace" {
		t.Errorf("ran as %q in %q", user, dir)
	}

	// Exit 124 is a timeout only when the limit has passed: a command may
	// exit 124 by itself.
	engine.exec = func([]string, docker.ExecOptions) (int, error) { return 124, nil }
	if result, _ := m.Run(context.Background(), id, "exit 124", time.Minute); result.TimedOut || result.ExitCode != 124 {
		t.Errorf("a quick exit 124: %+v", result)
	}
	engine.exec = func([]string, docker.ExecOptions) (int, error) { clock = clock.Add(time.Minute); return 137, nil }
	if result, _ := m.Run(context.Background(), id, "sleep 99", time.Minute); !result.TimedOut || result.ExitCode != -1 {
		t.Errorf("a command killed at its limit: %+v", result)
	}
	if _, err := m.Run(context.Background(), id, "true", 0); !errors.Is(err, ErrBadRequest) {
		t.Errorf("no limit: %v", err)
	}
}

func TestOutputPastTheCapIsDroppedAndSaidToBe(t *testing.T) {
	engine := newFakeEngine()
	m := newTestManager(t, engine)
	id, _, _ := m.Acquire(context.Background(), thread("1", "acct", 1))
	engine.exec = func(_ []string, options docker.ExecOptions) (int, error) {
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for range 20 {
			if n, err := options.Stdout.Write(chunk); n != len(chunk) || err != nil {
				return 0, fmt.Errorf("the writer refused output: %d, %v", n, err)
			}
		}
		return 0, nil
	}
	result, err := m.Run(context.Background(), id, "yes", time.Minute)
	if err != nil || len(result.Output) != MaxOutputBytes || !result.Truncated {
		t.Errorf("kept %d bytes, truncated %v (%v)", len(result.Output), result.Truncated, err)
	}
}

// A caller that goes away stops its command: the service signals the
// command's group, by its marker, as the sandbox user.
func TestACommandWhoseCallerLeftIsStopped(t *testing.T) {
	engine := newFakeEngine()
	m := newTestManager(t, engine)
	id, _, _ := m.Acquire(context.Background(), thread("1", "acct", 1))
	ctx, cancel := context.WithCancel(context.Background())
	var stopped []string
	engine.exec = func(cmd []string, options docker.ExecOptions) (int, error) {
		if cmd[0] == "pkill" {
			stopped = cmd
			return 0, nil
		}
		cancel()
		return 0, context.Canceled
	}
	if _, err := m.Run(ctx, id, "sleep 99", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	marker := engine.execs[0][6]
	if strings.Join(stopped, " ") != "pkill -TERM -f "+marker+"$" {
		t.Errorf("stopped with %q, want the marker %s", stopped, marker)
	}
}

// A container that died, or that someone else removed, is a sandbox that is
// gone: the call says so, and the registry lets it go so the next request
// makes a new one.
func TestADeadContainerIsGone(t *testing.T) {
	engine := newFakeEngine()
	m := newTestManager(t, engine)
	ctx := context.Background()
	id, _, _ := m.Acquire(ctx, thread("1", "acct", 1))
	engine.containers[id].state = "exited"
	if _, err := m.Run(ctx, id, "true", time.Minute); !errors.Is(err, ErrGone) {
		t.Fatalf("err = %v", err)
	}
	if m.Count() != 0 || engine.count() != 0 {
		t.Errorf("the dead sandbox is still held: %d entries, %d containers", m.Count(), engine.count())
	}
	if again, created, err := m.Acquire(ctx, thread("1", "acct", 1)); err != nil || !created || again == id {
		t.Errorf("the next request: %s, created %v (%v)", again, created, err)
	}
	if _, err := m.Run(ctx, "no-such-id", "true", time.Minute); !errors.Is(err, ErrGone) {
		t.Errorf("an unknown id: %v", err)
	}
}

// The sweep removes a sandbox idle past its time to live, and never one in
// use, however long its command has run.
func TestTheSweepRemovesOnlyIdleSandboxes(t *testing.T) {
	engine := newFakeEngine()
	m := newTestManager(t, engine)
	clock := time.Now()
	m.now = func() time.Time { return clock }
	ctx := context.Background()
	idle, _, _ := m.Acquire(ctx, thread("1", "acct", 2))
	busy, _, _ := m.Acquire(ctx, thread("2", "acct", 2))

	inCommand, release := make(chan struct{}), make(chan struct{})
	engine.exec = func([]string, docker.ExecOptions) (int, error) {
		close(inCommand)
		<-release
		return 0, nil
	}
	done := make(chan error)
	go func() {
		_, err := m.Run(ctx, busy, "render", time.Hour)
		done <- err
	}()
	<-inCommand
	clock = clock.Add(11 * time.Minute)
	m.Sweep(ctx)
	if _, ok := engine.containers[idle]; ok {
		t.Error("the idle sandbox outlived its time to live")
	}
	if _, ok := engine.containers[busy]; !ok {
		t.Fatal("the sandbox in use was removed under its command")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Its time to live counts from when the command ended.
	clock = clock.Add(9 * time.Minute)
	m.Sweep(ctx)
	if _, ok := engine.containers[busy]; !ok {
		t.Error("removed before its time to live from its last use")
	}
	clock = clock.Add(2 * time.Minute)
	m.Sweep(ctx)
	if engine.count() != 0 {
		t.Error("not removed after its time to live from its last use")
	}
}

// A restarted service takes back this stack's running sandboxes, removes
// its stopped or unreadable ones, and never touches another stack's.
func TestAdoptTakesBackThisStacksSandboxesOnly(t *testing.T) {
	engine := newFakeEngine()
	first := newTestManager(t, engine)
	ctx := context.Background()
	kept, _, _ := first.Acquire(ctx, thread("1", "acct", 3))
	stopped, _, _ := first.Acquire(ctx, thread("2", "acct", 3))
	engine.containers[stopped].state = "exited"
	bad, _, _ := first.Acquire(ctx, thread("3", "acct", 3))
	engine.containers[bad].spec.Labels[labelIdleTTL] = "soon"
	theirs, _ := engine.Create(ctx, "otherstack-sandbox-thread-1", docker.ContainerSpec{Labels: map[string]string{labelStack: "otherstack", labelKind: "thread", labelKey: "1", labelAccount: "acct", labelIdleTTL: "600"}})
	engine.containers[theirs].state = "running"

	restarted := newTestManager(t, engine)
	if err := restarted.Adopt(ctx); err != nil {
		t.Fatal(err)
	}
	if restarted.Count() != 1 {
		t.Fatalf("adopted %d, want 1", restarted.Count())
	}
	if id, created, err := restarted.Acquire(ctx, thread("1", "acct", 3)); err != nil || created || id != kept {
		t.Errorf("thread 1 after the restart: %s, created %v (%v)", id, created, err)
	}
	for _, id := range []string{stopped, bad} {
		if _, ok := engine.containers[id]; ok {
			t.Errorf("%s was left", id)
		}
	}
	if _, ok := engine.containers[theirs]; !ok {
		t.Error("another stack's sandbox was removed")
	}
}

func TestFilesGoInAndOutAsTheSandboxUser(t *testing.T) {
	engine := newFakeEngine()
	m := newTestManager(t, engine)
	ctx := context.Background()
	id, _, _ := m.Acquire(ctx, thread("1", "acct", 1))
	files := map[string][]byte{}
	engine.exec = func(cmd []string, options docker.ExecOptions) (int, error) {
		if options.User != "1000:1000" {
			return 0, fmt.Errorf("ran as %q", options.User)
		}
		name := cmd[len(cmd)-1]
		switch cmd[2] {
		case writeScript:
			data, err := io.ReadAll(options.Stdin)
			files[name] = data
			return 0, err
		case readScript:
			data, ok := files[name]
			if !ok {
				return notFoundCode, nil
			}
			// The size line and the file may arrive in any split.
			fmt.Fprintf(options.Stdout, "%d", len(data))
			fmt.Fprintf(options.Stdout, "\n%s", data[:2])
			_, _ = options.Stdout.Write(data[2:])
			return 0, nil
		}
		return 1, nil
	}
	if err := m.WriteFile(ctx, id, "/workspace/a.bin", strings.NewReader("hello\x00")); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	var size int64
	if err := m.ReadFile(ctx, id, "/workspace/a.bin", func(n int64) (io.Writer, error) { size = n; return &got, nil }); err != nil || got.String() != "hello\x00" || size != 6 {
		t.Errorf("read %q, size %d (%v)", got.String(), size, err)
	}
	opened := false
	if err := m.ReadFile(ctx, id, "/workspace/none", func(int64) (io.Writer, error) { opened = true; return io.Discard, nil }); !errors.Is(err, ErrNotFound) || opened {
		t.Errorf("a missing file: %v (opened %v)", err, opened)
	}
	for _, bad := range []string{"relative", "/workspace/../etc/passwd", "/workspace/", "/a\x00b"} {
		if err := m.WriteFile(ctx, id, bad, strings.NewReader("")); !errors.Is(err, ErrBadPath) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestSizesAreReadInTheSettingsSpelling(t *testing.T) {
	for text, want := range map[string]int64{"2": 2e9, "1.5": 15e8, "500m": 5e8, " 4 ": 4e9} {
		if got, err := ParseCPU(text); err != nil || got != want {
			t.Errorf("ParseCPU(%q) = %d (%v), want %d", text, got, err, want)
		}
	}
	for _, bad := range []string{"", "0", "-1", "two", "5m", "2Gi", "Inf"} {
		if _, err := ParseCPU(bad); err == nil {
			t.Errorf("ParseCPU(%q) was accepted", bad)
		}
	}
	for text, want := range map[string]int64{"6Gi": 6 << 30, "512Mi": 512 << 20, "1Ti": 1 << 40, "134217728": 128 << 20} {
		if got, err := ParseMemory(text); err != nil || got != want {
			t.Errorf("ParseMemory(%q) = %d (%v), want %d", text, got, err, want)
		}
	}
	for _, bad := range []string{"", "6G", "6GB", "1.5Gi", "-1Gi", "1Mi", "99999999999Ti"} {
		if _, err := ParseMemory(bad); err == nil {
			t.Errorf("ParseMemory(%q) was accepted", bad)
		}
	}
}
