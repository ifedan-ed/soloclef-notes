package gatedanalytics

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/posthogcapture"
)

// The body is compared against what posthog-python put on the wire for the same
// event, captured from the running Python tier against a stand-in for PostHog.
// Only uuid and the two times are free: posthog-python makes a fresh uuid per
// event and stamps the moment it sent the batch.
func TestTheBatchIsTheOneThePythonLibraryPosts(t *testing.T) {
	sent := make(chan struct {
		path  string
		body  []byte
		agent string
	}, 1)
	posthog := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		sent <- struct {
			path  string
			body  []byte
			agent string
		}{request.URL.Path, body, request.Header.Get("User-Agent")}
		_, _ = response.Write([]byte(`{"status": 1}`))
	}))
	defer posthog.Close()

	sink := NewPostHogSink("phc_a_project_key", posthog.URL,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	sink.Now = func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) }
	sink.Record(Event{
		Account:    "aaaaaaaa-0000-4000-8000-000000000001",
		Route:      "/api/v1/notebooks/{notebook_id}/feature",
		Method:     http.MethodGet,
		Status:     http.StatusOK,
		AuthMethod: "agent",
		Client:     "mcp",
	})
	sink.Close()

	posted := <-sent
	if posted.path != posthogcapture.CapturePath {
		t.Errorf("posted to %q, want %q", posted.path, posthogcapture.CapturePath)
	}
	if posted.agent != posthogcapture.LibraryName {
		t.Errorf("user-agent %q, want %q", posted.agent, posthogcapture.LibraryName)
	}

	var envelope struct {
		APIKey              string `json:"api_key"`
		HistoricalMigration bool   `json:"historical_migration"`
		SentAt              string `json:"sent_at"`
		Batch               []struct {
			Event      string         `json:"event"`
			DistinctID string         `json:"distinct_id"`
			Timestamp  string         `json:"timestamp"`
			UUID       string         `json:"uuid"`
			Properties map[string]any `json:"properties"`
		} `json:"batch"`
	}
	if err := json.Unmarshal(posted.body, &envelope); err != nil {
		t.Fatalf("the batch is not JSON: %v", err)
	}
	if envelope.APIKey != "phc_a_project_key" || envelope.HistoricalMigration {
		t.Errorf("envelope: key %q, historical %v", envelope.APIKey, envelope.HistoricalMigration)
	}
	if envelope.SentAt != "2026-11-08T12:00:00+00:00" {
		t.Errorf("sent_at %q", envelope.SentAt)
	}
	if len(envelope.Batch) != 1 {
		t.Fatalf("%d events in the batch, want 1", len(envelope.Batch))
	}

	event := envelope.Batch[0]
	if event.Event != EventName {
		t.Errorf("event %q, want %q", event.Event, EventName)
	}
	if event.DistinctID != "aaaaaaaa-0000-4000-8000-000000000001" {
		t.Errorf("distinct_id %q", event.DistinctID)
	}
	if len(event.UUID) != 36 {
		t.Errorf("uuid %q is not a uuid", event.UUID)
	}
	// These are the properties a PostHog query spans both tiers with.
	for name, want := range map[string]any{
		"source":         "backend",
		"auth_method":    "agent",
		"client":         "mcp",
		"route":          "/api/v1/notebooks/{notebook_id}/feature",
		"method":         "GET",
		"status_code":    float64(200),
		"$geoip_disable": true,
		"$is_server":     true,
	} {
		if got := event.Properties[name]; got != want {
			t.Errorf("property %s: got %v, want %v", name, got, want)
		}
	}
}

// An event is never allowed to wait for PostHog, and never allowed to fail the
// request that produced it.
func TestAnUnreachablePostHogCostsNothingButTheEvent(t *testing.T) {
	sink := NewPostHogSink("phc_a_project_key", "http://127.0.0.1:1",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	for attempt := 0; attempt < 10; attempt++ {
		sink.Record(Event{Account: "account", Route: "/x", Method: "GET", Status: 200})
	}
	sink.Close()
}
