package drain_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupe"
	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupe/dedupetest"
	"git.danvics.com/danvics/soloclef/soloclef_mail/drain"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailjob"
)

type move struct {
	what string
	raw  string
	next string
	due  float64
}

type fakeStore struct {
	moves []move
	fail  error
}

func (s *fakeStore) Take(context.Context, int, time.Duration) (string, bool, error) {
	return "", false, nil
}

func (s *fakeStore) Done(_ context.Context, _ int, raw string) error {
	s.moves = append(s.moves, move{what: "done", raw: raw})
	return s.fail
}

func (s *fakeStore) RetryLater(_ context.Context, _ int, raw, next string, due float64) error {
	s.moves = append(s.moves, move{what: "retry", raw: raw, next: next, due: due})
	return s.fail
}

type fakeMailer struct {
	err      error
	sent     []mailer.Message
	closed   bool
	onSend   func(mailer.Message) error
	sendCall int
}

func (m *fakeMailer) Send(_ context.Context, message mailer.Message) error {
	m.sendCall++
	m.sent = append(m.sent, message)
	if m.onSend != nil {
		return m.onSend(message)
	}
	return m.err
}

func (m *fakeMailer) Close() error {
	m.closed = true
	return nil
}

const now = 1762608000.0

func aJob(t *testing.T, change func(*mailjob.Job)) (mailjob.Job, string) {
	t.Helper()
	job := mailjob.Job{
		Message:    mailjob.Message{To: "person@queue.test", Subject: "s", TextBody: "t"},
		ID:         "0123456789abcdef0123456789abcdef",
		Kind:       "password_reset",
		Ref:        "41",
		EnqueuedAt: now,
	}
	if change != nil {
		change(&job)
	}
	raw, err := job.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return job, raw
}

func aSender(store drain.Store, m mailer.Mailer) drain.Sender {
	return guardedSender(store, m, dedupe.HandoverGuard{Keys: dedupetest.NewMemory()}, 2)
}

func guardedSender(store drain.Store, m mailer.Mailer, guard drain.Guard, index int) drain.Sender {
	return drain.Sender{Index: index, Store: store, Mailer: m, Guard: guard, Clock: func() float64 { return now }}
}

func TestASentMessageIsFinishedWith(t *testing.T) {
	store := &fakeStore{}
	post := &fakeMailer{}
	_, raw := aJob(t, nil)

	outcome, err := aSender(store, post).Handle(context.Background(), raw)

	if err != nil || outcome != drain.Sent {
		t.Fatalf("got %q %v", outcome, err)
	}
	if len(store.moves) != 1 || store.moves[0].what != "done" || store.moves[0].raw != raw {
		t.Errorf("the held copy was not dropped: %+v", store.moves)
	}
	if len(post.sent) != 1 || post.sent[0].To != "person@queue.test" {
		t.Errorf("sent %+v", post.sent)
	}
}

func TestAnExpiredMessageIsDroppedBeforeAnythingIsSent(t *testing.T) {
	store := &fakeStore{}
	post := &fakeMailer{}
	deadline := now - 1
	_, raw := aJob(t, func(j *mailjob.Job) { j.NotAfter = &deadline })

	outcome, err := aSender(store, post).Handle(context.Background(), raw)

	if err != nil || outcome != drain.Expired {
		t.Fatalf("got %q %v", outcome, err)
	}
	if post.sendCall != 0 {
		t.Error("a message whose link has expired reached the mail server")
	}
	if len(store.moves) != 1 || store.moves[0].what != "done" {
		t.Errorf("%+v", store.moves)
	}
}

func TestAPermanentRefusalIsNotRetried(t *testing.T) {
	for name, cause := range map[string]error{
		"recipient refused": fmt.Errorf("%w: 550", mailer.ErrRejected),
		"our settings":      fmt.Errorf("%w: key", mailer.ErrMisconfigured),
		"a bug in sending":  errors.New("nil map"),
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeStore{}
			_, raw := aJob(t, nil)

			outcome, err := aSender(store, &fakeMailer{err: cause}).Handle(context.Background(), raw)

			if err != nil || outcome != drain.Failed {
				t.Fatalf("got %q %v", outcome, err)
			}
			if len(store.moves) != 1 || store.moves[0].what != "done" {
				t.Errorf("it was put on the retry set: %+v", store.moves)
			}
		})
	}
}

func TestATransientRefusalComesBackAtTheRightTimeOneTryOn(t *testing.T) {
	store := &fakeStore{}
	_, raw := aJob(t, nil)

	outcome, err := aSender(store, &fakeMailer{err: fmt.Errorf("%w: 421", mailer.ErrUnavailable)}).
		Handle(context.Background(), raw)

	if err != nil || outcome != drain.Retry {
		t.Fatalf("got %q %v", outcome, err)
	}
	if len(store.moves) != 1 || store.moves[0].what != "retry" {
		t.Fatalf("%+v", store.moves)
	}
	if store.moves[0].due != now+5 {
		t.Errorf("due at %v, the first delay is five seconds", store.moves[0].due-now)
	}
	if !strings.Contains(store.moves[0].next, `"attempt":1`) {
		t.Errorf("the retry does not count the failure: %s", store.moves[0].next)
	}
	if !strings.Contains(store.moves[0].next, `"id":"0123456789abcdef0123456789abcdef"`) {
		t.Error("the retry carries a different id, so its log lines no longer join up")
	}
}

func TestTheSeventhFailureIsTheEndOfIt(t *testing.T) {
	store := &fakeStore{}
	_, raw := aJob(t, func(j *mailjob.Job) { j.Attempt = 6 })

	outcome, err := aSender(store, &fakeMailer{err: fmt.Errorf("%w: 421", mailer.ErrUnavailable)}).
		Handle(context.Background(), raw)

	if err != nil || outcome != drain.GaveUp {
		t.Fatalf("got %q %v", outcome, err)
	}
	if store.moves[0].what != "done" {
		t.Errorf("%+v", store.moves)
	}
}

func TestAMessageThatWouldExpireBeforeItsRetryIsDropped(t *testing.T) {
	store := &fakeStore{}
	deadline := now + 3
	_, raw := aJob(t, func(j *mailjob.Job) { j.NotAfter = &deadline })

	outcome, err := aSender(store, &fakeMailer{err: fmt.Errorf("%w: 421", mailer.ErrUnavailable)}).
		Handle(context.Background(), raw)

	if err != nil || outcome != drain.Expired {
		t.Fatalf("got %q %v", outcome, err)
	}
	if store.moves[0].what != "retry" {
		return
	}
	t.Error("it was queued for a retry that would arrive after the link had expired")
}

func TestAnUnreadableEntryIsDroppedRatherThanSent(t *testing.T) {
	store := &fakeStore{}
	post := &fakeMailer{}

	outcome, err := aSender(store, post).Handle(context.Background(), "{not json")

	if err != nil || outcome != drain.Unreadable {
		t.Fatalf("got %q %v", outcome, err)
	}
	if post.sendCall != 0 {
		t.Error("something was sent from an entry nobody could read")
	}
	if len(store.moves) != 1 || store.moves[0].what != "done" {
		t.Errorf("%+v", store.moves)
	}
}

func TestOneMessageIsOneSend(t *testing.T) {
	// Nothing dedupes, here or in Python, so the one thing this loop must
	// never do is hand the same message to the mailer twice.
	store := &fakeStore{}
	post := &fakeMailer{}
	_, raw := aJob(t, nil)

	for range 3 {
		if _, err := aSender(store, post).Handle(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
	}
	if post.sendCall != 3 {
		t.Fatalf("three drains made %d sends", post.sendCall)
	}
}

func TestTheMailerIsLetGoWhenTheSenderStops(t *testing.T) {
	post := &fakeMailer{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := aSender(&fakeStore{}, post).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !post.closed {
		t.Error("the connection was left open")
	}
}

// The send guard: a message handed over once is not handed over twice.
//
// This is the failure the queue could not fix at the push, because the message
// was queued exactly once -- a sender dies between giving the message to the
// mail server and dropping it from its held list, and the next run puts it
// back. The trap it must not fall into is the retry, which re-queues the same
// mail id at the next attempt.

func TestASentMessageIsMarkedAsHandedOver(t *testing.T) {
	guard := dedupe.HandoverGuard{Keys: dedupetest.NewMemory()}
	job, raw := aJob(t, nil)
	ctx := context.Background()

	if _, err := guardedSender(&fakeStore{}, &fakeMailer{}, guard, 2).Handle(ctx, raw); err != nil {
		t.Fatal(err)
	}

	if held, _ := guard.Claim(ctx, job.ID, job.Attempt); held != dedupe.Sent {
		t.Errorf("the mail server took it and nothing recorded that: %q", held)
	}
}

func TestTheSameAttemptPutBackByARestartIsNotSentAgain(t *testing.T) {
	guard := dedupe.HandoverGuard{Keys: dedupetest.NewMemory()}
	store := &fakeStore{}
	post := &fakeMailer{}
	_, raw := aJob(t, nil)
	sender := guardedSender(store, post, guard, 2)
	ctx := context.Background()

	if outcome, err := sender.Handle(ctx, raw); err != nil || outcome != drain.Sent {
		t.Fatalf("got %q %v", outcome, err)
	}
	outcome, err := sender.Handle(ctx, raw)
	if err != nil || outcome != drain.Duplicate {
		t.Fatalf("got %q %v", outcome, err)
	}
	if post.sendCall != 1 {
		t.Errorf("the mail went out %d times", post.sendCall)
	}
	if len(store.moves) != 2 || store.moves[1].what != "done" {
		t.Errorf("the duplicate was not finished with: %+v", store.moves)
	}
}

func TestARetryOfAFailedJobStillSends(t *testing.T) {
	guard := dedupe.HandoverGuard{Keys: dedupetest.NewMemory()}
	job, raw := aJob(t, nil)
	ctx := context.Background()

	failing := guardedSender(&fakeStore{}, &fakeMailer{err: fmt.Errorf("%w: 451 later", mailer.ErrUnavailable)}, guard, 0)
	if outcome, err := failing.Handle(ctx, raw); err != nil || outcome != drain.Retry {
		t.Fatalf("got %q %v", outcome, err)
	}

	next, err := job.NextAttempt().Encode()
	if err != nil {
		t.Fatal(err)
	}
	working := &fakeMailer{}
	if outcome, err := guardedSender(&fakeStore{}, working, guard, 1).Handle(ctx, next); err != nil || outcome != drain.Sent {
		t.Fatalf("a legitimate retry was swallowed: %q %v", outcome, err)
	}
	if working.sendCall != 1 {
		t.Errorf("the retry reached the mail server %d times", working.sendCall)
	}
}

func TestTwoSendersRacingOneMessageSendItOnce(t *testing.T) {
	guard := dedupe.HandoverGuard{Keys: dedupetest.NewMemory()}
	_, raw := aJob(t, nil)
	one, two := &fakeMailer{}, &fakeMailer{}
	ctx := context.Background()

	outcomes := make(chan drain.Outcome, 2)
	for i, post := range []*fakeMailer{one, two} {
		go func(index int, post *fakeMailer) {
			outcome, _ := guardedSender(&fakeStore{}, post, guard, index).Handle(ctx, raw)
			outcomes <- outcome
		}(i, post)
	}
	seen := map[drain.Outcome]int{}
	for range 2 {
		seen[<-outcomes]++
	}

	if one.sendCall+two.sendCall != 1 {
		t.Errorf("one message reached the mail server %d times", one.sendCall+two.sendCall)
	}
	if seen[drain.Sent] != 1 || seen[drain.Duplicate] != 1 {
		t.Errorf("outcomes %v", seen)
	}
}

func TestAMessageWhoseClaimWasReleasedIsSentAgain(t *testing.T) {
	guard := dedupe.HandoverGuard{Keys: dedupetest.NewMemory()}
	job, raw := aJob(t, nil)
	ctx := context.Background()
	if _, err := guard.Claim(ctx, job.ID, job.Attempt); err != nil {
		t.Fatal(err)
	}
	if err := guard.Release(ctx, job.ID, job.Attempt); err != nil {
		t.Fatal(err)
	}

	post := &fakeMailer{}
	if outcome, err := guardedSender(&fakeStore{}, post, guard, 2).Handle(ctx, raw); err != nil || outcome != drain.Sent {
		t.Fatalf("got %q %v", outcome, err)
	}
	if post.sendCall != 1 {
		t.Errorf("sent %d times", post.sendCall)
	}
}

func TestAnExpiredMessageNeverTakesAClaim(t *testing.T) {
	guard := dedupe.HandoverGuard{Keys: dedupetest.NewMemory()}
	deadline := now - 1
	job, raw := aJob(t, func(j *mailjob.Job) { j.NotAfter = &deadline })
	ctx := context.Background()

	if outcome, _ := guardedSender(&fakeStore{}, &fakeMailer{}, guard, 2).Handle(ctx, raw); outcome != drain.Expired {
		t.Fatalf("got %q", outcome)
	}
	if held, _ := guard.Claim(ctx, job.ID, job.Attempt); held != "" {
		t.Errorf("a message dropped unsent took a claim: %q", held)
	}
}
