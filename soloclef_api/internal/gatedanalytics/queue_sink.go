package gatedanalytics

import (
	"log/slog"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/posthogcapture"
)

// PostHogSink is the gated-request event's own sink: it names the event and
// renders its properties, and the queue underneath it posts the batch
// (internal/posthogcapture, which every event of this tier shares).
type PostHogSink struct {
	*posthogcapture.Queue
}

// NewPostHogSink starts the queue. It is only built when this deployment has a
// project key, so an installation with telemetry off has no queue at all.
func NewPostHogSink(apiKey, host string, logger *slog.Logger) *PostHogSink {
	return &PostHogSink{Queue: posthogcapture.NewQueue(apiKey, host, logger)}
}

// Record queues one gated request. It never blocks and never fails a request.
func (s *PostHogSink) Record(event Event) {
	s.Capture(posthogcapture.Captured{
		Name:       EventName,
		Account:    event.Account,
		Properties: Properties(event),
	})
}
