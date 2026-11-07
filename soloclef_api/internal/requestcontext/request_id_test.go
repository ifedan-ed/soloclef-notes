package requestcontext

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

func TestAcceptKeepsAPlainCallerIDAndMintsOtherwise(t *testing.T) {
	if got := Accept("req_abc123"); got != "req_abc123" {
		t.Errorf("a well-formed id must be kept, got %q", got)
	}
	minted := regexp.MustCompile(`^req_[0-9a-f]{12}$`)
	for _, raw := range []string{"", "short", "has spaces", "a;b;c", string(make([]byte, 65))} {
		if got := Accept(raw); !minted.MatchString(got) {
			t.Errorf("%q: got %q, want a minted id", raw, got)
		}
	}
}

func TestMiddlewareEchoesTheIDAndHandsItToTheHandler(t *testing.T) {
	var seen string
	handler := Middleware(slog.New(slog.NewTextHandler(io.Discard, nil)),
		http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			seen = IDFrom(request)
			response.WriteHeader(http.StatusOK)
		}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/logs/workspaces/1/summary", nil)
	request.Header.Set(Header, "req_from_caller")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if seen != "req_from_caller" {
		t.Errorf("handler saw %q", seen)
	}
	if got := recorder.Header().Get(Header); got != "req_from_caller" {
		t.Errorf("response header: got %q", got)
	}
}
