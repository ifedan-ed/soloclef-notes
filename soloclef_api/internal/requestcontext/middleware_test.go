package requestcontext

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// connection stands in for net/http's own writer: its deadline can be moved
// and it can take a reader. It records both, so a test can see what reached
// the bottom of the chain.
type connection struct {
	*httptest.ResponseRecorder
	deadlines int
	handed    io.Reader
}

func (c *connection) SetWriteDeadline(time.Time) error {
	c.deadlines++
	return nil
}

func (c *connection) ReadFrom(source io.Reader) (int64, error) {
	c.handed = source
	return io.Copy(c.ResponseRecorder, source)
}

// served runs one request through the middleware and returns its access log.
func served(response http.ResponseWriter, request *http.Request, handler http.HandlerFunc) string {
	var log bytes.Buffer
	Middleware(slog.New(slog.NewTextHandler(&log, nil)), handler).ServeHTTP(response, request)
	return log.String()
}

// This middleware wraps every response first, so if the connection cannot be
// reached through it, it cannot be reached at all -- and a route that streams
// a file could not move the write deadline (internal/rollingdeadline).
func TestTheConnectionsDeadlineIsReachableThroughTheRecorder(t *testing.T) {
	bottom := &connection{ResponseRecorder: httptest.NewRecorder()}
	served(bottom, httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil),
		func(response http.ResponseWriter, _ *http.Request) {
			if err := http.NewResponseController(response).SetWriteDeadline(time.Now().Add(time.Minute)); err != nil {
				t.Errorf("setting the deadline through the recorder: %v", err)
			}
		})
	if bottom.deadlines != 1 {
		t.Errorf("%d deadlines reached the connection, want 1", bottom.deadlines)
	}
}

// A copied body is handed to the writer underneath as the reader it was, so
// a file can still be sent by the kernel -- and the status is still recorded.
func TestACopiedBodyIsHandedDownAndItsStatusRecorded(t *testing.T) {
	bottom := &connection{ResponseRecorder: httptest.NewRecorder()}
	source := strings.NewReader("the body")
	// What io.Copy hands a destination with ReadFrom is the source without
	// its own WriteTo; this is that source.
	plain := struct{ io.Reader }{source}

	log := served(bottom, httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil),
		func(response http.ResponseWriter, _ *http.Request) {
			if _, err := io.Copy(response, plain); err != nil {
				t.Error(err)
			}
		})
	if bottom.handed != io.Reader(plain) {
		t.Errorf("the writer underneath was handed %T, not the reader itself", bottom.handed)
	}
	if bottom.Body.String() != "the body" {
		t.Errorf("body %q", bottom.Body.String())
	}
	// A body with no WriteHeader is a 200, as it is through Write.
	if !strings.Contains(log, "status=200") {
		t.Errorf("access line: %s", log)
	}
}

// A writer underneath that cannot take a reader still gets the bytes.
func TestACopiedBodyReachesAWriterThatCannotTakeAReader(t *testing.T) {
	recorder := httptest.NewRecorder()
	served(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil),
		func(response http.ResponseWriter, _ *http.Request) {
			if _, err := response.(io.ReaderFrom).ReadFrom(strings.NewReader("the body")); err != nil {
				t.Error(err)
			}
		})
	if recorder.Body.String() != "the body" {
		t.Errorf("body %q", recorder.Body.String())
	}
}

// What the access line says of a request nothing was written for.
func TestARequestThatWasNeverAnswered(t *testing.T) {
	// A handler that returns without writing is a fault: 500, as a warning.
	log := served(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil),
		func(http.ResponseWriter, *http.Request) {})
	if !strings.Contains(log, "level=WARN") || !strings.Contains(log, "status=500") {
		t.Errorf("a silent handler: %s", log)
	}

	// Unless there was nobody to write to: the client closed the connection,
	// which is what cancels a request's context. That is not this server's
	// failure and is not logged as one.
	gone, leave := context.WithCancel(context.Background())
	leave()
	log = served(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil).WithContext(gone),
		func(http.ResponseWriter, *http.Request) {})
	if !strings.Contains(log, "level=INFO") || !strings.Contains(log, "status=499") {
		t.Errorf("a client that left: %s", log)
	}

	// A client that left *after* its answer was written got that answer.
	log = served(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil).WithContext(gone),
		func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusPartialContent) })
	if !strings.Contains(log, "status=206") {
		t.Errorf("a client that left mid-body: %s", log)
	}
}
