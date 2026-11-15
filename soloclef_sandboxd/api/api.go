// Package api is the service's HTTP face, the one the API and the worker
// call (soloclef_studio/sandbox is the client). It is reachable only on the
// stack's sandbox network and every route but /health needs the key.
//
//	POST   /v1/sandboxes                     find or create a thread's sandbox,
//	                                         waiting up to waitSeconds for a place
//	DELETE /v1/sandboxes/{kind}/{key}        remove it
//	POST   /v1/sandboxes/{id}/commands       run a command and wait for it
//	GET    /v1/sandboxes/{id}/files?path=    read a file (Content-Length is its size)
//	PUT    /v1/sandboxes/{id}/files?path=    write a file
//	GET    /health                           the daemon answers
//
// Errors are {"error": code, "message": text}, with the code one of
// bad_request, unauthorized, account_full (429), gone (410: the sandbox no
// longer exists), not_found (404: the file does not), internal.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_sandboxd/sandboxes"
)

// Manager is what the routes need of the sandboxes (*sandboxes.Manager).
type Manager interface {
	Acquire(ctx context.Context, request sandboxes.Request) (string, bool, error)
	Release(ctx context.Context, kind sandboxes.Kind, key string) error
	Run(ctx context.Context, id, command string, limit time.Duration) (sandboxes.Result, error)
	ReadFile(ctx context.Context, id, name string, open func(int64) (io.Writer, error)) error
	WriteFile(ctx context.Context, id, name string, data io.Reader) error
}

// Pinger answers whether Docker is reachable (*docker.Client).
type Pinger interface {
	Ping(ctx context.Context) error
}

// Handler returns the routes, guarded by key.
func Handler(manager Manager, docker Pinger, key string, log *slog.Logger) http.Handler {
	routes := &routes{manager: manager, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sandboxes", routes.acquire)
	mux.HandleFunc("DELETE /v1/sandboxes/{kind}/{key}", routes.release)
	mux.HandleFunc("POST /v1/sandboxes/{id}/commands", routes.run)
	mux.HandleFunc("GET /v1/sandboxes/{id}/files", routes.readFile)
	mux.HandleFunc("PUT /v1/sandboxes/{id}/files", routes.writeFile)

	guarded := requireKey(key, mux)
	outer := http.NewServeMux()
	outer.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := docker.Ping(ctx); err != nil {
			fail(w, http.StatusServiceUnavailable, "internal", "Docker does not answer: "+err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	outer.Handle("/", guarded)
	return outer
}

// requireKey lets through only requests bearing the key, compared in
// constant time.
func requireKey(key string, next http.Handler) http.Handler {
	want := []byte("Bearer " + key)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			fail(w, http.StatusUnauthorized, "unauthorized", "the key is missing or wrong")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type routes struct {
	manager Manager
	log     *slog.Logger
}

// AcquireRequest is the body of POST /v1/sandboxes. CPU and memory keep the
// settings' spelling ("2", "6Gi") and are read here, so a deployment's
// values mean what they always meant.
type AcquireRequest struct {
	Kind           string `json:"kind"`
	Key            string `json:"key"`
	Account        string `json:"account"`
	CPU            string `json:"cpu"`
	Memory         string `json:"memory"`
	IdleTTLSeconds int64  `json:"idleTtlSeconds"`
	MaxPerAccount  int    `json:"maxPerAccount"`
	// WaitSeconds is how long the call may wait for a place in the
	// account's limit; absent or 0 answers account_full at once. The call
	// is held open while it waits, and a caller that hangs up leaves the
	// queue.
	WaitSeconds int64 `json:"waitSeconds"`
}

// AcquireAnswer is its answer.
type AcquireAnswer struct {
	ID      string `json:"id"`
	Created bool   `json:"created"`
}

func (s *routes) acquire(w http.ResponseWriter, r *http.Request) {
	var body AcquireRequest
	if !decode(w, r, &body) {
		return
	}
	cpu, err := sandboxes.ParseCPU(body.CPU)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	memory, err := sandboxes.ParseMemory(body.Memory)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	// The idle time to live has no upper bound of its own (the manager
	// checks its lower one), only what a duration can hold; the wait is
	// bounded by MaximumWait, here in whole seconds as the body counts it.
	idleTTL, err := durationOf("idleTtlSeconds", body.IdleTTLSeconds, time.Second, maxWholeUnits(time.Second))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	maxWait, err := durationOf("waitSeconds", body.WaitSeconds, time.Second, int64(sandboxes.MaximumWait/time.Second))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	id, created, err := s.manager.Acquire(r.Context(), sandboxes.Request{
		Kind: sandboxes.Kind(body.Kind), Key: body.Key, Account: body.Account,
		NanoCPUs: cpu, MemoryBytes: memory, IdleTTL: idleTTL,
		MaxPerAccount: body.MaxPerAccount, MaxWait: maxWait,
	})
	if err != nil {
		s.failWith(w, err)
		return
	}
	answer(w, AcquireAnswer{ID: id, Created: created})
}

func (s *routes) release(w http.ResponseWriter, r *http.Request) {
	kind := sandboxes.Kind(r.PathValue("kind"))
	if kind != sandboxes.Thread {
		fail(w, http.StatusBadRequest, "bad_request", "kind is thread")
		return
	}
	if err := s.manager.Release(r.Context(), kind, r.PathValue("key")); err != nil {
		s.failWith(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CommandRequest is the body of POST /v1/sandboxes/{id}/commands.
type CommandRequest struct {
	Command   string `json:"command"`
	TimeoutMS int64  `json:"timeoutMs"`
}

func (s *routes) run(w http.ResponseWriter, r *http.Request) {
	var body CommandRequest
	if !decode(w, r, &body) {
		return
	}
	if body.TimeoutMS <= 0 {
		fail(w, http.StatusBadRequest, "bad_request", "a command needs its time limit (timeoutMs)")
		return
	}
	limit, err := durationOf("timeoutMs", body.TimeoutMS, time.Millisecond, maxWholeUnits(time.Millisecond))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	// The request's context is the command's: a caller that hangs up stops
	// its command (sandboxes.Manager.Run).
	result, err := s.manager.Run(r.Context(), r.PathValue("id"), body.Command, limit)
	if err != nil {
		s.failWith(w, err)
		return
	}
	answer(w, result)
}

func (s *routes) readFile(w http.ResponseWriter, r *http.Request) {
	err := s.manager.ReadFile(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"), func(size int64) (io.Writer, error) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.WriteHeader(http.StatusOK)
		return w, nil
	})
	if err != nil {
		// Once the file's bytes have started, the status is sent; a
		// failure then can only cut the body short, which the client sees
		// as fewer bytes than Content-Length promised.
		if w.Header().Get("Content-Length") == "" {
			s.failWith(w, err)
		} else {
			s.log.Warn("sandbox: a file read ended early", "error", err)
		}
	}
}

func (s *routes) writeFile(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.WriteFile(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"), r.Body); err != nil {
		s.failWith(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// failWith answers a manager's error with its code.
func (s *routes) failWith(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sandboxes.ErrGone):
		fail(w, http.StatusGone, "gone", err.Error())
	case errors.Is(err, sandboxes.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, sandboxes.ErrAccountFull):
		fail(w, http.StatusTooManyRequests, "account_full", err.Error())
	case errors.Is(err, sandboxes.ErrBadPath), errors.Is(err, sandboxes.ErrBadRequest):
		fail(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, context.Canceled):
		// The caller hung up; nobody reads this answer.
		s.log.Info("sandbox: a call ended because its caller went away")
	default:
		s.log.Error("sandbox: a call failed", "error", err)
		fail(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// maxBodyBytes bounds a JSON body: a command, which a model wrote, is the
// largest, and a megabyte of shell is far past any real one.
const maxBodyBytes = 1 << 20

func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "the body is not the expected JSON: "+err.Error())
		return false
	}
	return true
}

func answer(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": message})
}
