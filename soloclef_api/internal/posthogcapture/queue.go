package posthogcapture

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// The consumer's shape, which is posthog-python's.
const (
	queueDepth  = 1000
	batchSize   = 100
	flushEvery  = 500 * time.Millisecond
	sendTimeout = 10 * time.Second
)

// CapturePath is where posthog-python's consumer posts a batch.
const CapturePath = "/batch/"

// Queue holds events until one background worker posts them, exactly where
// posthog-python posts them: {host}/batch/, as JSON, with the project key in
// the envelope.
type Queue struct {
	apiKey string
	host   string
	client *http.Client
	logger *slog.Logger
	queue  chan Captured
	done   chan struct{}
	// Now is overridable for tests only.
	Now func() time.Time
}

// NewQueue starts the background worker. It is only built when this deployment
// has a project key, so an installation with telemetry off has no queue and no
// worker at all.
func NewQueue(apiKey, host string, logger *slog.Logger) *Queue {
	queue := &Queue{
		apiKey: apiKey,
		host:   host,
		client: &http.Client{Timeout: sendTimeout},
		logger: logger,
		queue:  make(chan Captured, queueDepth),
		done:   make(chan struct{}),
	}
	go queue.run()
	return queue
}

// Capture queues an event. It never blocks and never fails a request: a full
// queue drops the event, which is the same trade posthog-python makes.
func (q *Queue) Capture(event Captured) {
	select {
	case q.queue <- event:
	default:
		q.logger.Warn("dropped a product event; the analytics queue is full",
			slog.String("event", event.Name))
	}
}

// Close flushes what is queued and stops the worker.
func (q *Queue) Close() {
	close(q.queue)
	<-q.done
}

func (q *Queue) run() {
	defer close(q.done)
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()

	batch := make([]Captured, 0, batchSize)
	for {
		select {
		case event, open := <-q.queue:
			if !open {
				q.send(batch)
				return
			}
			batch = append(batch, event)
			if len(batch) >= batchSize {
				q.send(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				q.send(batch)
				batch = batch[:0]
			}
		}
	}
}

func (q *Queue) send(batch []Captured) {
	if len(batch) == 0 {
		return
	}
	body, err := pythonjson.Marshal(q.Envelope(batch))
	if err != nil {
		q.logger.Warn("could not render the product events", slog.String("error", err.Error()))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, q.host+CapturePath, bytes.NewReader(body))
	if err != nil {
		q.logger.Warn("could not post the product events", slog.String("error", err.Error()))
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", LibraryName)

	response, err := q.client.Do(request)
	if err != nil {
		q.logger.Warn("could not reach PostHog", slog.String("error", err.Error()))
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		q.logger.Warn("PostHog refused the product events", slog.Int("status", response.StatusCode))
	}
}

// Envelope is the body posthog-python's consumer posts: the batch, the project
// key, and the two fields it always sends beside them.
func (q *Queue) Envelope(batch []Captured) *pythonjson.Object {
	now := time.Now
	if q.Now != nil {
		now = q.Now
	}
	sentAt := now().UTC()

	events := make([]any, 0, len(batch))
	for _, event := range batch {
		captured := pythonjson.NewObject()
		captured.Set("properties", event.Properties)
		captured.Set("timestamp", pythonjson.ISOFormat(sentAt))
		captured.Set("distinct_id", event.Account)
		captured.Set("event", event.Name)
		captured.Set("uuid", newEventID())
		events = append(events, captured)
	}

	envelope := pythonjson.NewObject()
	envelope.Set("batch", events)
	envelope.Set("historical_migration", false)
	envelope.Set("sent_at", pythonjson.ISOFormat(sentAt))
	envelope.Set("api_key", q.apiKey)
	return envelope
}
