package livechanges

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

const (
	alice = "11111111-1111-4111-8111-111111111111"
	bob   = "22222222-2222-4222-8222-222222222222"
)

// streamServer is the route on a real listener (a stream needs a real
// connection: its write deadline is moved on every frame). The test names
// the caller in two headers, standing in for the gate, which is cmd/api's
// and tested there.
func streamServer(t *testing.T, hub *Hub, heartbeat, life time.Duration) *httptest.Server {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Route{Hub: hub, Lifetime: context.Background(), Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stall: 5 * time.Second, Heartbeat: heartbeat, MaxLife: life}.Register(api)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who := principal.Principal{UserID: r.Header.Get("X-Test-User"), Method: principal.Method(r.Header.Get("X-Test-Method"))}
		api.Adapter().ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), who)))
	}))
	t.Cleanup(server.Close)
	return server
}

type stream struct {
	response *http.Response
	lines    chan string
	cancel   context.CancelFunc
}

func open(t *testing.T, server *httptest.Server, user string, method principal.Method, lastEventID string) *stream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+Path, nil)
	request.Header.Set("X-Test-User", user)
	request.Header.Set("X-Test-Method", string(method))
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	s := &stream{response: response, lines: make(chan string, 100), cancel: cancel}
	go func() {
		defer close(s.lines)
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			s.lines <- scanner.Text()
		}
	}()
	t.Cleanup(func() { cancel(); response.Body.Close() })
	return s
}

// next is the next line that is not blank, or "" when the stream ended or
// nothing came in time.
func (s *stream) next(t *testing.T) string {
	t.Helper()
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				return ""
			}
			if line != "" {
				return line
			}
		case <-time.After(3 * time.Second):
			t.Fatal("nothing arrived on the stream")
		}
	}
}

// waitListening waits until the hub has the stream, so a change delivered
// next is one the stream is open for.
func waitListening(t *testing.T, hub *Hub, n int) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); hub.Listening() != n; {
		if time.Now().After(deadline) {
			t.Fatalf("listening %d, want %d", hub.Listening(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestASessionGetsAStreamThatOpensWithReady(t *testing.T) {
	hub := NewHub(nil)
	s := open(t, streamServer(t, hub, time.Minute, time.Minute), alice, principal.MethodSession, "")
	if s.response.StatusCode != http.StatusOK {
		t.Fatalf("status %d", s.response.StatusCode)
	}
	for header, want := range map[string]string{
		"Content-Type": "text/event-stream", "Cache-Control": "no-store", "X-Accel-Buffering": "no",
	} {
		if got := s.response.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if got := s.next(t); got != `data: {"type":"ready","resumed":false}` {
		t.Fatalf("first frame %q", got)
	}
}

func TestAnAPIKeyIsRefused(t *testing.T) {
	hub := NewHub(nil)
	server := streamServer(t, hub, time.Minute, time.Minute)
	for _, method := range []principal.Method{principal.MethodPersonalAccessToken} {
		s := open(t, server, alice, method, "")
		if s.response.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status %d", method, s.response.StatusCode)
		}
	}
	if hub.Listening() != 0 {
		t.Fatal("a refused caller was subscribed")
	}
}

func TestOneAccountNeverHearsAnothersChanges(t *testing.T) {
	hub := NewHub(nil)
	server := streamServer(t, hub, time.Minute, time.Minute)
	a := open(t, server, alice, principal.MethodSession, "")
	b := open(t, server, bob, principal.MethodSession, "")
	a.next(t)
	b.next(t)
	waitListening(t, hub, 2)

	hub.Deliver(livechange.Message{UserID: bob, Changes: []livechange.Change{{Kind: livechange.Inbox}}})
	hub.Deliver(livechange.Message{UserID: alice, Changes: []livechange.Change{{Kind: livechange.Documents, ID: 9}}})

	if id := a.next(t); !strings.HasPrefix(id, "id: ") {
		t.Fatalf("alice: %q", id)
	}
	if got := a.next(t); got != `data: {"type":"documents","id":9}` {
		t.Fatalf("alice heard %q", got)
	}
	b.next(t) // bob's own id line
	if got := b.next(t); got != `data: {"type":"inbox"}` {
		t.Fatalf("bob heard %q", got)
	}
	select {
	case line := <-a.lines:
		if line != "" {
			t.Fatalf("alice heard more: %q", line)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAnIdleStreamHasAHeartbeat(t *testing.T) {
	hub := NewHub(nil)
	s := open(t, streamServer(t, hub, 30*time.Millisecond, time.Minute), alice, principal.MethodSession, "")
	s.next(t)
	if got := s.next(t); got != ": keepalive" {
		t.Fatalf("got %q", got)
	}
}

func TestAReconnectWithLastEventIDResumes(t *testing.T) {
	hub := NewHub(nil)
	server := streamServer(t, hub, time.Minute, time.Minute)
	first := open(t, server, alice, principal.MethodSession, "")
	first.next(t)
	waitListening(t, hub, 1)
	hub.Deliver(livechange.Message{UserID: alice, Changes: []livechange.Change{{Kind: livechange.Documents}}})
	lastID := strings.TrimPrefix(first.next(t), "id: ")
	first.next(t)
	first.cancel()
	waitListening(t, hub, 0)

	hub.Deliver(livechange.Message{UserID: alice, Changes: []livechange.Change{{Kind: livechange.Outputs, NotebookID: 4}}})

	again := open(t, server, alice, principal.MethodSession, lastID)
	if got := again.next(t); got != `data: {"type":"ready","resumed":true}` {
		t.Fatalf("ready %q", got)
	}
	again.next(t)
	if got := again.next(t); got != `data: {"type":"outputs","notebook_id":4}` {
		t.Fatalf("replayed %q", got)
	}

	fresh := open(t, server, alice, principal.MethodSession, "someone-else-1")
	if got := fresh.next(t); got != `data: {"type":"ready","resumed":false}` {
		t.Fatalf("a foreign id resumed: %q", got)
	}
}

func TestAStreamEndsAtItsMaxLifeAndOnAGap(t *testing.T) {
	hub := NewHub(nil)
	short := open(t, streamServer(t, hub, time.Minute, 50*time.Millisecond), alice, principal.MethodSession, "")
	short.next(t)
	if got := short.next(t); got != "" {
		t.Fatalf("the stream outlived its life: %q", got)
	}

	gapped := open(t, streamServer(t, hub, time.Minute, time.Minute), alice, principal.MethodSession, "")
	gapped.next(t)
	waitListening(t, hub, 1)
	hub.Gap()
	if got := gapped.next(t); got != "" {
		t.Fatalf("the stream outlived a gap: %q", got)
	}
	waitListening(t, hub, 0)
}
