package automationmail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
)

// outbox is the waiting runs, in memory.
type outbox struct {
	waiting   []Mail
	settled   map[int64]Outcome
	released  map[int64]int
	abandoned []time.Time
}

func (o *outbox) Claim(_ context.Context, batch int) ([]Mail, error) {
	taken := o.waiting[:min(batch, len(o.waiting))]
	o.waiting = o.waiting[len(taken):]
	return taken, nil
}

func (o *outbox) Settle(_ context.Context, runID int64, outcome Outcome) error {
	o.settled[runID] = outcome
	return nil
}

func (o *outbox) Release(_ context.Context, runID int64, attempts int) error {
	o.released[runID] = attempts
	return nil
}

func (o *outbox) Abandon(_ context.Context, before time.Time, _ Outcome) (int64, error) {
	o.abandoned = append(o.abandoned, before)
	return 0, nil
}

// post records what was sent and fails as told, by recipient.
type post struct {
	sent  []mailer.Message
	fails map[string]error
}

func (p *post) Send(_ context.Context, message mailer.Message) error {
	if err := p.fails[message.To]; err != nil {
		return err
	}
	p.sent = append(p.sent, message)
	return nil
}

var now = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

func sender(rows Rows, mail Mailer) *Sender {
	return &Sender{Rows: rows, Mail: mail, FrontendURL: "https://notes.example.org/", Brand: "Soloclef Notes",
		Every: time.Second, Batch: 2, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
}

func text(outcome Outcome) string {
	to, why := "", ""
	if outcome.To != nil {
		to = *outcome.To
	}
	if outcome.Reason != nil {
		why = *outcome.Reason
	}
	return outcome.Status + "|" + to + "|" + why
}

func TestAResultIsMailedToItsOwnerAndTheOutcomeWritten(t *testing.T) {
	rows := &outbox{settled: map[int64]Outcome{}, released: map[int64]int{}, waiting: []Mail{
		{RunID: 1, AutomationID: 7, Automation: "Morning\r\nBcc: someone@example.net digest", To: "ada@example.org", Active: true, Status: "succeeded",
			Result: "High tide at noon [citation:481]. See the chart [citation:https://tides.example.org/today]."},
		{RunID: 2, AutomationID: 8, Automation: "Watch", To: "ada@example.org", Active: true, Status: "failed", Failure: "The model you chose is no longer offered."},
		{RunID: 3, AutomationID: 9, Automation: "Third", To: "ben@example.org", Active: true, Status: "needs_attention", Result: "Done."},
	}}
	out := &post{}
	s := sender(rows, out)
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	if err := s.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	// One batch: two of the three, each settled as sent to its own owner.
	if len(out.sent) != 2 || len(rows.waiting) != 1 || text(rows.settled[1]) != "sent|ada@example.org|" || text(rows.settled[2]) != "sent|ada@example.org|" {
		t.Fatalf("sent %d, settled %+v", len(out.sent), rows.settled)
	}
	first := out.sent[0]
	// The automation's name cannot add a header: the subject is one line.
	if first.To != "ada@example.org" || first.Subject != "Morning Bcc: someone@example.net digest: finished" || strings.ContainsAny(first.Subject, "\r\n") {
		t.Errorf("the first mail: to %q, subject %q", first.To, first.Subject)
	}
	for _, piece := range []string{
		"Your automation \"Morning Bcc: someone@example.net digest\" finished.",
		// A marker that only the site can open is dropped; an address is kept.
		"High tide at noon. See the chart (https://tides.example.org/today).",
		"See the run: https://notes.example.org/dashboard/automations/7\n", "\nSoloclef Notes\n",
	} {
		if !strings.Contains(first.TextBody, piece) {
			t.Errorf("the first mail lacks %q:\n%s", piece, first.TextBody)
		}
	}
	if strings.Contains(first.TextBody, "citation:") {
		t.Errorf("a marker was mailed:\n%s", first.TextBody)
	}
	if failed := out.sent[1]; failed.Subject != "Watch: failed" || !strings.Contains(failed.TextBody, "The model you chose is no longer offered.") {
		t.Errorf("the failed run's mail: %q\n%s", failed.Subject, failed.TextBody)
	}
	// Runs taken and never settled are given up on only after a while.
	if len(rows.abandoned) != 1 || !rows.abandoned[0].Equal(now.Add(-abandonAfter)) {
		t.Errorf("abandoned before %v", rows.abandoned)
	}
	// The next look takes the rest.
	if err := s.Once(context.Background()); err != nil || len(out.sent) != 3 || out.sent[2].Subject != "Third: finished and needs your attention" {
		t.Fatalf("the second look: %v, sent %d", err, len(out.sent))
	}
}

func TestWhatIsNotSentSaysWhy(t *testing.T) {
	one := func(mail Mail, send Mailer) (*outbox, error) {
		rows := &outbox{settled: map[int64]Outcome{}, released: map[int64]int{}, waiting: []Mail{mail}}
		return rows, sender(rows, send).Once(context.Background())
	}
	ada := Mail{RunID: 1, AutomationID: 7, Automation: "Digest", To: "ada@example.org", Active: true, Status: "succeeded", Result: "Done."}
	failing := func(err error) Mailer { return &post{fails: map[string]error{"ada@example.org": err}} }

	// A server that sends no mail settles each run instead of leaving it
	// waiting for ever.
	rows, err := one(ada, nil)
	if err != nil || text(rows.settled[1]) != "not_sent||This server is not set up to send mail." {
		t.Errorf("no mailer: %v, %+v", err, rows.settled)
	}
	inactive := ada
	inactive.Active = false
	out := &post{}
	if rows, err = one(inactive, out); err != nil || text(rows.settled[1]) != "not_sent||The account is no longer active." || len(out.sent) != 0 {
		t.Errorf("an inactive account: %v, %+v, sent %d", err, rows.settled, len(out.sent))
	}

	// A mail server that cannot take it now is tried again later, three
	// times in all, and then it is said.
	rows, err = one(ada, failing(fmt.Errorf("connect: %w", mailer.ErrUnavailable)))
	if err != nil || rows.released[1] != 1 || len(rows.settled) != 0 {
		t.Errorf("a first failed attempt: %v, released %v, settled %v", err, rows.released, rows.settled)
	}
	last := ada
	last.Attempts = maxAttempts - 1
	rows, err = one(last, failing(mailer.ErrUnavailable))
	if err != nil || len(rows.released) != 0 || text(rows.settled[1]) != "not_sent||The mail server could not be reached." {
		t.Errorf("the last failed attempt: %v, released %v, settled %v", err, rows.released, rows.settled)
	}
	rows, _ = one(ada, failing(mailer.ErrRejected))
	if text(rows.settled[1]) != "not_sent||The mail server refused the message." {
		t.Errorf("a refused message: %+v", rows.settled)
	}
	// This server's own settings are not shown to the owner as theirs.
	rows, _ = one(ada, failing(fmt.Errorf("%w: 535 authentication failed for relay-user", mailer.ErrMisconfigured)))
	if got := text(rows.settled[1]); got != "not_sent||This server's mail settings need attention." || strings.Contains(got, "relay-user") {
		t.Errorf("a misconfigured server: %s", got)
	}
	rows, _ = one(ada, failing(errors.New("something else")))
	if text(rows.settled[1]) != "not_sent||This server's mail settings need attention." {
		t.Errorf("an unforeseen failure: %+v", rows.settled)
	}
}

func TestALongResultIsCutAndTheMailWithNoSiteHasNoLink(t *testing.T) {
	s := sender(&outbox{}, &post{})
	s.FrontendURL = ""
	body := s.body(Mail{AutomationID: 7, Automation: "Digest", Status: "timed_out", Result: strings.Repeat("é", maxBodyChars+10)})
	if strings.Count(body, "é") != maxBodyChars || !strings.Contains(body, "The result is longer") || strings.Contains(body, "See the run") ||
		!strings.HasPrefix(body, "Your automation \"Digest\" ran out of time.") {
		t.Errorf("the body is %d characters and begins %q", len([]rune(body)), body[:60])
	}
}
