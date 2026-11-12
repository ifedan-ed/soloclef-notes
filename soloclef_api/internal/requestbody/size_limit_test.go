package requestbody

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// countingBody reports how many bytes the middleware pulled from the client,
// which is what an oversized body costs the server in memory.
type countingBody struct {
	source io.Reader
	read   int64
}

func (c *countingBody) Read(buffer []byte) (int, error) {
	n, err := c.source.Read(buffer)
	c.read += int64(n)
	return n, err
}

func (c *countingBody) Close() error { return nil }

// sizedRequest is a covered request whose body is a JSON string of about
// size bytes. declared says whether it states its Content-Length; a chunked
// request does not, and must be bounded all the same.
func sizedRequest(size int64, declared bool) (*http.Request, *countingBody) {
	text := `{"a":"` + strings.Repeat("x", int(size)-8) + `"}`
	body := &countingBody{source: strings.NewReader(text)}
	request := httptest.NewRequest(http.MethodPost, "/covered", nil)
	request.Body = body
	request.Header.Set("Content-Type", "application/json")
	request.ContentLength = -1
	if declared {
		request.ContentLength = int64(len(text))
	}
	return request, body
}

// sizeHarness is the middleware in front of a route that records what it
// was given.
func sizeHarness(t *testing.T) (http.Handler, *[]byte) {
	t.Helper()
	var reached []byte
	route := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		reached = Buffered(request.Context())
		response.WriteHeader(http.StatusOK)
	})
	handler := Middleware{Covers: func(*http.Request) bool { return true }}.Wrap(route)
	return handler, &reached
}

func TestMiddlewareRefusesABodyOverTheLimitBeforeReadingIt(t *testing.T) {
	// The middleware runs before anyone is signed in, so a body it buffers
	// whole is memory any stranger can spend. One that says it is too large
	// is refused on its word, with nothing read.
	for _, declared := range []bool{true, false} {
		handler, reached := sizeHarness(t)
		request, body := sizedRequest(MaxBufferedBytes+1024, declared)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("declared=%v: status = %d, want 413", declared, recorder.Code)
		}
		if *reached != nil {
			t.Fatalf("declared=%v: an oversized body reached the route", declared)
		}
		// A chunked body is read only one byte past the limit: enough to know
		// it is over, and not the rest of whatever the client sends.
		if limit := int64(MaxBufferedBytes + 1); body.read > limit+32<<10 {
			t.Fatalf("declared=%v: read %d bytes of an oversized body, want at most about %d", declared, body.read, limit)
		}
		if declared && body.read != 0 {
			t.Fatalf("a body declared too large was read (%d bytes)", body.read)
		}
		var envelope struct {
			Error struct {
				Code   string `json:"code"`
				Status int    `json:"status"`
			} `json:"error"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Error.Status != http.StatusRequestEntityTooLarge {
			t.Fatalf("declared=%v: the refusal is not the error envelope: %s", declared, recorder.Body.String())
		}
	}
}

func TestMiddlewareStillBuffersABodyOfExactlyTheLimit(t *testing.T) {
	handler, reached := sizeHarness(t)
	request, _ := sizedRequest(MaxBufferedBytes, false)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; a body of exactly the limit must reach the route", recorder.Code)
	}
	if int64(len(*reached)) != MaxBufferedBytes || !bytes.HasPrefix(*reached, []byte(`{"a":"x`)) {
		t.Fatalf("the route was given %d bytes, want %d", len(*reached), MaxBufferedBytes)
	}
}
