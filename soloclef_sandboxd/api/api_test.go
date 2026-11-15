package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_sandboxd/sandboxes"
)

const key = "0123456789abcdef0123456789abcdef"

// fakeManager records what the routes asked and answers what the test set.
type fakeManager struct {
	acquired sandboxes.Request
	released string
	command  string
	limit    time.Duration
	written  string
	err      error
	file     string
}

func (f *fakeManager) Acquire(_ context.Context, r sandboxes.Request) (string, bool, error) {
	f.acquired = r
	return "box-1", true, f.err
}

func (f *fakeManager) Release(_ context.Context, kind sandboxes.Kind, key string) error {
	f.released = string(kind) + "/" + key
	return f.err
}

func (f *fakeManager) Run(_ context.Context, id, command string, limit time.Duration) (sandboxes.Result, error) {
	f.command, f.limit = id+": "+command, limit
	return sandboxes.Result{Output: "hi\n", ExitCode: 2, Truncated: true}, f.err
}

func (f *fakeManager) ReadFile(_ context.Context, id, name string, open func(int64) (io.Writer, error)) error {
	if f.err != nil {
		return f.err
	}
	w, err := open(int64(len(f.file)))
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, f.file)
	return err
}

func (f *fakeManager) WriteFile(_ context.Context, id, name string, data io.Reader) error {
	raw, _ := io.ReadAll(data)
	f.written = id + " " + name + " " + string(raw)
	return f.err
}

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

func call(t *testing.T, handler http.Handler, method, target, body string, withKey bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if withKey {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func newHandler(manager Manager, docker Pinger) http.Handler {
	return Handler(manager, docker, key, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestEveryRouteButHealthNeedsTheKey(t *testing.T) {
	handler := newHandler(&fakeManager{}, pinger{})
	if got := call(t, handler, http.MethodGet, "/health", "", false); got.Code != http.StatusNoContent {
		t.Errorf("health: %d", got.Code)
	}
	if got := call(t, newHandler(&fakeManager{}, pinger{err: errors.New("down")}), http.MethodGet, "/health", "", false); got.Code != http.StatusServiceUnavailable {
		t.Errorf("health with Docker down: %d", got.Code)
	}
	for _, route := range []struct{ method, target string }{
		{http.MethodPost, "/v1/sandboxes"}, {http.MethodDelete, "/v1/sandboxes/thread/1"},
		{http.MethodPost, "/v1/sandboxes/box-1/commands"}, {http.MethodGet, "/v1/sandboxes/box-1/files?path=/x"},
		{http.MethodPut, "/v1/sandboxes/box-1/files?path=/x"},
	} {
		if got := call(t, handler, route.method, route.target, "{}", false); got.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without the key: %d", route.method, route.target, got.Code)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer "+key+"x")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("a wrong key: %d", recorder.Code)
	}
}

func TestAcquireReadsTheSettingsSpelling(t *testing.T) {
	manager := &fakeManager{}
	handler := newHandler(manager, pinger{})
	got := call(t, handler, http.MethodPost, "/v1/sandboxes",
		`{"kind":"thread","key":"7","account":"acct","cpu":"1.5","memory":"6Gi","idleTtlSeconds":600,"maxPerAccount":2}`, true)
	if got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != `{"id":"box-1","created":true}` {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	want := sandboxes.Request{Kind: sandboxes.Thread, Key: "7", Account: "acct", NanoCPUs: 15e8, MemoryBytes: 6 << 30, IdleTTL: 10 * time.Minute, MaxPerAccount: 2}
	if manager.acquired != want {
		t.Errorf("asked %+v, want %+v", manager.acquired, want)
	}
	// A call that will wait says for how long, in seconds.
	got = call(t, handler, http.MethodPost, "/v1/sandboxes",
		`{"kind":"thread","key":"7","account":"acct","cpu":"1.5","memory":"6Gi","idleTtlSeconds":600,"maxPerAccount":2,"waitSeconds":600}`, true)
	want.MaxWait = 10 * time.Minute
	if got.Code != http.StatusOK || manager.acquired != want {
		t.Errorf("a waiting call: %d, asked %+v, want %+v", got.Code, manager.acquired, want)
	}
	for _, bad := range []string{
		`{"kind":"thread","key":"7","account":"a","cpu":"lots","memory":"6Gi","idleTtlSeconds":600,"maxPerAccount":2}`,
		`{"kind":"thread","key":"7","account":"a","cpu":"2","memory":"6GB","idleTtlSeconds":600,"maxPerAccount":2}`,
		`{"kind":"thread","image":"alpine"}`,
		`not json`,
	} {
		if got := call(t, handler, http.MethodPost, "/v1/sandboxes", bad, true); got.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, got.Code)
		}
	}
}

func TestErrorsAnswerTheirCodes(t *testing.T) {
	for err, want := range map[error]struct {
		status int
		code   string
	}{
		sandboxes.ErrGone:                                 {http.StatusGone, `"gone"`},
		sandboxes.ErrAccountFull:                          {http.StatusTooManyRequests, `"account_full"`},
		sandboxes.ErrNotFound:                             {http.StatusNotFound, `"not_found"`},
		sandboxes.ErrBadPath:                              {http.StatusBadRequest, `"bad_request"`},
		errors.Join(sandboxes.ErrBadRequest, io.EOF):      {http.StatusBadRequest, `"bad_request"`},
		errors.New("docker: the daemon could not answer"): {http.StatusInternalServerError, `"internal"`},
	} {
		handler := newHandler(&fakeManager{err: err}, pinger{})
		got := call(t, handler, http.MethodGet, "/v1/sandboxes/box-1/files?path=/x", "", true)
		if got.Code != want.status || !strings.Contains(got.Body.String(), want.code) {
			t.Errorf("%v: %d %s", err, got.Code, got.Body.String())
		}
	}
}

func TestCommandsAndFiles(t *testing.T) {
	manager := &fakeManager{file: "hello\x00"}
	handler := newHandler(manager, pinger{})
	got := call(t, handler, http.MethodPost, "/v1/sandboxes/box-1/commands", `{"command":"echo hi","timeoutMs":90000}`, true)
	if got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != `{"output":"hi\n","exitCode":2,"timedOut":false,"truncated":true}` {
		t.Errorf("command: %d %s", got.Code, got.Body.String())
	}
	if manager.command != "box-1: echo hi" || manager.limit != 90*time.Second {
		t.Errorf("ran %q within %s", manager.command, manager.limit)
	}
	if got := call(t, handler, http.MethodPost, "/v1/sandboxes/box-1/commands", `{"command":"echo hi"}`, true); got.Code != http.StatusBadRequest {
		t.Errorf("a command without a limit: %d", got.Code)
	}

	got = call(t, handler, http.MethodGet, "/v1/sandboxes/box-1/files?path=/workspace/a", "", true)
	if got.Code != http.StatusOK || got.Body.String() != "hello\x00" || got.Header().Get("Content-Length") != "6" {
		t.Errorf("read: %d %q %v", got.Code, got.Body.String(), got.Header())
	}
	if got := call(t, handler, http.MethodPut, "/v1/sandboxes/box-1/files?path=/workspace/b", "data", true); got.Code != http.StatusNoContent || manager.written != "box-1 /workspace/b data" {
		t.Errorf("write: %d, wrote %q", got.Code, manager.written)
	}
	if got := call(t, handler, http.MethodDelete, "/v1/sandboxes/thread/7", "", true); got.Code != http.StatusNoContent || manager.released != "thread/7" {
		t.Errorf("release: %d, released %q", got.Code, manager.released)
	}
	if got := call(t, handler, http.MethodDelete, "/v1/sandboxes/job/deliverable-job-1-attempt-1", "", true); got.Code != http.StatusBadRequest {
		t.Errorf("release of the retired job kind: %d", got.Code)
	}
	if got := call(t, handler, http.MethodDelete, "/v1/sandboxes/box/1", "", true); got.Code != http.StatusBadRequest {
		t.Errorf("release of an unknown kind: %d", got.Code)
	}
}
