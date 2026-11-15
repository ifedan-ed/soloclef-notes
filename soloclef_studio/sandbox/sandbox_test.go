package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The accounts of these tests.
const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
)

const stubKey = "stub-key"

// stub is the sandbox service in memory, speaking its protocol
// (soloclef_sandboxd/api): find-or-create with the account's count, removal,
// commands and files, with the service's status codes and error bodies.
// The tests below that name a real service run against the real thing when
// it is configured; these run everywhere.
type stub struct {
	t  *testing.T
	mu sync.Mutex
	// boxes is every sandbox by id; byKey finds one by kind and key.
	boxes map[string]*stubBox
	byKey map[string]string
	next  int
	// acquired is every find-or-create body received.
	acquired []map[string]any
	// run answers a command; nil answers a plain success.
	run func(command string, timeoutMS int64) string
	// shortBody makes a file's body end before its Content-Length.
	shortBody bool
	// waitHeld is how long a call that asked to wait is held before it is
	// answered, as the service holds one until a place frees.
	waitHeld time.Duration
}

type stubBox struct {
	kind, account string
	files         map[string][]byte
}

func newStub(t *testing.T) (*stub, *httptest.Server) {
	s := &stub{t: t, boxes: map[string]*stubBox{}, byKey: map[string]string{}}
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	return s, server
}

func fail(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q,"message":"the stub says %s"}`, code, code)
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+stubKey {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/sandboxes")
	switch {
	case path == "" && r.Method == http.MethodPost:
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.t.Errorf("acquire body: %v", err)
		}
		s.acquired = append(s.acquired, body)
		if _, waits := body["waitSeconds"]; waits {
			time.Sleep(s.waitHeld)
		}
		key := body["kind"].(string) + "/" + body["key"].(string)
		if id, ok := s.byKey[key]; ok {
			fmt.Fprintf(w, `{"id":%q,"created":false}`, id)
			return
		}
		if body["kind"] == "thread" {
			count := 0
			for _, box := range s.boxes {
				if box.kind == "thread" && box.account == body["account"] {
					count++
				}
			}
			if float64(count) >= body["maxPerAccount"].(float64) {
				fail(w, http.StatusTooManyRequests, "account_full")
				return
			}
		}
		s.next++
		id := "box-" + strconv.Itoa(s.next)
		s.boxes[id] = &stubBox{kind: body["kind"].(string), account: body["account"].(string), files: map[string][]byte{}}
		s.byKey[key] = id
		fmt.Fprintf(w, `{"id":%q,"created":true}`, id)
	case r.Method == http.MethodDelete:
		key := strings.TrimPrefix(path, "/")
		if id, ok := s.byKey[key]; ok {
			delete(s.boxes, id)
			delete(s.byKey, key)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		id, route, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
		box := s.boxes[id]
		if box == nil {
			fail(w, http.StatusGone, "gone")
			return
		}
		switch {
		case route == "commands" && r.Method == http.MethodPost:
			var body struct {
				Command   string `json:"command"`
				TimeoutMS int64  `json:"timeoutMs"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TimeoutMS <= 0 {
				s.t.Errorf("a command must carry its time limit: %+v, %v", body, err)
			}
			answer := `{"output":"","exitCode":0}`
			if s.run != nil {
				answer = s.run(body.Command, body.TimeoutMS)
			}
			fmt.Fprint(w, answer)
		case route == "files" && r.Method == http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			box.files[r.URL.Query().Get("path")] = data
			w.WriteHeader(http.StatusNoContent)
		case route == "files" && r.Method == http.MethodGet:
			data, ok := box.files[r.URL.Query().Get("path")]
			if !ok {
				fail(w, http.StatusNotFound, "not_found")
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			if s.shortBody {
				data = data[:len(data)/2]
			}
			_, _ = w.Write(data)
		default:
			s.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}
}

func settingsFor(address string) Settings {
	return Settings{
		URL: address, APIKey: stubKey, CPU: "2", Memory: "2Gi",
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
		"URL":            func(s *Settings) { s.URL = " " },
		"APIKey":         func(s *Settings) { s.APIKey = "" },
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
	bad := full
	bad.URL = "sandbox:8080"
	if _, err := New(bad, &http.Client{}); err == nil {
		t.Error("an address without a scheme was accepted")
	}
	if _, err := New(full, &http.Client{Timeout: time.Second}); err == nil {
		t.Error("a client with a whole-request timeout was accepted; it would cut a long command short")
	}
	if _, err := New(full, &http.Client{}); err != nil {
		t.Errorf("full settings: %v", err)
	}
}

// A thread's sandbox is asked for with everything the service needs to make
// it, and the same one comes back for the thread while it lives.
func TestAThreadHasOneSandboxAndKeepsIt(t *testing.T) {
	s, client := stubClient(t)
	ctx := context.Background()
	first, err := client.ForThread(ctx, strings.ToUpper(ada), 7)
	if err != nil {
		t.Fatal(err)
	}
	again, err := client.ForThread(ctx, ada, 7)
	if err != nil || again.ID != first.ID {
		t.Fatalf("found %v (%v), want %s", again, err, first.ID)
	}
	s.mu.Lock()
	sent := s.acquired[0]
	s.mu.Unlock()
	// The account is lower-cased: one account is one count whatever case
	// its token carried.
	want := map[string]any{"kind": "thread", "key": "7", "account": ada, "cpu": "2", "memory": "2Gi",
		"idleTtlSeconds": float64(600), "maxPerAccount": float64(2)}
	for field, value := range want {
		if sent[field] != value {
			t.Errorf("%s = %v, want %v", field, sent[field], value)
		}
	}
	if err := client.Terminate(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Run(ctx, "true"); !errors.Is(err, ErrGone) {
		t.Errorf("a removed thread's sandbox: err = %v", err)
	}
	// Removing a thread with no sandbox is not an error.
	if err := client.Terminate(ctx, 8); err != nil {
		t.Errorf("a thread with none: %v", err)
	}
}

// The account's limit is the service's answer, and says the account is
// full rather than that something broke.
func TestAnAccountAtItsLimitIsToldSo(t *testing.T) {
	_, client := stubClient(t)
	ctx := context.Background()
	for thread := int64(1); thread <= 2; thread++ {
		if _, err := client.ForThread(ctx, ada, thread); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.ForThread(ctx, ada, 3); !errors.Is(err, ErrAccountFull) {
		t.Fatalf("a third thread: err = %v", err)
	}
	if _, err := client.ForThread(ctx, ben, 3); err != nil {
		t.Errorf("another account is not held to the first's limit: %v", err)
	}
}

// A caller nobody watches asks the service to hold the call for a place,
// for a whole number of seconds, and does not hang up on its own shorter
// bound while the service holds it. ForThread never asks to wait.
func TestAWaitingCallAsksForItsWaitAndOutlastsTheCallBound(t *testing.T) {
	s, server := newStub(t)
	settings := settingsFor(server.URL)
	settings.CommandTimeout = 50 * time.Millisecond
	client, err := New(settings, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	s.waitHeld = 200 * time.Millisecond
	ctx := context.Background()
	if _, err := client.WaitForThread(ctx, ada, 1, 1500*time.Millisecond); err != nil {
		t.Fatalf("a call held for less than its wait: %v", err)
	}
	if _, err := client.ForThread(ctx, ada, 2); err != nil {
		t.Fatal(err)
	}
	if got := s.acquired[0]["waitSeconds"]; got != float64(2) {
		t.Errorf("waitSeconds = %v, want 2 (1.5s rounded up)", got)
	}
	if _, asked := s.acquired[1]["waitSeconds"]; asked {
		t.Error("ForThread asked to wait")
	}
	if _, err := client.WaitForThread(ctx, ada, 3, 0); err == nil {
		t.Error("a wait of nothing was accepted")
	}
}

func TestACommandsOutputAndEnding(t *testing.T) {
	s, client := stubClient(t)
	ctx := context.Background()
	var limits []int64
	s.run = func(command string, timeoutMS int64) string {
		limits = append(limits, timeoutMS)
		switch command {
		case "printf":
			return `{"output":"a\n\nno-newline","exitCode":0}`
		case "exit 3":
			return `{"output":"","exitCode":3}`
		case "sleep":
			return `{"output":"partial","exitCode":-1,"timedOut":true}`
		case "loud":
			return `{"output":"x","exitCode":0,"truncated":true}`
		}
		return `{"output":"","exitCode":0}`
	}
	session, err := client.ForThread(ctx, ada, 1)
	if err != nil {
		t.Fatal(err)
	}
	for command, want := range map[string]Result{
		"printf": {Output: "a\n\nno-newline"},
		"exit 3": {ExitCode: 3},
		"sleep":  {Output: "partial", ExitCode: -1, TimedOut: true},
		"loud":   {Output: "x", Truncated: true},
	} {
		if got, err := session.Run(ctx, command); err != nil || got != want {
			t.Errorf("%s = %+v (%v), want %+v", command, got, err, want)
		}
	}
	for _, limit := range limits {
		if limit != 30_000 {
			t.Errorf("the limits sent were %v: every one CommandTimeout", limits)
			break
		}
	}
	if (Result{ExitCode: 0}).OK() == false || (Result{ExitCode: 1}).OK() {
		t.Error("OK is the exit status being zero")
	}
}

func TestFilesGoInAndComeOut(t *testing.T) {
	s, client := stubClient(t)
	ctx := context.Background()
	session, err := client.ForThread(ctx, ada, 1)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("hello\x00binary")
	if err := session.WriteFile(ctx, "/workspace/a/b.bin", data); err != nil {
		t.Fatal(err)
	}
	if got, err := session.ReadFile(ctx, "/workspace/a/b.bin", 1024); err != nil || !bytes.Equal(got, data) {
		t.Errorf("read back %q (%v)", got, err)
	}
	if _, err := session.ReadFile(ctx, "/workspace/a/b.bin", 3); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a file over the limit: err = %v", err)
	}
	if _, err := session.ReadFile(ctx, "/workspace/missing", 1024); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing file: err = %v", err)
	}
	// A body cut short is an error, never a shorter file taken as whole.
	s.mu.Lock()
	s.shortBody = true
	s.mu.Unlock()
	if _, err := session.ReadFile(ctx, "/workspace/a/b.bin", 1024); err == nil {
		t.Error("a file cut short was read as whole")
	}
}

func TestAWrongKeyIsAnErrorThatNamesTheStatus(t *testing.T) {
	_, server := newStub(t)
	settings := settingsFor(server.URL)
	settings.APIKey = "wrong"
	client, err := New(settings, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ForThread(context.Background(), ada, 1); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v", err)
	}
}
