// Package automationmail emails the result of an automation's run to the
// automation's owner, for an automation that asks for it
// (`execution.email_result`).
//
// # Why it is here and not in the worker
//
// The worker runs the run; this tier holds the deployment's SMTP client.
// So the run's row is the outbox between them. The worker ends a run with
// `output.email.status` "pending" and does nothing more. A sender in this
// process looks for such runs every few seconds, takes one by marking it
// "sending", sends it, and writes what became of it: "sent" with the
// address, or "not_sent" with the reason. The run page reads that.
//
// # At most once
//
// A run is taken in the statement that marks it, with SKIP LOCKED, so two
// replicas never send the same one. If this process stops between taking
// a run and writing the outcome, the mail may or may not have left: it is
// not sent again (two copies of a result are worse than a line saying it
// may not have arrived), and after a while the run is marked not sent,
// with that said. A send the mail server could not take just now is put
// back and tried again, three times in all.
//
// # Whose address
//
// The owner's, read from the account through the run's own automation in
// the statement that takes the run. Nothing in a run or a definition names
// a recipient, so an automation can only ever mail its own account.
//
// # A server that sends no mail
//
// An automation that asks for an email cannot be saved where
// SMTP_ENABLED is false (internal/automations). One saved before mail was
// switched off still ends its runs "pending"; the sender runs without a
// mailer then, and settles each as not sent, saying why.
package automationmail

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
)

// The statuses of output.email. Pending is the worker's word
// (soloclef_worker/internal/automationrun.EmailPending) and, with Sending,
// what the schema's partial index is built on.
const (
	Pending = "pending"
	Sending = "sending"
	Sent    = "sent"
	NotSent = "not_sent"
)

// The decisions about the sender, made here with their reasons.
const (
	// maxAttempts is how many times one result is offered to a mail server
	// that cannot take it just now.
	maxAttempts = 3
	// abandonAfter is how long a run may stay "sending" before it is said
	// to be not sent: far longer than a send, which the SMTP client's own
	// timeout bounds.
	abandonAfter = 10 * time.Minute
	// maxBodyChars bounds the result in a mail. A result is an answer, and
	// the whole of it is on the run's page.
	maxBodyChars = 50_000
)

// Mail is one run's result, to be mailed.
type Mail struct {
	RunID        int64
	AutomationID int64
	Automation   string
	// To is the owner's address; Active whether the account still is.
	To     string
	Active bool
	// Status is how the run ended; Result its final message and Failure
	// its error's sentence.
	Status   string
	Result   string
	Failure  string
	Attempts int
}

// Outcome is what became of one mail, as output.email holds it.
type Outcome struct {
	Status string  `json:"status"`
	To     *string `json:"to"`
	Reason *string `json:"reason"`
}

// Rows is the outbox.
type Rows interface {
	// Claim takes up to batch pending runs for this sender.
	Claim(ctx context.Context, batch int) ([]Mail, error)
	// Settle writes the outcome of a run this sender took.
	Settle(ctx context.Context, runID int64, outcome Outcome) error
	// Release puts a run back to wait, with the attempts it has had.
	Release(ctx context.Context, runID int64, attempts int) error
	// Abandon settles, as outcome, every run taken before `before` and
	// never settled.
	Abandon(ctx context.Context, before time.Time, outcome Outcome) (int64, error)
}

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) Claim(ctx context.Context, batch int) ([]Mail, error) {
	rows, err := New(s.Pool).ClaimPendingMail(ctx, int32(batch))
	if err != nil {
		return nil, err
	}
	out := make([]Mail, 0, len(rows))
	for _, row := range rows {
		out = append(out, Mail{RunID: row.ID, AutomationID: row.AutomationID, Automation: row.Name, To: row.Email, Active: row.IsActive,
			Status: row.Status, Result: row.FinalMessage, Failure: row.ErrorMessage, Attempts: int(row.Attempts)})
	}
	return out, nil
}

func (s Store) Settle(ctx context.Context, runID int64, outcome Outcome) error {
	encoded, err := json.Marshal(outcome)
	if err != nil {
		return err
	}
	_, err = New(s.Pool).SettleMail(ctx, SettleMailParams{Email: encoded, ID: runID})
	return err
}

func (s Store) Release(ctx context.Context, runID int64, attempts int) error {
	_, err := New(s.Pool).ReleaseMail(ctx, ReleaseMailParams{Attempts: int64(attempts), ID: runID})
	return err
}

func (s Store) Abandon(ctx context.Context, before time.Time, outcome Outcome) (int64, error) {
	encoded, err := json.Marshal(outcome)
	if err != nil {
		return 0, err
	}
	return New(s.Pool).AbandonStaleMail(ctx, AbandonStaleMailParams{Email: encoded, Before: before})
}

// Mailer sends one message (soloclef_mail's SMTP client).
type Mailer interface {
	Send(ctx context.Context, message mailer.Message) error
}

// Sender sends the waiting results. One goroutine runs it: an SMTP client
// carries one message at a time.
type Sender struct {
	Rows Rows
	// Mail is the deployment's SMTP client, or nil where it sends no mail.
	Mail Mailer
	// FrontendURL is the website's address, for the link to the run.
	// Optional: without it the mail has no link.
	FrontendURL string
	// Brand signs the mail.
	Brand string
	// Every is how often the outbox is looked at, and Batch how many runs
	// one look takes.
	Every  time.Duration
	Batch  int
	Logger *slog.Logger
	Now    func() time.Time
}

// Check reports what is missing. Called once at start-up.
func (s *Sender) Check() error {
	if s.Rows == nil || s.Brand == "" || s.Every <= 0 || s.Batch < 1 || s.Logger == nil || s.Now == nil {
		return errors.New("automationmail: Rows, Brand, Every, Batch, Logger and Now are all required")
	}
	return nil
}

// Run looks at the outbox every Every until ctx ends.
func (s *Sender) Run(ctx context.Context) {
	ticker := time.NewTicker(s.Every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// A look that failed is the next look's to repeat: the rows
			// are still there.
			if err := s.Once(ctx); err != nil && ctx.Err() == nil {
				s.Logger.LogAttrs(ctx, slog.LevelWarn, "the automation results waiting to be emailed could not be sent", slog.String("error", err.Error()))
			}
		}
	}
}

func reason(text string) *string { return &text }

// Once takes one batch and settles each run of it.
func (s *Sender) Once(ctx context.Context) error {
	if _, err := s.Rows.Abandon(ctx, s.Now().Add(-abandonAfter), Outcome{Status: NotSent,
		Reason: reason("The server stopped while sending it, so it may not have arrived.")}); err != nil {
		return err
	}
	taken, err := s.Rows.Claim(ctx, s.Batch)
	if err != nil {
		return err
	}
	for _, mail := range taken {
		// Each outcome is written whether or not the server is stopping:
		// a run taken and left "sending" would be said to be lost.
		settling := context.WithoutCancel(ctx)
		outcome, again := s.send(ctx, mail)
		if again {
			err = s.Rows.Release(settling, mail.RunID, mail.Attempts+1)
		} else {
			err = s.Rows.Settle(settling, mail.RunID, outcome)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// send sends one run's mail and says what became of it; again is true for
// a send to try once more later.
func (s *Sender) send(ctx context.Context, mail Mail) (outcome Outcome, again bool) {
	switch {
	case s.Mail == nil:
		return Outcome{Status: NotSent, Reason: reason("This server is not set up to send mail.")}, false
	case !mail.Active:
		return Outcome{Status: NotSent, Reason: reason("The account is no longer active.")}, false
	}
	err := s.Mail.Send(ctx, mailer.Message{To: mail.To, Subject: subject(mail), TextBody: s.body(mail)})
	switch {
	case err == nil:
		return Outcome{Status: Sent, To: &mail.To}, false
	case errors.Is(err, mailer.ErrUnavailable) && mail.Attempts+1 < maxAttempts:
		return Outcome{}, true
	case errors.Is(err, mailer.ErrUnavailable):
		return Outcome{Status: NotSent, Reason: reason("The mail server could not be reached.")}, false
	case errors.Is(err, mailer.ErrRejected):
		return Outcome{Status: NotSent, Reason: reason("The mail server refused the message.")}, false
	}
	// The server's own settings, or something unforeseen: the operator's
	// to read, never the owner's fault.
	s.Logger.LogAttrs(ctx, slog.LevelError, "an automation's result could not be emailed", slog.Int64("run_id", mail.RunID), slog.String("error", err.Error()))
	return Outcome{Status: NotSent, Reason: reason("This server's mail settings need attention.")}, false
}

// endings is how a run's status reads in a sentence.
var endings = map[string]string{
	"succeeded": "finished", "needs_attention": "finished and needs your attention",
	"failed": "failed", "timed_out": "ran out of time",
}

func ending(status string) string {
	if words, known := endings[status]; known {
		return words
	}
	return "ended"
}

// oneLine is a name on one line: a subject has no line breaks.
func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

func subject(mail Mail) string {
	return oneLine(mail.Automation) + ": " + ending(mail.Status)
}

// The source markers a result holds, which a page draws as chips. In a
// mail a marker that is an address is kept as the address, and one that
// names a passage of the owner's documents, which only the site can open,
// is dropped.
var (
	addressMarker = regexp.MustCompile(`\[citation:\s*(https?://[^\]\s]+)\s*\]`)
	otherMarker   = regexp.MustCompile(`\s?\[citation:[^\]]*\]`)
)

func plain(result string) string {
	result = addressMarker.ReplaceAllString(result, "($1)")
	return otherMarker.ReplaceAllString(result, "")
}

// body is the mail's text: what happened, the result or the reason, and
// where to see the run.
func (s *Sender) body(mail Mail) string {
	var b strings.Builder
	b.WriteString("Your automation \"" + oneLine(mail.Automation) + "\" " + ending(mail.Status) + ".\n")
	if failure := strings.TrimSpace(mail.Failure); failure != "" {
		b.WriteString("\n" + failure + "\n")
	}
	if result := strings.TrimSpace(plain(mail.Result)); result != "" {
		if runes := []rune(result); len(runes) > maxBodyChars {
			result = string(runes[:maxBodyChars]) + "\n\n(The result is longer; the rest is on the run's page.)"
		}
		b.WriteString("\n" + result + "\n")
	}
	if site := strings.TrimRight(s.FrontendURL, "/"); site != "" {
		b.WriteString("\nSee the run: " + site + "/dashboard/automations/" + strconv.FormatInt(mail.AutomationID, 10) + "\n")
	}
	b.WriteString("\n" + s.Brand + "\n")
	return b.String()
}
