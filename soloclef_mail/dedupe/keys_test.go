package dedupe_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupe"
)

func TestACallersKeyIsHashedSoNoTokenBecomesARedisKeyName(t *testing.T) {
	key := dedupe.QueuedKey("password_reset:tok-secret-123")
	if strings.Contains(key, "tok-secret-123") {
		t.Errorf("the caller's key is in the Redis key name: %s", key)
	}
	if !strings.HasPrefix(key, "soloclef:mail:queued:") {
		t.Errorf("got %s", key)
	}
}

func TestTheHandoverKeyCarriesTheAttemptSoARetryIsNotADuplicate(t *testing.T) {
	first := dedupe.HandoverKey("aaaabbbbccccddddeeeeffff00001111", 0)
	retry := dedupe.HandoverKey("aaaabbbbccccddddeeeeffff00001111", 1)
	if first == retry {
		t.Fatal("a retry would be refused as a duplicate")
	}
	if first != "soloclef:mail:handover:aaaabbbbccccddddeeeeffff00001111:0" {
		t.Errorf("got %s", first)
	}
}

func TestTheWindowIsADay(t *testing.T) {
	if dedupe.Window != 24*time.Hour {
		t.Errorf("got %v", dedupe.Window)
	}
}

// Both sides must spell every key the same way, because either may be the one
// queueing and either may be the one sending. The fixture is generated out of
// app/mail_queue/dedupe's own code (scripts/python_dedupe_keys.py), not written
// by hand.
func TestTheKeysAreTheOnesPythonWrites(t *testing.T) {
	raw, err := os.ReadFile("testdata/python_dedupe_keys.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		WindowSeconds float64 `json:"window_seconds"`
		Claimed       string  `json:"claimed"`
		Sent          string  `json:"sent"`
		Queued        []struct {
			IdempotencyKey string `json:"idempotency_key"`
			Key            string `json:"key"`
		} `json:"queued"`
		Handover []struct {
			MailID  string `json:"mail_id"`
			Attempt int    `json:"attempt"`
			Key     string `json:"key"`
		} `json:"handover"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if dedupe.Window.Seconds() != fixture.WindowSeconds {
		t.Errorf("the window is %v here and %v seconds in Python", dedupe.Window, fixture.WindowSeconds)
	}
	if dedupe.Claimed != fixture.Claimed || dedupe.Sent != fixture.Sent {
		t.Errorf("the marks read %q/%q here and %q/%q in Python",
			dedupe.Claimed, dedupe.Sent, fixture.Claimed, fixture.Sent)
	}
	if len(fixture.Queued) == 0 || len(fixture.Handover) == 0 {
		t.Fatal("the fixture names no keys")
	}
	for _, c := range fixture.Queued {
		if got := dedupe.QueuedKey(c.IdempotencyKey); got != c.Key {
			t.Errorf("QueuedKey(%q) = %q, Python writes %q", c.IdempotencyKey, got, c.Key)
		}
	}
	for _, c := range fixture.Handover {
		if got := dedupe.HandoverKey(c.MailID, c.Attempt); got != c.Key {
			t.Errorf("HandoverKey(%q, %d) = %q, Python writes %q", c.MailID, c.Attempt, got, c.Key)
		}
	}
}
