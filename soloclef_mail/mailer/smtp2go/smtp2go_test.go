package smtp2go

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
)

// Every test here runs against a stand-in for the API on a local port. The
// real one is never called: a test that sends a real email is a test that
// mails a stranger.

const accepted = `{"request_id":"x","data":{"succeeded":1,"failed":0,"failures":[],"email_id":"e"}}`

func standIn(t *testing.T, handler http.HandlerFunc) (*Mailer, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	m, err := New(Settings{
		APIKey:  "stand-in-key",
		From:    "notes@queue.test",
		Timeout: 5 * time.Second,
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m, server
}

func aMessage() mailer.Message {
	return mailer.Message{To: "person@queue.test", Subject: "s", TextBody: "t"}
}

func TestAnAcceptedMessageIsSentOnceToTheOneEndpoint(t *testing.T) {
	var method, path, key, contentType string
	var calls int32
	m, _ := standIn(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		method, path = r.Method, r.URL.Path
		key = r.Header.Get("X-Smtp2go-Api-Key")
		contentType = r.Header.Get("Content-Type")
		io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, accepted)
	})

	if err := m.Send(context.Background(), aMessage()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("one send made %d requests", calls)
	}
	if method != http.MethodPost || path != Path {
		t.Errorf("called %s %s", method, path)
	}
	if key != "stand-in-key" {
		t.Errorf("the key went up as %q", key)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type %q", contentType)
	}
}

func TestEachAnswerIsTheFailureTheQueueActsOn(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"rate limited", http.StatusTooManyRequests, `{"data":{"error_code":"E_ApiResponseCodes.RATE_LIMIT"}}`, mailer.ErrUnavailable},
		{"provider down", http.StatusBadGateway, "upstream", mailer.ErrUnavailable},
		{"server error", http.StatusInternalServerError, "", mailer.ErrUnavailable},
		{"key refused", http.StatusUnauthorized, `{"data":{"error_code":"E_ApiResponseCodes.AUTHENTICATION_FAILED"}}`, mailer.ErrMisconfigured},
		{"key forbidden", http.StatusForbidden, "", mailer.ErrMisconfigured},
		{"message refused", http.StatusBadRequest, `{"data":{"error_code":"E_ApiResponseCodes.ENDPOINT_PERMISSION"}}`, mailer.ErrRejected},
		{"not found", http.StatusNotFound, "", mailer.ErrRejected},
		// A 200 whose body says the recipient failed. The request worked and
		// the mail did not: reading the status alone would report this sent.
		{"accepted but failed", http.StatusOK, `{"data":{"succeeded":0,"failed":1,"failures":["person@queue.test: not allowed"],"error_code":"E_ApiResponseCodes.NO_RECIPIENTS"}}`, mailer.ErrRejected},
		{"accepted but one of two failed", http.StatusOK, `{"data":{"succeeded":1,"failed":1,"error_code":"E_ApiResponseCodes.PARTIAL"}}`, mailer.ErrRejected},
		{"200 from something that is not the API", http.StatusOK, "<html>hello</html>", mailer.ErrRejected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _ := standIn(t, func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.WriteHeader(c.status)
				fmt.Fprint(w, c.body)
			})
			err := m.Send(context.Background(), aMessage())
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if strings.Contains(err.Error(), "person@queue.test") {
				t.Errorf("the error quotes the recipient: %v", err)
			}
			if strings.Contains(err.Error(), "stand-in-key") {
				t.Errorf("the error quotes the key: %v", err)
			}
			if strings.Contains(err.Error(), "not allowed") {
				t.Errorf("the error quotes the provider's free text, which names addresses: %v", err)
			}
		})
	}
}

func TestAnUnreachableApiIsWorthRetrying(t *testing.T) {
	m, server := standIn(t, func(w http.ResponseWriter, r *http.Request) {})
	server.Close()

	err := m.Send(context.Background(), aMessage())
	if !errors.Is(err, mailer.ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestTheConnectionIsKeptBetweenMessages(t *testing.T) {
	// The reason this transport exists: no TCP connect and TLS handshake per
	// message, which was most of a send's time at smtp2go.
	var opened int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, accepted)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			atomic.AddInt32(&opened, 1)
		}
	}
	server.Start()
	defer server.Close()

	m, err := New(Settings{APIKey: "k", From: "notes@queue.test", Timeout: 5 * time.Second, BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	for range 10 {
		if err := m.Send(context.Background(), aMessage()); err != nil {
			t.Fatal(err)
		}
	}

	if got := atomic.LoadInt32(&opened); got != 1 {
		t.Errorf("ten messages opened %d connections; a kept-alive client opens one", got)
	}
}

func TestAnUnconfiguredTransportRefusesToStart(t *testing.T) {
	cases := map[string]Settings{
		"no key":     {From: "a@b.test", Timeout: time.Second},
		"no sender":  {APIKey: "k", Timeout: time.Second},
		"no timeout": {APIKey: "k", From: "a@b.test"},
	}
	for name, settings := range cases {
		if _, err := New(settings); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAMessageWithNoSenderGoesOutUnderTheDeploymentDefault(t *testing.T) {
	var body string
	m, _ := standIn(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		fmt.Fprint(w, accepted)
	})

	if err := m.Send(context.Background(), aMessage()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body, `{"sender":"notes@queue.test"`) {
		t.Errorf("sender is %s", body)
	}

	own := aMessage()
	own.Sender = "Soloclef Notes <licenses@queue.test>"
	if err := m.Send(context.Background(), own); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body, `{"sender":"Soloclef Notes <licenses@queue.test>"`) {
		t.Errorf("a message's own sender was dropped: %s", body)
	}
}
