package drain_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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
	return drain.Sender{Index: 2, Store: store, Mailer: m, Clock: func() float64 { return now }}
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
