package humawriter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
