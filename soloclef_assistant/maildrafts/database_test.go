package maildrafts

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The opt-in tests below put the whole guard to a real Postgres: the
// transaction, the locks and the statements are what makes a send without
// a valid go-ahead impossible, and only Postgres can be asked about them.
// They are skipped unless the variable names a throwaway database at the
// schema soloclef_schema's `migrate up` makes; it must never name one
// anybody uses, because it empties it.
//
//	SOLOCLEF_API_TEST_MAILDRAFTS_DATABASE=postgres://check:check@127.0.0.1:39827/check \
//	  go test ./maildrafts/ -run OverARealDatabase -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_MAILDRAFTS_DATABASE"

const (
	accountA = "a0a0a0a0-0000-4000-8000-0000000000a1"
	accountB = "b0b0b0b0-0000-4000-8000-0000000000b1"
)

const seed = `
TRUNCATE "user" CASCADE;
INSERT INTO "user" (id, email) VALUES
  ('aaaaaaaa-0000-4000-8000-000000000001', 'ada@example.test'),
  ('bbbbbbbb-0000-4000-8000-000000000002', 'ben@example.test');
INSERT INTO jmap_mail_accounts (id, user_id, label, server_url, username, auth, credentials, can_send) VALUES
  ('a0a0a0a0-0000-4000-8000-0000000000a1', 'aaaaaaaa-0000-4000-8000-000000000001', 'Ada''s Stalwart', 'https://mail.example.test', 'ada@example.test', 'app_password', 'sealed', true),
  ('b0b0b0b0-0000-4000-8000-0000000000b1', 'bbbbbbbb-0000-4000-8000-000000000002', 'Ben''s Stalwart', 'https://mail.example.test', 'ben@example.test', 'app_password', 'sealed', true);
`

// recordingSender is a mailbox that records what it was asked to send.
type recordingSender struct {
	mu   sync.Mutex
	sent []Draft
	fail string
}

func (r *recordingSender) Send(_ context.Context, _ string, draft Draft) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != "" {
		return "", &SendFailure{Message: r.fail}
	}
	r.sent = append(r.sent, draft)
	return "server-id-" + draft.ID[:8], nil
}

type namedAccounts struct{}

func (namedAccounts) Describe(_ context.Context, _, _, accountID string) (AccountView, error) {
	return AccountView{ID: accountID, Label: "Stalwart"}, nil
}

// waitingNotes records the notification hook's calls.
type waitingNotes struct {
	mu    sync.Mutex
	calls []Waiting
}

func (w *waitingNotes) DraftWaiting(_ context.Context, waiting Waiting) {
	w.mu.Lock()
	w.calls = append(w.calls, waiting)
	w.mu.Unlock()
}

// history is the recipients' history the tests set: never is who the
// mailbox has never written to, failing makes it unreachable.
type history struct {
	mu      sync.Mutex
	never   map[string]bool
	failing bool
	asked   [][]string
}

func (h *history) NeverMailed(_ context.Context, _, _, _ string, addresses []string) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.asked = append(h.asked, addresses)
	if h.failing {
		return nil, errors.New("the mailbox could not be reached")
	}
	out := []string{}
	for _, address := range addresses {
		if h.never[address] {
			out = append(out, address)
		}
	}
	return out, nil
}

// files is a draft's files as the tests set them: changed names the file
// whose row no longer matches.
type files struct {
	NoAttachmentFiles
	changed string
}

func (f *files) Changed(context.Context, string, []Attachment) (string, error) { return f.changed, nil }

// testClock is a clock the tests move.
type testClock struct{ at atomic.Int64 }

func (c *testClock) now() time.Time          { return time.Unix(0, c.at.Load()).UTC() }
func (c *testClock) advance(d time.Duration) { c.at.Add(int64(d)) }

// fixture is a guard over the database and the fakes the tests steer.
type fixture struct {
	guard   *Guard
	pool    *pgxpool.Pool
	sender  *recordingSender
	clock   *testClock
	notes   *waitingNotes
	history *history
	files   *files
}

func openGuard(t *testing.T) fixture {
	t.Helper()
	address := os.Getenv(databaseVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, seed); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	sender := &recordingSender{}
	clock := &testClock{}
	clock.at.Store(time.Now().UnixNano())
	notes := &waitingNotes{}
	known := &history{never: map[string]bool{}}
	attached := &files{}
	guard := &Guard{Rows: Store{Pool: pool}, Accounts: namedAccounts{}, Senders: map[string]Sender{ProviderJMAP: sender},
		History: known, Files: attached, Notify: notes, Now: clock.now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := guard.Check(); err != nil {
		t.Fatal(err)
	}
	return fixture{guard: guard, pool: pool, sender: sender, clock: clock, notes: notes, history: known, files: attached}
}

func draftFor(account string) Message {
	message := sample()
	message.AccountID = account
	return message
}

func TestTheSendGuardOverARealDatabase(t *testing.T) {
	f := openGuard(t)
	guard, pool, sender, clock, notes := f.guard, f.pool, f.sender, f.clock, f.notes
	ctx := context.Background()

	created, err := guard.Create(ctx, ownerA, ProviderJMAP, draftFor(accountA), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes.calls) != 1 || notes.calls[0].DraftID != created.ID || notes.calls[0].Recipients != 1 || notes.calls[0].Subject != "Lunch" {
		t.Fatalf("the waiting draft was not announced: %+v", notes.calls)
	}

	t.Run("a send without a valid go-ahead is impossible", func(t *testing.T) {
		message := draftFor(accountA)
		for name, token := range map[string]string{"none": "", "made up": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
			if _, err := guard.Send(ctx, ownerA, created.ID, token, message.ContentHash(), nil); !errors.Is(err, ErrConfirmationInvalid) {
				t.Errorf("%s: %v", name, err)
			}
		}
		if len(sender.sent) != 0 {
			t.Fatal("a draft was sent without a go-ahead")
		}
	})

	t.Run("another account is refused", func(t *testing.T) {
		shown, err := guard.Confirm(ctx, ownerA, created.ID)
		if err != nil || shown.Confirmation == nil {
			t.Fatalf("confirm = %+v, %v", shown, err)
		}
		// Ben with Ada's draft and Ada's go-ahead: the draft is not his.
		if _, err := guard.Send(ctx, ownerB, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil); !errors.Is(err, ErrNoDraft) {
			t.Errorf("Ben sent Ada's draft: %v", err)
		}
		if _, err := guard.Confirm(ctx, ownerB, created.ID); !errors.Is(err, ErrNoDraft) {
			t.Errorf("Ben was shown Ada's draft: %v", err)
		}
		// Ben's own draft with Ada's go-ahead: not his go-ahead.
		his, err := guard.Create(ctx, ownerB, ProviderJMAP, draftFor(accountB), "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := guard.Send(ctx, ownerB, his.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil); !errors.Is(err, ErrConfirmationInvalid) {
			t.Errorf("Ada's go-ahead sent Ben's draft: %v", err)
		}
		if len(sender.sent) != 0 {
			t.Fatal("something was sent")
		}
	})

	t.Run("a draft changed after it was shown invalidates the go-ahead", func(t *testing.T) {
		shown, err := guard.Confirm(ctx, ownerA, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		// Anything that touches the row: here a recipient written straight
		// into it, as nothing in the product can.
		if _, err := pool.Exec(ctx, `UPDATE mail_drafts SET message = jsonb_set(message, '{bcc}', '[{"name":"","email":"evil@example.com"}]') WHERE id = $1`, created.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := guard.Send(ctx, ownerA, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil); !errors.Is(err, ErrDraftChanged) {
			t.Errorf("a changed draft: %v", err)
		}
		// The browser saying it showed something other than what the
		// go-ahead was issued for is refused the same way.
		if _, err := pool.Exec(ctx, `UPDATE mail_drafts SET message = jsonb_set(message, '{bcc}', '[]') WHERE id = $1`, created.ID); err != nil {
			t.Fatal(err)
		}
		fresh, err := guard.Confirm(ctx, ownerA, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := guard.Send(ctx, ownerA, created.ID, fresh.Confirmation.Token, "sha256:something-else", nil); !errors.Is(err, ErrDraftChanged) {
			t.Errorf("a presented hash that does not match: %v", err)
		}
		if len(sender.sent) != 0 {
			t.Fatal("a changed draft was sent")
		}
	})

	t.Run("a revision supersedes the draft and its go-ahead", func(t *testing.T) {
		first, err := guard.Create(ctx, ownerA, ProviderJMAP, draftFor(accountA), "")
		if err != nil {
			t.Fatal(err)
		}
		shown, err := guard.Confirm(ctx, ownerA, first.ID)
		if err != nil {
			t.Fatal(err)
		}
		revised := draftFor(accountA)
		revised.Body = "Make it one o'clock."
		second, err := guard.Create(ctx, ownerA, ProviderJMAP, revised, first.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := guard.Send(ctx, ownerA, first.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil); !errors.Is(err, ErrDraftNotPending) {
			t.Errorf("a superseded draft: %v", err)
		}
		view, err := guard.Confirm(ctx, ownerA, first.ID)
		if err != nil || view.Draft.Status != StatusSuperseded || view.Confirmation != nil || *view.Draft.SupersededBy != second.ID {
			t.Errorf("the replaced draft's view = %+v, %v", view.Draft, err)
		}
		// Revising a draft that is not waiting is refused, and stores nothing.
		if _, err := guard.Create(ctx, ownerA, ProviderJMAP, revised, first.ID); !errors.Is(err, ErrNoDraft) {
			t.Errorf("a second revision of a superseded draft: %v", err)
		}
	})

	t.Run("a go-ahead runs out", func(t *testing.T) {
		shown, err := guard.Confirm(ctx, ownerA, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		clock.advance(ConfirmationLifetime)
		if _, err := guard.Send(ctx, ownerA, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil); !errors.Is(err, ErrConfirmationExpired) {
			t.Errorf("an expired go-ahead: %v", err)
		}
	})

	t.Run("the confirmed message is sent once, exactly, and logged", func(t *testing.T) {
		shown, err := guard.Confirm(ctx, ownerA, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		// Two presses at once: one sends, the other finds the go-ahead spent.
		var wait sync.WaitGroup
		results := make([]error, 2)
		for index := range results {
			wait.Add(1)
			go func() {
				defer wait.Done()
				_, results[index] = guard.Send(ctx, ownerA, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil)
			}()
		}
		wait.Wait()
		succeeded := 0
		for _, err := range results {
			switch {
			case err == nil:
				succeeded++
			case !errors.Is(err, ErrConfirmationSpent) && !errors.Is(err, ErrDraftNotPending):
				t.Errorf("the second press: %v", err)
			}
		}
		if succeeded != 1 || len(sender.sent) != 1 {
			t.Fatalf("%d sends succeeded, %d reached the server", succeeded, len(sender.sent))
		}
		if sender.sent[0].Message.ContentHash() != shown.Confirmation.ContentHash || sender.sent[0].Message.Body != "Noon at the usual place?" {
			t.Fatalf("what was sent is not what was shown: %+v", sender.sent[0].Message)
		}
		// Replayed afterwards: refused.
		if _, err := guard.Send(ctx, ownerA, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil); !errors.Is(err, ErrConfirmationSpent) {
			t.Errorf("a replay: %v", err)
		}
		view, err := guard.Confirm(ctx, ownerA, created.ID)
		if err != nil || view.Draft.Status != StatusSent || view.Confirmation != nil || view.Draft.SentAt == nil {
			t.Fatalf("after sending = %+v, %v", view, err)
		}
		var message, status, source string
		var to []string
		err = pool.QueryRow(ctx, `SELECT message, status::text, source, ARRAY(SELECT jsonb_array_elements_text(log_metadata->'to'))
			FROM logs WHERE user_id = $1 ORDER BY id DESC LIMIT 1`, ownerA).Scan(&message, &status, &source, &to)
		if err != nil || message != "Email sent: Lunch" || status != "SUCCESS" || source != activitySource || len(to) != 1 || to[0] != "ben@example.test" {
			t.Fatalf("activity = %q %q %q %v, %v", message, status, source, to, err)
		}
	})

	t.Run("a refused send is logged and can be confirmed again", func(t *testing.T) {
		again, err := guard.Create(ctx, ownerA, ProviderJMAP, draftFor(accountA), "")
		if err != nil {
			t.Fatal(err)
		}
		shown, err := guard.Confirm(ctx, ownerA, again.ID)
		if err != nil {
			t.Fatal(err)
		}
		sender.fail = "The mail server does not allow this sign-in to send"
		view, err := guard.Send(ctx, ownerA, again.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil)
		var failure *SendFailure
		if !errors.As(err, &failure) || view.Draft.Status != StatusFailed || *view.Draft.Failure != sender.fail {
			t.Fatalf("a refused send = %+v, %v", view.Draft, err)
		}
		sender.fail = ""
		retry, err := guard.Confirm(ctx, ownerA, again.ID)
		if err != nil || retry.Confirmation == nil {
			t.Fatalf("a failed draft cannot be confirmed again: %+v, %v", retry, err)
		}
		var failed int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM logs WHERE user_id = $1 AND status = 'FAILED'`, ownerA).Scan(&failed)
		if failed != 1 {
			t.Fatalf("%d failed sends logged", failed)
		}
	})

	t.Run("a busy hour is confirmed twice, never refused", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE mail_drafts SET sending_at = $2 WHERE user_id = $1`, ownerA, clock.now()); err != nil {
			t.Fatal(err)
		}
		for range UnusualSendsPerHour {
			if _, err := pool.Exec(ctx, `INSERT INTO mail_drafts (user_id, provider, jmap_account_id, message, content_hash, status, expires_at, sending_at)
				VALUES ($1, 'jmap', $2, '{}', 'x', 'sent', $3, $3)`, ownerA, accountA, clock.now()); err != nil {
				t.Fatal(err)
			}
		}
		next, err := guard.Create(ctx, ownerA, ProviderJMAP, draftFor(accountA), "")
		if err != nil {
			t.Fatal(err)
		}
		shown, err := guard.Confirm(ctx, ownerA, next.ID)
		if err != nil || len(shown.Confirmation.Unusual) != 1 || shown.Confirmation.Unusual[0].Code != UnusualManySends {
			t.Fatalf("the busy hour is not asked about: %+v, %v", shown.Confirmation, err)
		}
		// Send alone, as before: the server refuses it for the missing tick,
		// whatever the card drew, and the go-ahead is not spent by it.
		if _, err := guard.Send(ctx, ownerA, next.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil); !errors.Is(err, ErrUnusualNotConfirmed) {
			t.Fatalf("an unticked busy hour: %v", err)
		}
		if _, err := guard.Send(ctx, ownerA, next.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, []string{UnusualManySends}); err != nil {
			t.Fatalf("a ticked busy hour: %v", err)
		}
		// The ticks were bound to that one go-ahead: replayed, refused.
		if _, err := guard.Send(ctx, ownerA, next.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, []string{UnusualManySends}); !errors.Is(err, ErrConfirmationSpent) {
			t.Fatalf("a replay of the ticked go-ahead: %v", err)
		}
	})

	t.Run("a send that becomes unusual after the card was drawn is shown again", func(t *testing.T) {
		clock.advance(2 * time.Hour)
		calm, err := guard.Create(ctx, ownerA, ProviderJMAP, draftFor(accountA), "")
		if err != nil {
			t.Fatal(err)
		}
		shown, err := guard.Confirm(ctx, ownerA, calm.ID)
		if err != nil || len(shown.Confirmation.Unusual) != 0 {
			t.Fatalf("a quiet hour: %+v, %v", shown.Confirmation, err)
		}
		// Meanwhile other cards sent enough to make the hour busy.
		for range UnusualSendsPerHour {
			if _, err := pool.Exec(ctx, `INSERT INTO mail_drafts (user_id, provider, jmap_account_id, message, content_hash, status, expires_at, sending_at)
				VALUES ($1, 'jmap', $2, '{}', 'x', 'sent', $3, $3)`, ownerA, accountA, clock.now()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := guard.Send(ctx, ownerA, calm.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil); !errors.Is(err, ErrUnusualChanged) {
			t.Fatalf("a send that became unusual: %v", err)
		}
		again, err := guard.Confirm(ctx, ownerA, calm.ID)
		if err != nil || len(again.Confirmation.Unusual) != 1 {
			t.Fatalf("drawn again: %+v, %v", again.Confirmation, err)
		}
		if _, err := guard.Send(ctx, ownerA, calm.ID, again.Confirmation.Token, again.Confirmation.ContentHash, []string{UnusualManySends}); err != nil {
			t.Fatalf("ticked after being shown: %v", err)
		}
	})

	t.Run("many recipients, new ones and large files are each asked about", func(t *testing.T) {
		clock.advance(2 * time.Hour)
		message := draftFor(accountA)
		message.Cc = manyRecipients().Cc
		message.Attachments = []Attachment{testAttachment("scan.pdf", UnusualAttachmentBytes+1)}
		f.history.never = map[string]bool{"persona@example.test": true, "personb@example.test": true}
		created, err := guard.Create(ctx, ownerA, ProviderJMAP, message, "")
		if err != nil {
			t.Fatal(err)
		}
		shown, err := guard.Confirm(ctx, ownerA, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]Unusual{}
		for _, point := range shown.Confirmation.Unusual {
			got[point.Code] = point
		}
		if len(got) != 3 || got[UnusualManyRecipients].Count != UnusualRecipients+1 || got[UnusualLargeAttachments].Bytes != UnusualAttachmentBytes+1 ||
			strings.Join(got[UnusualNewRecipients].Addresses, ",") != "persona@example.test,personb@example.test" {
			t.Fatalf("the points = %+v", shown.Confirmation.Unusual)
		}
		if shown.Draft.Attachments[0].Name != "scan.pdf" || shown.Draft.Attachments[0].Size != UnusualAttachmentBytes+1 || shown.Draft.Attachments[0].Origin == "" {
			t.Fatalf("the card's file = %+v", shown.Draft.Attachments)
		}
		// Two of three ticked: refused, and nothing reached the server.
		before := len(sender.sent)
		if _, err := guard.Send(ctx, ownerA, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash,
			[]string{UnusualManyRecipients, UnusualLargeAttachments}); !errors.Is(err, ErrUnusualNotConfirmed) {
			t.Fatalf("two of three ticked: %v", err)
		}
		if _, err := guard.Send(ctx, ownerA, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash,
			[]string{UnusualManyRecipients, UnusualLargeAttachments, UnusualNewRecipients}); err != nil {
			t.Fatalf("all three ticked: %v", err)
		}
		if len(sender.sent) != before+1 || len(sender.sent[before].Message.Attachments) != 1 {
			t.Fatal("the confirmed message with its file was not what was sent")
		}
		// A mailbox that cannot be asked asks about every recipient instead.
		f.history.failing = true
		defer func() { f.history.failing = false }()
		unknown, err := guard.Create(ctx, ownerA, ProviderJMAP, draftFor(accountA), "")
		if err != nil {
			t.Fatal(err)
		}
		shown, err = guard.Confirm(ctx, ownerA, unknown.ID)
		if err != nil || len(shown.Confirmation.Unusual) != 1 || shown.Confirmation.Unusual[0].Code != UnusualUncheckedRecipients {
			t.Fatalf("an unreachable history = %+v, %v", shown.Confirmation, err)
		}
	})

	t.Run("a changed file invalidates the go-ahead", func(t *testing.T) {
		clock.advance(2 * time.Hour)
		message := draftFor(accountA)
		message.Attachments = []Attachment{testAttachment("slides.pptx", 2048)}
		created, err := guard.Create(ctx, ownerA, ProviderJMAP, message, "")
		if err != nil {
			t.Fatal(err)
		}
		shown, err := guard.Confirm(ctx, ownerA, created.ID)
		if err != nil || shown.Confirmation == nil {
			t.Fatalf("confirm = %+v, %v", shown, err)
		}
		// The file's digest in the stored draft changed after the card was
		// drawn: another file is another message.
		if _, err := pool.Exec(ctx, `UPDATE mail_drafts SET message = jsonb_set(message, '{attachments,0,sha256}', to_jsonb(repeat('cd', 32))) WHERE id = $1`, created.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := guard.Send(ctx, ownerA, created.ID, shown.Confirmation.Token, shown.Confirmation.ContentHash, nil); !errors.Is(err, ErrDraftChanged) {
			t.Fatalf("a changed file: %v", err)
		}
		// The file behind the draft was replaced or removed: no go-ahead at
		// all, and the card says why.
		f.files.changed = "slides.pptx"
		defer func() { f.files.changed = "" }()
		blocked, err := guard.Confirm(ctx, ownerA, created.ID)
		if err != nil || blocked.Confirmation != nil || blocked.Draft.Blocked == nil || !strings.Contains(*blocked.Draft.Blocked, "slides.pptx") {
			t.Fatalf("a draft whose file changed = %+v, %v", blocked, err)
		}
	})

	t.Run("discarding, and a disconnected mailbox takes its drafts", func(t *testing.T) {
		dropped, err := guard.Create(ctx, ownerA, ProviderJMAP, draftFor(accountA), "")
		if err != nil {
			t.Fatal(err)
		}
		view, err := guard.Discard(ctx, ownerA, dropped.ID)
		if err != nil || view.Draft.Status != StatusDiscarded {
			t.Fatalf("discard = %+v, %v", view.Draft, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM jmap_mail_accounts WHERE id = $1`, accountA); err != nil {
			t.Fatal(err)
		}
		if _, err := guard.Confirm(ctx, ownerA, dropped.ID); !errors.Is(err, ErrNoDraft) {
			t.Errorf("a draft of a removed mailbox: %v", err)
		}
	})
}
