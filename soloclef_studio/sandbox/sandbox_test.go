package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// The accounts of these tests.
const (
	ada          = "aaaaaaaa-0000-4000-8000-000000000001"
	ben          = "bbbbbbbb-0000-4000-8000-000000000002"
	benchAccount = "cccccccc-0000-4000-8000-000000000003"
)

// stub is an OpenSandbox server in memory: the lifecycle routes and the
// daemon's command and file routes behind the proxy path, with the status
// codes, bodies and stream framing recorded from opensandbox/server v0.2.2
// with opensandbox/execd v1.0.21. The tests below that name a real server
// run against the real thing when it is configured; these run everywhere.
type stub struct {
	t  *testing.T
	mu sync.Mutex
	// boxes is every sandbox by id; a removed one is deleted.
	boxes map[string]*stubBox
	next  int
	// renewed counts renewals per sandbox.
	renewed map[string]int
	// stream is what POST /command answers; nil answers a plain success.
	stream func(command string) string
	// ignoreFilter makes the list answer every sandbox whatever was asked.
	ignoreFilter bool
}

type stubBox struct {
	metadata map[string]string
	request  map[string]any
	files    map[string][]byte
}

const stubKey = "stub-key"

func newStub(t *testing.T) (*stub, *httptest.Server) {
	s := &stub{t: t, boxes: map[string]*stubBox{}, renewed: map[string]int{}}
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	return s, server
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get(keyHeader) != stubKey {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"code":"MISSING_API_KEY","message":"Authentication credentials are missing."}`)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/sandboxes")
	switch {
	case path == "" && r.Method == http.MethodPost:
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			s.t.Errorf("create body: %v", err)
		}
		s.next++
		id := fmt.Sprintf("box-%d", s.next)
		metadata := map[string]string{}
		for k, v := range request["metadata"].(map[string]any) {
			metadata[k] = v.(string)
		}
		s.boxes[id] = &stubBox{metadata: metadata, request: request, files: map[string][]byte{}}
		fmt.Fprintf(w, `{"id":%q,"status":{"state":"Running"},"createdAt":"2026-01-01T00:00:00Z"}`, id)
	case path == "" && r.Method == http.MethodGet:
		// The filter is a query string of its own, every pair of which
		// must match, as the real server reads it.
		filter, err := url.ParseQuery(r.URL.Query().Get("metadata"))
		if err != nil {
			s.t.Errorf("list filter: %v", err)
		}
		var items []string
		for id, box := range s.boxes {
			matches := true
			for label := range filter {
				matches = matches && box.metadata[label] == filter.Get(label)
			}
			if s.ignoreFilter || matches {
				encoded, _ := json.Marshal(box.metadata)
				items = append(items, fmt.Sprintf(`{"id":%q,"status":{"state":"Running"},"metadata":%s}`, id, encoded))
			}
		}
		fmt.Fprintf(w, `{"items":[%s],"pagination":{"page":1,"pageSize":200,"totalItems":%d,"totalPages":1,"hasNextPage":false}}`, strings.Join(items, ","), len(items))
	default:
		id, rest, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
		box := s.boxes[id]
		if box == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"code":"DOCKER::SANDBOX_NOT_FOUND","message":"Sandbox %s not found."}`, id)
			return
		}
		switch {
		case rest == "" && r.Method == http.MethodDelete:
			delete(s.boxes, id)
			w.WriteHeader(http.StatusNoContent)
		case rest == "renew-expiration":
			var body struct {
				ExpiresAt time.Time `json:"expiresAt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.ExpiresAt.After(time.Now()) {
				s.t.Errorf("renewal must name a time in the future: %v, %v", body.ExpiresAt, err)
			}
			s.renewed[id]++
			fmt.Fprint(w, `{"expiresAt":"2026-01-01T00:00:00Z"}`)
		case rest == "proxy/44772/command":
			var body struct {
				Command string `json:"command"`
				Timeout int64  `json:"timeout"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Timeout <= 0 {
				s.t.Errorf("a command must carry its time limit: %+v, %v", body, err)
			}
			answer := `{"type":"init","text":"x","timestamp":1}` + "\n\n" + `{"type":"execution_complete","execution_time":3,"timestamp":2}` + "\n\n"
			if s.stream != nil {
				answer = s.stream(body.Command)
			}
			fmt.Fprint(w, answer)
		case rest == "proxy/44772/files/upload":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				s.t.Errorf("upload: %v", err)
				return
			}
			var metadata struct {
				Path string `json:"path"`
			}
			description, _, err := r.FormFile("metadata")
			if err != nil || json.NewDecoder(description).Decode(&metadata) != nil || metadata.Path == "" {
				s.t.Errorf("upload without a readable metadata part: %v", err)
				return
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				s.t.Errorf("upload without a file part: %v", err)
				return
			}
			box.files[metadata.Path], _ = io.ReadAll(file)
		case rest == "proxy/44772/files/download":
			data, found := box.files[r.URL.Query().Get("path")]
			if !found {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"code":"FILE_NOT_FOUND","message":"file not found."}`)
				return
			}
			_, _ = w.Write(data)
		default:
			s.t.Errorf("the stub has no route %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotImplemented)
		}
	}
}

// The stacks of these tests: two deployments on one server's Docker.
const (
	stackA = "stack-a"
	stackB = "stack_b"
)

func settingsFor(address string) Settings {
	return Settings{
		Stack: stackA, URL: address, APIKey: stubKey, Image: "image:tag", CPU: "2", Memory: "2Gi",
		IdleTTL: 10 * time.Minute, CommandTimeout: 30 * time.Second, MaxPerAccount: 2,
	}
}

func stubClient(t *testing.T) (*stub, *Client) {
	t.Helper()
	s, server := newStub(t)
	client, err := New(settingsFor(server.URL), &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	return s, client
}

func TestEverySettingIsRequired(t *testing.T) {
	full := settingsFor("http://sandbox:8080")
	for name, strip := range map[string]func(*Settings){
		"Stack":          func(s *Settings) { s.Stack = "" },
		"URL":            func(s *Settings) { s.URL = " " },
		"APIKey":         func(s *Settings) { s.APIKey = "" },
		"Image":          func(s *Settings) { s.Image = "" },
		"CPU":            func(s *Settings) { s.CPU = "" },
		"Memory":         func(s *Settings) { s.Memory = "" },
		"IdleTTL":        func(s *Settings) { s.IdleTTL = 59 * time.Second },
		"CommandTimeout": func(s *Settings) { s.CommandTimeout = 0 },
		"MaxPerAccount":  func(s *Settings) { s.MaxPerAccount = 0 },
	} {
		settings := full
		strip(&settings)
		if _, err := New(settings, &http.Client{}); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("without %s: err = %v", name, err)
		}
	}
	// A stack's name is a label value, and one that could not be a label
	// would be a sandbox no lookup can find again.
	for _, bad := range []string{"Stack", "a b", "-a", "a=b", strings.Repeat("a", 64)} {
		settings := full
		settings.Stack = bad
		if _, err := New(settings, &http.Client{}); err == nil {
			t.Errorf("the stack %q was accepted", bad)
		}
	}
	if _, err := New(full, &http.Client{Timeout: time.Second}); err == nil {
		t.Error("a client with a whole-request timeout was accepted; it would cut a long command's stream")
	}
	if _, err := New(full, &http.Client{}); err != nil {
		t.Errorf("full settings: %v", err)
	}
}

func TestAThreadHasOneSandboxAndKeepsIt(t *testing.T) {
	s, client := stubClient(t)
	ctx := context.Background()
	first, err := client.ForThread(ctx, ada, 41)
	if err != nil {
		t.Fatal(err)
	}
	box := s.boxes[first.ID]
	if box == nil || box.metadata[threadLabel] != "41" || box.metadata[accountLabel] != ada {
		t.Fatalf("created = %+v", box)
	}
	// What the sandbox is created as: no network, the image's entrypoint,
	// the limits and the time to live the deployment set.
	request := box.request
	if request["networkPolicy"].(map[string]any)["defaultAction"] != "deny" {
		t.Errorf("the sandbox was not created with its network denied: %v", request["networkPolicy"])
	}
	if request["timeout"].(float64) != 600 || request["image"].(map[string]any)["uri"] != "image:tag" ||
		request["resourceLimits"].(map[string]any)["memory"] != "2Gi" || request["resourceLimits"].(map[string]any)["cpu"] != "2" ||
		fmt.Sprint(request["entrypoint"]) != "[/opt/code-interpreter/code-interpreter.sh]" {
		t.Errorf("create request = %v", request)
	}

	again, err := client.ForThread(ctx, ada, 41)
	if err != nil || again.ID != first.ID || len(s.boxes) != 1 {
		t.Fatalf("the second call got %v (%v) with %d sandboxes; want the first one again", again, err, len(s.boxes))
	}
	// Within half a time to live the same process asks the server nothing.
	if s.renewed[first.ID] != 0 {
		t.Errorf("a sandbox just created was renewed %d times by the next call", s.renewed[first.ID])
	}
	// Past half of it, the sandbox is renewed, without a list.
	client.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
	if later, err := client.ForThread(ctx, ada, 41); err != nil || later.ID != first.ID || s.renewed[first.ID] != 1 {
		t.Errorf("after six of ten minutes: %v (%v), renewed %d times, want the same sandbox renewed once", later, err, s.renewed[first.ID])
	}

	// Another process, which has never seen the thread, finds the same one.
	other, err := New(client.settings, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if found, err := other.ForThread(ctx, ada, 41); err != nil || found.ID != first.ID || s.renewed[first.ID] != 2 {
		t.Errorf("a second client got %v (%v), renewals %d; want %s, renewed on adoption", found, err, s.renewed[first.ID], first.ID)
	}
}

// The limit is per account, counted by the "soloclef_account" label: two
// accounts never share a count, and one account's token spelled in another
// case is the same account.
func TestTheAccountLimitIsCountedOnTheServer(t *testing.T) {
	_, client := stubClient(t)
	ctx := context.Background()
	for thread := int64(1); thread <= 2; thread++ {
		if _, err := client.ForThread(ctx, ada, thread); err != nil {
			t.Fatal(err)
		}
	}
	// A fresh client: the count is not this process's memory.
	fresh, _ := New(client.settings, &http.Client{})
	if _, err := fresh.ForThread(ctx, ada, 3); !errors.Is(err, ErrAccountFull) {
		t.Fatalf("a third sandbox in an account limited to two: err = %v", err)
	}
	if _, err := fresh.ForThread(ctx, strings.ToUpper(ada), 4); !errors.Is(err, ErrAccountFull) {
		t.Errorf("the same account in capitals got past its limit: err = %v", err)
	}
	if _, err := fresh.ForThread(ctx, ben, 3); err != nil {
		t.Errorf("another account was refused: %v", err)
	}
	// A thread that already has one is not refused by its own sandbox.
	if _, err := fresh.ForThread(ctx, ada, 2); err != nil {
		t.Errorf("a thread with a sandbox was refused at the limit: %v", err)
	}
	// Removing one frees a place.
	if err := fresh.Terminate(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.ForThread(ctx, ada, 3); err != nil {
		t.Errorf("after one was removed: %v", err)
	}
	if err := fresh.Terminate(ctx, 999); err != nil {
		t.Errorf("removing the sandbox of a thread that has none: %v", err)
	}
}

func TestAnotherThreadsSandboxIsNeverAdopted(t *testing.T) {
	s, client := stubClient(t)
	ctx := context.Background()
	theirs, err := client.ForThread(ctx, ada, 1)
	if err != nil {
		t.Fatal(err)
	}
	// A server that ignores the label filter answers every sandbox.
	s.ignoreFilter = true
	mine, err := client.ForThread(ctx, ben, 2)
	if err != nil {
		t.Fatal(err)
	}
	if mine.ID == theirs.ID {
		t.Fatal("thread 2 was handed thread 1's sandbox, and its files")
	}
}

// Two stacks on one Docker count their threads from one, so both have a
// thread 1; each finds, counts and removes its own sandboxes only, and a
// sandbox that carries no stack (the Python tier's) is never touched.
func TestAnotherStacksSandboxIsNeverTouched(t *testing.T) {
	s, server := newStub(t)
	ctx := context.Background()
	clientFor := func(stack string) *Client {
		settings := settingsFor(server.URL)
		settings.Stack = stack
		client, err := New(settings, &http.Client{})
		if err != nil {
			t.Fatal(err)
		}
		return client
	}
	a, b := clientFor(stackA), clientFor(stackB)
	s.mu.Lock()
	s.boxes["unstacked"] = &stubBox{metadata: map[string]string{threadLabel: "1", accountLabel: ada}, files: map[string][]byte{}}
	s.mu.Unlock()

	// Stack A fills the account's two sandboxes.
	ours, err := a.ForThread(ctx, ada, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ForThread(ctx, ada, 2); err != nil {
		t.Fatal(err)
	}
	if got := s.boxes[ours.ID].metadata[stackLabel]; got != stackA {
		t.Errorf("a new sandbox's stack = %q, want %q", got, stackA)
	}
	// Stack B's thread 1 is not stack A's, nor the unstacked one, and stack
	// A's sandboxes do not count against the account on stack B.
	theirs, err := b.ForThread(ctx, ada, 1)
	if err != nil {
		t.Fatalf("stack B was refused for stack A's sandboxes: %v", err)
	}
	if theirs.ID == ours.ID || theirs.ID == "unstacked" {
		t.Fatalf("stack B's thread 1 was handed %s", theirs.ID)
	}
	if err := b.Terminate(ctx, 1); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	_, oursLeft := s.boxes[ours.ID]
	_, unstackedLeft := s.boxes["unstacked"]
	_, theirsLeft := s.boxes[theirs.ID]
	s.mu.Unlock()
	if !oursLeft || !unstackedLeft || theirsLeft {
		t.Errorf("after stack B removed its thread 1: stack A's left %v, unstacked left %v, stack B's left %v", oursLeft, unstackedLeft, theirsLeft)
	}

	// A server that ignores the filter is held to the stack here.
	s.ignoreFilter = true
	again, err := clientFor(stackB).ForThread(ctx, ada, 1)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID == ours.ID || again.ID == "unstacked" {
		t.Fatalf("with the filter ignored, stack B's thread 1 was handed %s", again.ID)
	}
	// And jobs alike: the same attempt's name on two stacks is two sandboxes.
	s.ignoreFilter = false
	jobA, err := a.ForJob(ctx, ada, "video-1-attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	jobB, err := b.ForJob(ctx, ada, "video-1-attempt-1")
	if err != nil || jobB.ID == jobA.ID {
		t.Fatalf("stack B's job got %v (%v), stack A's is %s", jobB, err, jobA.ID)
	}
	if err := b.TerminateJob(ctx, "video-1-attempt-1"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	_, jobALeft := s.boxes[jobA.ID]
	s.mu.Unlock()
	if !jobALeft {
		t.Error("stack B removed stack A's job sandbox")
	}
}

func TestConcurrentCallsOfOneThreadCreateOneSandbox(t *testing.T) {
	s, client := stubClient(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.ForThread(context.Background(), ada, 5); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(s.boxes) != 1 {
		t.Errorf("%d sandboxes were created for one thread", len(s.boxes))
	}
}

func TestACommandsOutputAndEnding(t *testing.T) {
	s, client := stubClient(t)
	session, err := client.ForThread(context.Background(), ada, 1)
	if err != nil {
		t.Fatal(err)
	}
	event := func(body string) string { return body + "\n\n" }
	for _, test := range []struct {
		name, stream string
		want         Result
		fails        bool
	}{
		{
			// Recorded from `printf "a\n\nb  \nno-newline"`.
			name: "lines, an empty line and a last line without an end",
			stream: event(`{"type":"init","text":"id","timestamp":1}`) + event(`{"type":"ping","text":"pong","timestamp":1}`) +
				event(`{"type":"stdout","text":"a","timestamp":1}`) + event(`{"type":"stdout","text":"\n","timestamp":1}`) +
				event(`{"type":"stdout","text":"b  ","timestamp":1}`) + event(`{"type":"stdout","text":"no-newline","timestamp":1}`) +
				event(`{"type":"execution_complete","execution_time":3,"timestamp":1}`),
			want: Result{Output: "a\n\nb  \nno-newline\n"},
		},
		{
			name: "a failing command is a result with its exit status",
			stream: event(`{"type":"stdout","text":"hi","timestamp":1}`) + event(`{"type":"stderr","text":"err","timestamp":1}`) +
				event(`{"type":"error","timestamp":1,"error":{"ename":"CommandExecError","evalue":"3","traceback":["exit status 3"]}}`),
			want: Result{Output: "hi\nerr\n", ExitCode: 3},
		},
		{
			name:   "a command killed at its limit",
			stream: event(`{"type":"error","timestamp":1,"error":{"ename":"CommandExecError","evalue":"-1","traceback":["signal: killed"]}}`),
			want:   Result{ExitCode: -1, TimedOut: true},
		},
		{
			name:   "a stream cut before the command ended is a failure, not a success",
			stream: event(`{"type":"stdout","text":"half","timestamp":1}`),
			fails:  true,
		},
		{
			name:   "output past the bound is dropped and said to be",
			stream: event(`{"type":"stdout","text":"`+strings.Repeat("x", maxOutputBytes)+`","timestamp":1}`) + event(`{"type":"stdout","text":"kept","timestamp":1}`) + event(`{"type":"execution_complete","timestamp":1}`),
			want:   Result{Output: "kept\n", Truncated: true},
		},
	} {
		s.mu.Lock()
		s.stream = func(string) string { return test.stream }
		s.mu.Unlock()
		got, err := session.Run(context.Background(), "anything")
		if test.fails {
			if err == nil {
				t.Errorf("%s: got %+v, want an error", test.name, got)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Errorf("%s: got %+v (%v), want %+v", test.name, got, err, test.want)
		}
	}
}

func TestFilesGoInAndComeOut(t *testing.T) {
	_, client := stubClient(t)
	ctx := context.Background()
	session, err := client.ForThread(ctx, ada, 1)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("hello\x00binary")
	if err := session.WriteFile(ctx, "/workspace/a/b.bin", data); err != nil {
		t.Fatal(err)
	}
	got, err := session.ReadFile(ctx, "/workspace/a/b.bin", 1024)
	if err != nil || string(got) != string(data) {
		t.Fatalf("read back %q (%v)", got, err)
	}
	if _, err := session.ReadFile(ctx, "/workspace/a/b.bin", int64(len(data))-1); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a file over the limit: err = %v", err)
	}
	if got, err := session.ReadFile(ctx, "/workspace/a/b.bin", int64(len(data))); err != nil || len(got) != len(data) {
		t.Errorf("a file exactly at the limit: %d bytes, %v", len(got), err)
	}
	if _, err := session.ReadFile(ctx, "/workspace/missing", 1024); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing file: err = %v", err)
	}
}

func TestASandboxThatExpiredIsSaidToBeGone(t *testing.T) {
	s, client := stubClient(t)
	ctx := context.Background()
	session, err := client.ForThread(ctx, ada, 1)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	delete(s.boxes, session.ID)
	s.mu.Unlock()
	if _, err := session.Run(ctx, "true"); !errors.Is(err, ErrGone) {
		t.Errorf("running in a removed sandbox: err = %v", err)
	}
	if err := session.WriteFile(ctx, "/workspace/x", []byte("x")); !errors.Is(err, ErrGone) {
		t.Errorf("writing into a removed sandbox: err = %v", err)
	}
	// The next call for the thread makes a new one instead of failing.
	fresh, err := client.ForThread(ctx, ada, 1)
	if err != nil || fresh.ID == session.ID {
		t.Errorf("after expiry got %v (%v), want a new sandbox", fresh, err)
	}
}

func TestAWrongKeyIsAnErrorThatNamesTheStatus(t *testing.T) {
	_, server := newStub(t)
	settings := settingsFor(server.URL)
	settings.APIKey = "wrong"
	client, _ := New(settings, &http.Client{})
	if _, err := client.ForThread(context.Background(), ada, 1); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v", err)
	}
}

func TestALongCommandRenewsItsSandbox(t *testing.T) {
	s, client := stubClient(t)
	session, err := client.ForThread(context.Background(), ada, 1)
	if err != nil {
		t.Fatal(err)
	}
	// The keep-alive's interval is a third of the time to live; the
	// shortest the settings allow is too long for a test, so the interval
	// is set directly.
	client.keepAliveEvery = 20 * time.Millisecond
	stop := session.keepAlive(context.Background())
	time.Sleep(150 * time.Millisecond)
	stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.renewed[session.ID] < 2 {
		t.Errorf("renewed %d times in 150 ms at a 20 ms interval", s.renewed[session.ID])
	}
}

// The variables that point the tests below at a real OpenSandbox server.
// It must be a throwaway: the tests create and remove sandboxes on it.
const (
	realURLVariable   = "SOLOCLEF_TEST_SANDBOX_URL"
	realKeyVariable   = "SOLOCLEF_TEST_SANDBOX_KEY"
	realImageVariable = "SOLOCLEF_TEST_SANDBOX_IMAGE"
)

// realClient is a client of the real server for one stack: each test names
// stacks of its own for the run, so nothing it makes is any deployment's.
func realClient(tb testing.TB, stack string) *Client {
	tb.Helper()
	address := os.Getenv(realURLVariable)
	if address == "" {
		tb.Skipf("%s is not set; this test needs a throwaway OpenSandbox server", realURLVariable)
	}
	client, err := New(Settings{
		Stack: stack, URL: address, APIKey: os.Getenv(realKeyVariable), Image: os.Getenv(realImageVariable),
		CPU: "2", Memory: "2Gi", IdleTTL: 5 * time.Minute, CommandTimeout: 2 * time.Minute, MaxPerAccount: 2,
	}, &http.Client{})
	if err != nil {
		tb.Fatal(err)
	}
	return client
}

// TestARealSandbox is the same contract against the real server and the
// real image: create, find again, run, the output's framing, a time limit,
// files both ways, no network, and removal.
func TestARealSandbox(t *testing.T) {
	client := realClient(t, fmt.Sprintf("test-%d", time.Now().UnixNano()))
	ctx := context.Background()
	// A thread id no other test uses, so a rerun finds nothing stale.
	thread := time.Now().UnixNano()
	account := fmt.Sprintf("cccccccc-0000-4000-8000-%012d", thread%1_000_000_000_000)
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
	t.Logf("find and renew: %s", time.Since(started).Round(time.Millisecond))

	started = time.Now()
	result, err := session.Run(ctx, `printf 'a\n\nb  \nno-newline'`)
	if err != nil || !result.OK() || result.Output != "a\n\nb  \nno-newline\n" {
		t.Errorf("run = %+v (%v)", result, err)
	}
	t.Logf("command round trip: %s", time.Since(started).Round(time.Millisecond))

	// Standard error is part of the output. Where it lands among the lines
	// of standard output is the daemon's to decide, so it is asked alone.
	if result, err := session.Run(ctx, "echo err >&2"); err != nil || result.Output != "err\n" {
		t.Errorf("standard error = %+v (%v)", result, err)
	}
	if result, err := session.Run(ctx, "exit 3"); err != nil || result.ExitCode != 3 || result.TimedOut {
		t.Errorf("exit 3 = %+v (%v)", result, err)
	}

	// The time limit is the daemon's. A second client with a short one:
	// five seconds, because the same limit bounds its calls to the server,
	// and the server's list has been seen to take over a second.
	short, err := New(Settings{
		Stack: client.settings.Stack, URL: client.settings.URL, APIKey: client.settings.APIKey, Image: client.settings.Image, CPU: "2", Memory: "2Gi",
		IdleTTL: 5 * time.Minute, CommandTimeout: 5 * time.Second, MaxPerAccount: 2,
	}, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	limited, err := short.ForThread(ctx, account, thread)
	if err != nil || limited.ID != session.ID {
		t.Fatalf("a second client found %v (%v), want %s", limited, err, session.ID)
	}
	if result, err := limited.Run(ctx, "sleep 20"); err != nil || !result.TimedOut || result.OK() {
		t.Errorf("a command past its limit = %+v (%v)", result, err)
	}

	data := []byte("hello\x00binary")
	if err := session.WriteFile(ctx, "/workspace/deep/dir/file.bin", data); err != nil {
		t.Fatalf("writing: %v", err)
	}
	started = time.Now()
	got, err := session.ReadFile(ctx, "/workspace/deep/dir/file.bin", 1024)
	if err != nil || string(got) != string(data) {
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
	if _, err := session.Run(ctx, "true"); err == nil {
		t.Error("a command ran in a removed sandbox")
	}
}

// TestTwoRealStacks is the stack label against the real server, which finds
// sandboxes among every container on its Docker: two stacks' thread 1 are
// two sandboxes, a stack's account limit counts its own only, and removing
// one stack's thread leaves the other's running.
func TestTwoRealStacks(t *testing.T) {
	run := time.Now().UnixNano()
	a := realClient(t, fmt.Sprintf("test-%d-a", run))
	b := realClient(t, fmt.Sprintf("test-%d-b", run))
	ctx := context.Background()
	// The same thread and account on both, as two stacks' first thread of
	// one account would be.
	thread := run
	account := fmt.Sprintf("dddddddd-0000-4000-8000-%012d", run%1_000_000_000_000)
	t.Cleanup(func() {
		_ = a.Terminate(context.Background(), thread)
		_ = b.Terminate(context.Background(), thread)
		_ = a.Terminate(context.Background(), thread+1)
	})

	ours, err := a.ForThread(ctx, account, thread)
	if err != nil {
		t.Fatalf("stack A: %v", err)
	}
	if _, err := a.ForThread(ctx, account, thread+1); err != nil {
		t.Fatalf("stack A's second thread: %v", err)
	}
	// Stack A holds the account's two; stack B is not refused for them.
	theirs, err := b.ForThread(ctx, account, thread)
	if err != nil {
		t.Fatalf("stack B: %v", err)
	}
	if theirs.ID == ours.ID {
		t.Fatal("stack B was handed stack A's sandbox")
	}
	if err := b.Terminate(ctx, thread); err != nil {
		t.Fatalf("stack B removing its thread: %v", err)
	}
	if _, err := theirs.Run(ctx, "true"); err == nil {
		t.Error("stack B's sandbox outlived its removal")
	}
	again, err := a.ForThread(ctx, account, thread)
	if err != nil || again.ID != ours.ID {
		t.Fatalf("stack A found %v (%v) after stack B's removal, want %s", again, err, ours.ID)
	}
	if result, err := again.Run(ctx, "true"); err != nil || !result.OK() {
		t.Errorf("stack A's sandbox after stack B's removal: %+v (%v)", result, err)
	}
}

// BenchmarkRealCommand is one trivial command's round trip, sandbox found
// and renewed first, as a tool call pays for it.
func BenchmarkRealCommand(b *testing.B) {
	client := realClient(b, fmt.Sprintf("bench-%d", time.Now().UnixNano()))
	ctx := context.Background()
	thread := time.Now().UnixNano()
	b.Cleanup(func() { _ = client.Terminate(context.Background(), thread) })
	if _, err := client.ForThread(ctx, benchAccount, thread); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		session, err := client.ForThread(ctx, benchAccount, thread)
		if err != nil {
			b.Fatal(err)
		}
		if result, err := session.Run(ctx, "true"); err != nil || !result.OK() {
			b.Fatal(result, err)
		}
	}
}
