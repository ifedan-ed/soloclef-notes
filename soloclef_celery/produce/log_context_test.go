package produce

import (
	"context"
	"strings"
	"testing"
)

// The header's fields keep _PROPAGATED's order rather than being sorted, and
// the ones the request never bound are absent rather than empty.
func TestTheLogContextHeaderIsWrittenInPythonsOrder(t *testing.T) {
	producer, broker := testProducer(t)
	if _, err := producer.Send(context.Background(), Message{
		Task: "delete_document_background",
		Context: LogContext{
			RequestID: "req_1ea615454491",
			UserID:    "11111111-1111-4111-8111-111111111111",
			JobID:     "job-7",
		},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := `"soloclef_log_context": {"request_id": "req_1ea615454491", ` +
		`"user_id": "11111111-1111-4111-8111-111111111111", "job_id": "job-7"}`
	if published := broker.pushes[0].value; !strings.Contains(published, want) {
		t.Errorf("the log context is written as\n%s\nwant\n%s", published, want)
	}
}

// A publisher with no log context at all writes no header, as `if carried`
// decides there. An empty object would not be the envelope Celery wrote.
func TestNoLogContextWritesNoHeader(t *testing.T) {
	producer, broker := testProducer(t)
	if _, err := producer.Send(context.Background(), Message{Task: "delete_document_background"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if published := broker.pushes[0].value; strings.Contains(published, "soloclef_log_context") {
		t.Errorf("the header is present with nothing in it:\n%s", published)
	}
}

// The two call shapes this app uses are told apart by one header, and Delay is
// the one that reads like its call site.
func TestDelayReprsItsArgumentsAsATuple(t *testing.T) {
	producer, broker := testProducer(t)
	if _, err := producer.Send(context.Background(), Delay("delete_document_background", 7)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if published := broker.pushes[0].value; !strings.Contains(published, `"argsrepr": "(7,)"`) {
		t.Errorf("argsrepr is not a tuple's:\n%s", published)
	}

	producer, broker = testProducer(t)
	if _, err := producer.Send(context.Background(), Message{
		Task: "automation_run_execute", Args: []any{7},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if published := broker.pushes[0].value; !strings.Contains(published, `"argsrepr": "[7]"`) {
		t.Errorf("argsrepr is not a list's:\n%s", published)
	}
}
