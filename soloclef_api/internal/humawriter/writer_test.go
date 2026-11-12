package humawriter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// The adapter decides the status, the headers and the framing of every
// response this tier writes, so it is tested through a real Huma operation
// rather than a stand-in context.
func TestAHandlersStatusHeadersAndBodyReachTheClient(t *testing.T) {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	huma.Register(api, huma.Operation{
		OperationID: "write",
		Method:      http.MethodGet,
		Path:        "/write",
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			response := New(ctx)
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("X-Request-ID", "req_0123456789ab")
			response.Header().Add("Vary", "Origin")
			response.Header().Add("Vary", "Accept-Encoding")
			response.WriteHeader(http.StatusForbidden)
			_, _ = response.Write([]byte(`{"detail":"no"}`))
		}}, nil
	})

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/write", nil))

	if recorder.Code != http.StatusForbidden {
		t.Errorf("status: got %d", recorder.Code)
	}
	if got := recorder.Body.String(); got != `{"detail":"no"}` {
		t.Errorf("body: got %s", got)
	}
	if got := recorder.Header().Get("X-Request-ID"); got != "req_0123456789ab" {
		t.Errorf("header: got %q", got)
	}
	if got := recorder.Header().Values("Vary"); len(got) != 2 {
		t.Errorf("both Vary values must survive, got %v", got)
	}
}

func TestWritingWithoutAStatusIsTwoHundred(t *testing.T) {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	huma.Register(api, huma.Operation{
		OperationID: "implicit",
		Method:      http.MethodGet,
		Path:        "/implicit",
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			_, _ = New(ctx).Write([]byte("ok"))
		}}, nil
	})

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/implicit", nil))

	if recorder.Code != http.StatusOK {
		t.Errorf("status: got %d", recorder.Code)
	}
}

// connection stands in for net/http's own writer: its deadline can be moved
// and it can take a reader. It records both.
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

// through runs body as a streaming Huma operation whose response is written
// to bottom.
func through(t *testing.T, bottom http.ResponseWriter, body func(response http.ResponseWriter)) {
	t.Helper()
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	huma.Register(api, huma.Operation{
		OperationID: "stream",
		Method:      http.MethodGet,
		Path:        "/stream",
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) { body(New(ctx)) }}, nil
	})
	mux.ServeHTTP(bottom, httptest.NewRequest(http.MethodGet, "/stream", nil))
}

// A route that streams a file moves the connection's write deadline as it
// goes (internal/rollingdeadline). It can only do that if the connection is
// reachable through this adapter; when it was not, every stream was cut at
// the server's write timeout.
func TestTheConnectionsDeadlineIsReachableThroughTheAdapter(t *testing.T) {
	bottom := &connection{ResponseRecorder: httptest.NewRecorder()}
	through(t, bottom, func(response http.ResponseWriter) {
		if err := http.NewResponseController(response).SetWriteDeadline(time.Now().Add(time.Minute)); err != nil {
			t.Errorf("setting the deadline through the adapter: %v", err)
		}
	})
	if bottom.deadlines != 1 {
		t.Errorf("%d deadlines reached the connection, want 1", bottom.deadlines)
	}
}

// And a writer with no connection under it says so, rather than pretending.
func TestAWriterWithNoConnectionReportsThatDeadlinesAreNotSupported(t *testing.T) {
	through(t, httptest.NewRecorder(), func(response http.ResponseWriter) {
		err := http.NewResponseController(response).SetWriteDeadline(time.Now().Add(time.Minute))
		if !errors.Is(err, http.ErrNotSupported) {
			t.Errorf("got %v, want not supported", err)
		}
	})
}

// A copied body starts the response as Write does -- status and collected
// headers first -- and reaches the writer underneath as the reader it was,
// which is what lets net/http send a file from the kernel.
func TestACopiedBodyStartsTheResponseAndIsHandedDown(t *testing.T) {
	bottom := &connection{ResponseRecorder: httptest.NewRecorder()}
	// What io.Copy hands a destination with ReadFrom: a reader and nothing
	// else.
	source := struct{ io.Reader }{strings.NewReader("the film")}
	through(t, bottom, func(response http.ResponseWriter) {
		response.Header().Set("Content-Type", "video/mp4")
		response.Header().Set("Content-Range", "bytes 0-7/8")
		response.WriteHeader(http.StatusPartialContent)
		if sent, err := io.Copy(response, source); err != nil || sent != 8 {
			t.Errorf("sent %d, %v", sent, err)
		}
	})
	if bottom.Code != http.StatusPartialContent || bottom.Header().Get("Content-Range") != "bytes 0-7/8" {
		t.Errorf("status %d, headers %v", bottom.Code, bottom.Header())
	}
	if bottom.handed != io.Reader(source) {
		t.Errorf("the writer underneath was handed %T, not the reader itself", bottom.handed)
	}
	if bottom.Body.String() != "the film" {
		t.Errorf("body %q", bottom.Body.String())
	}
}

// With no status written first, a copied body is a 200 with its headers; and
// a writer underneath that cannot take a reader still gets the bytes.
func TestACopiedBodyWithNoStatusIntoAPlainWriter(t *testing.T) {
	recorder := httptest.NewRecorder()
	through(t, recorder, func(response http.ResponseWriter) {
		response.Header().Set("Content-Type", "audio/mpeg")
		if _, err := response.(io.ReaderFrom).ReadFrom(strings.NewReader("narration")); err != nil {
			t.Error(err)
		}
	})
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "audio/mpeg" ||
		recorder.Body.String() != "narration" {
		t.Errorf("status %d, headers %v, body %q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}
