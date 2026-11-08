package smtp

import "testing"

func TestTheAddressIsTakenOutOfTheHeaderValue(t *testing.T) {
	cases := map[string]string{
		"notes@queue.test":                         "notes@queue.test",
		"  notes@queue.test  ":                     "notes@queue.test",
		"Soloclef Notes <notes@queue.test>":        "notes@queue.test",
		"Soloclef Nötes <notes@queue.test>":        "notes@queue.test",
		"\"Notes, Inc\" <notes@queue.test>":        "notes@queue.test",
		"Notes <notes@queue.test> <second@x.test>": "second@x.test",
	}
	for header, want := range cases {
		if got := envelopeAddress(header); got != want {
			t.Errorf("%q gave %q, want %q", header, got, want)
		}
	}
}

func TestAnAddressNeedingSmtputf8IsRefusedRatherThanSentDifferently(t *testing.T) {
	// smtplib would send it with raw UTF-8 headers over SMTPUTF8, or raise if
	// the server does not advertise it. This transport does neither, so it
	// refuses -- the one message the two senders do not treat the same, and
	// the only one where this sender delivers nothing that the Python one
	// might have delivered.
	if err := ascii("notes@queue.test", "rené@queue.test"); err == nil {
		t.Error("a non-ASCII recipient was accepted")
	}
	if err := ascii("rené@queue.test", "person@queue.test"); err == nil {
		t.Error("a non-ASCII sender was accepted")
	}
	if err := ascii("notes@queue.test", "person@queue.test"); err != nil {
		t.Errorf("a plain pair was refused: %v", err)
	}
}
