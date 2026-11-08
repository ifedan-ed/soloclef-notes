// Package capturecompare reads what the Python tier answered and publishes the
// same message here, so a slice's test can compare the two.
//
// Nothing outside a test imports it. It exists because the proof that a write
// moved is a comparison against the Python tier over HTTP, and that comparison
// is worth keeping once it has been made: the captures are checked in beside
// each slice, and these tests fail if the Go tier stops reproducing them.
package capturecompare

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
)

// Answer is one request the Python tier was asked and everything it did: the
// status, the body, and the messages it left on the queues.
type Answer struct {
	Label   string `json:"label"`
	Request struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		As     string `json:"as"`
	} `json:"request"`
	Status int    `json:"status"`
	Body   string `json:"body"`
	Queued []struct {
		Queue    string `json:"queue"`
		Envelope string `json:"envelope"`
	} `json:"queued"`
}

// Answers loads a slice's captures.
func Answers(t *testing.T, path string) []Answer {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the captures: %v", err)
	}
	var answers []Answer
	if err := json.Unmarshal(raw, &answers); err != nil {
		t.Fatalf("the captures are not JSON: %v", err)
	}
	if len(answers) == 0 {
		t.Fatal("no captures to compare against")
	}
	return answers
}

// Published is the envelope this tier publishes for one message, and the Redis
// key it goes on, blanked the way the captures were.
func Published(t *testing.T, defaultQueue string, message produce.Message) (key, envelope string) {
	t.Helper()
	broker := &recordingBroker{}
	producer := produce.Producer{
		Broker:  broker,
		Routes:  routing.Table{Default: defaultQueue},
		Origin:  produce.Origin(),
		ReplyTo: "00000000-0000-4000-8000-00000000ffff",
	}
	if _, err := producer.Send(context.Background(), message); err != nil {
		t.Fatalf("Send: %v", err)
	}
	return broker.key, Blanked(broker.value)
}

type recordingBroker struct{ key, value string }

func (b *recordingBroker) LPush(_ context.Context, key, value string) error {
	b.key, b.value = key, value
	return nil
}

// Blanked replaces the parts of an envelope that are different every time a
// task is published — the ids, the publisher, the enqueue stamp and the
// request id — so that what is left to compare is the spelling. It is the
// substitution the captures were taken with; two spellings of it would compare
// two different things.
func Blanked(envelope string) string {
	for _, replacement := range blankings {
		envelope = replacement.pattern.ReplaceAllString(envelope, replacement.with)
	}
	return envelope
}

var blankings = []struct {
	pattern *regexp.Regexp
	with    string
}{
	{regexp.MustCompile(`"id": "[^"]+"`), `"id": "<id>"`},
	{regexp.MustCompile(`"root_id": "[^"]+"`), `"root_id": "<root_id>"`},
	{regexp.MustCompile(`"correlation_id": "[^"]+"`), `"correlation_id": "<correlation_id>"`},
	{regexp.MustCompile(`"delivery_tag": "[^"]+"`), `"delivery_tag": "<delivery_tag>"`},
	{regexp.MustCompile(`"origin": "[^"]+"`), `"origin": "<origin>"`},
	{regexp.MustCompile(`"reply_to": "[^"]+"`), `"reply_to": "<reply_to>"`},
	{regexp.MustCompile(`"surfsense\.enqueued_at_ns": "[^"]+"`), `"surfsense.enqueued_at_ns": "<enqueued_at>"`},
	{regexp.MustCompile(`"request_id": "req_[0-9a-f]+"`), `"request_id": "<request_id>"`},
}
