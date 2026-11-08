package capturecompare

import "testing"

// The blanking has to be the one the captures were taken with, or every
// comparison against them compares two different things.
func TestBlankedReplacesEveryValueThatChangesPerPublish(t *testing.T) {
	envelope := `{"headers": {"id": "a", "root_id": "a", "origin": "gen1@host", ` +
		`"soloclef_log_context": {"request_id": "req_0123456789ab", "user_id": "u"}, ` +
		`"surfsense.enqueued_at_ns": "1833747970624788"}, ` +
		`"properties": {"correlation_id": "a", "reply_to": "b", "delivery_tag": "c"}}`
	want := `{"headers": {"id": "<id>", "root_id": "<root_id>", "origin": "<origin>", ` +
		`"soloclef_log_context": {"request_id": "<request_id>", "user_id": "u"}, ` +
		`"surfsense.enqueued_at_ns": "<enqueued_at>"}, ` +
		`"properties": {"correlation_id": "<correlation_id>", "reply_to": "<reply_to>", "delivery_tag": "<delivery_tag>"}}`
	if got := Blanked(envelope); got != want {
		t.Errorf("Blanked wrote\n%s\nwant\n%s", got, want)
	}
}

// A user id is not blanked: it is the caller, and a message published for a
// different account is a different message.
func TestTheCallerIsNotBlanked(t *testing.T) {
	envelope := `{"user_id": "11111111-1111-4111-8111-111111111111"}`
	if got := Blanked(envelope); got != envelope {
		t.Errorf("Blanked wrote %s", got)
	}
}
