package accounterasure

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/documentremoval"
)

type scriptedEraser struct {
	errs  []error
	calls int
	user  string
}

func (s *scriptedEraser) EraseAccount(_ context.Context, userID string) error {
	s.calls++
	s.user = userID
	if len(s.errs) == 0 {
		return nil
	}
	err := s.errs[0]
	s.errs = s.errs[1:]
	return err
}

const account = "0b6d2a4e-3c1f-4f8e-9a51-6f7d1e2c3b4a"

func TestAnErasureIsRetriedWhenAStoreDidNotAnswer(t *testing.T) {
	eraser := &scriptedEraser{errs: []error{errors.New("qdrant: 503")}}
	job := &Job{Eraser: eraser, Attempts: 3, Backoff: time.Millisecond}
	if err := job.Run(context.Background(), &celery.Message{Args: []any{account}}); err != nil {
		t.Fatal(err)
	}
	if eraser.calls != 2 || eraser.user != account {
		t.Fatalf("%d calls for %q", eraser.calls, eraser.user)
	}
}

func TestAnActiveAccountIsNotRetried(t *testing.T) {
	eraser := &scriptedEraser{errs: []error{documentremoval.ErrStillActive}}
	job := &Job{Eraser: eraser, Attempts: 5, Backoff: time.Millisecond}
	if err := job.Run(context.Background(), &celery.Message{Args: []any{account}}); !errors.Is(err, documentremoval.ErrStillActive) {
		t.Fatalf("got %v", err)
	}
	if eraser.calls != 1 {
		t.Fatalf("an active account was asked about %d times", eraser.calls)
	}
}

func TestTheMessageNamesOneAccount(t *testing.T) {
	job := &Job{Eraser: &scriptedEraser{}, Attempts: 1}
	for _, args := range [][]any{nil, {float64(3)}, {"not-a-uuid"}, {account, account}} {
		if err := job.Run(context.Background(), &celery.Message{Args: args}); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}
