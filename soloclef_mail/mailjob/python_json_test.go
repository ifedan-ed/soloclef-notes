package mailjob

import (
	"testing"
)

func TestAnUnreadableEntryIsRefused(t *testing.T) {
	cases := map[string]string{
		"not json":          `{`,
		"no id":             `{"kind":"k","enqueued_at":1.0,"message":{"to":"a","subject":"s","text_body":"t"}}`,
		"no message":        `{"id":"x","kind":"k","enqueued_at":1.0}`,
		"no recipient":      `{"id":"x","kind":"k","enqueued_at":1.0,"message":{"subject":"s","text_body":"t"}}`,
		"attachment nonb64": `{"id":"x","kind":"k","enqueued_at":1.0,"message":{"to":"a","subject":"s","text_body":"t","attachments":[{"filename":"f","content":"!!!","content_type":"text/plain"}]}}`,
	}
	for name, raw := range cases {
		if _, err := Decode(raw); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAJobExpiresAtNotAfterAndNotBefore(t *testing.T) {
	deadline := 100.0
	job := Job{NotAfter: &deadline}
	if job.Expired(99.999) {
		t.Error("expired before its deadline")
	}
	if !job.Expired(100) {
		t.Error("MailJob.expired uses >=, so the deadline itself is expired")
	}
	if (Job{}).Expired(1e18) {
		t.Error("a job with no deadline never expires")
	}
}

func TestTheRetryCarriesTheSameIdOneTryOn(t *testing.T) {
	job := Job{ID: "abc", Kind: "invite", Attempt: 2}
	next := job.NextAttempt()
	if next.Attempt != 3 || next.ID != job.ID {
		t.Errorf("got attempt %d id %q", next.Attempt, next.ID)
	}
	if job.Attempt != 2 {
		t.Error("the original job was mutated; the queue entry it came from is still valid")
	}
}
