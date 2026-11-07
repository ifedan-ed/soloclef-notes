package errorenvelope

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWriteMatchesThePythonEnvelope(t *testing.T) {
	writer := Writer{
		ReportURL: "https://notes.example.com/contact",
		Now: func() time.Time {
			return time.Date(2026, 11, 7, 9, 30, 0, 123456000, time.UTC)
		},
	}
	recorder := httptest.NewRecorder()

	writer.Write(recorder, http.StatusUnauthorized, "Unauthorized", "req_0123456789ab")

	const want = `{"error":{"code":"UNAUTHORIZED","message":"Unauthorized","status":401,` +
		`"request_id":"req_0123456789ab","timestamp":"2026-11-07T09:30:00.123456+00:00",` +
		`"report_url":"https://notes.example.com/contact"},"detail":"Unauthorized"}`
	if got := recorder.Body.String(); got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content type: got %q", got)
	}
	if got := recorder.Header().Get("X-Request-ID"); got != "req_0123456789ab" {
		t.Errorf("request id header: got %q", got)
	}
}

func TestCodeForStatusMatchesTheMapping(t *testing.T) {
	for status, want := range map[int]string{
		400: "BAD_REQUEST", 401: "UNAUTHORIZED", 403: "FORBIDDEN", 404: "NOT_FOUND",
		405: "METHOD_NOT_ALLOWED", 409: "CONFLICT", 422: "VALIDATION_ERROR",
		429: "RATE_LIMIT_EXCEEDED", 418: "CLIENT_ERROR", 503: "INTERNAL_ERROR",
	} {
		if got := CodeForStatus(status); got != want {
			t.Errorf("%d: got %q, want %q", status, got, want)
		}
	}
}
