package queuekeys

import (
	"os"
	"strings"
	"testing"
)

// The keys are the contract between the two producers and the two senders, so
// they are checked against the ones app/mail_queue/queue_store.py spells --
// the file itself, not a copy of it. A rename there that is not made here is
// two queues with one name.
func TestTheKeysAreTheOnesQueueStoreSpells(t *testing.T) {
	raw, err := os.ReadFile("../../soloclef_backend/app/mail_queue/queue_store.py")
	if err != nil {
		t.Skipf("the Python queue store is not in this checkout: %v", err)
	}
	source := string(raw)
	for _, want := range []string{
		`PREFIX = "soloclef:mail"`,
		`READY = f"{PREFIX}:ready"`,
		`RETRY = f"{PREFIX}:retry"`,
		`SENDING_PATTERN = f"{PREFIX}:sending:*"`,
		`return f"{PREFIX}:sending:{sender}"`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("queue_store.py no longer says %s; these keys may have moved with it", want)
		}
	}
}

func TestTheKeysAreWhatBothSendersRead(t *testing.T) {
	cases := map[string]string{
		Prefix:         "soloclef:mail",
		Ready:          "soloclef:mail:ready",
		Retry:          "soloclef:mail:retry",
		SendingPattern: "soloclef:mail:sending:*",
		Sending(0):     "soloclef:mail:sending:0",
		Sending(3):     "soloclef:mail:sending:3",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
