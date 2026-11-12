// Package accounterasure is the job that erases an account the person
// deleted.
//
// # The message
//
// `erase_account`, one positional argument: the account's id as text. The
// API tier's DELETE /users/me locks the account out (is_active false, every
// sign-in ended) and commits that before it publishes the message
// (soloclef_api/internal/accountprofile/delete_route.go).
//
// The erasure removes what the rows only point at (the account's vectors
// and stored files) and then the account row, from which every other row
// it owns cascades. An account that is active again by the time the job
// runs is not erased: an active account means the message is not what the
// person asked for, and erasing is not undoable.
//
// Billing is gone, so there is no payment provider's customer to forget.
package accounterasure

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/documentremoval"
)

// Task is the message's name.
const Task = "erase_account"

// Eraser erases one account (documentremoval.Removal).
type Eraser interface {
	EraseAccount(ctx context.Context, userID string) error
}

// Job runs erase_account messages.
type Job struct {
	Eraser Eraser
	// Attempts and Backoff bound the retries when a store did not answer.
	// After them the account stays locked out and partly erased, and
	// running the job again finishes it.
	Attempts int
	Backoff  time.Duration
}

// Run is the jobs.Handler.
func (j *Job) Run(ctx context.Context, msg *celery.Message) error {
	if len(msg.Args) != 1 || len(msg.Kwargs) != 0 {
		return fmt.Errorf("accounterasure: %s takes one argument, the account id; got %d positional and %d keyword",
			Task, len(msg.Args), len(msg.Kwargs))
	}
	written, isText := msg.Args[0].(string)
	parsed, err := uuid.Parse(written)
	if !isText || err != nil {
		return fmt.Errorf("accounterasure: %v is not an account id", msg.Args[0])
	}
	userID := parsed.String()
	for attempt := 1; ; attempt++ {
		err := j.Eraser.EraseAccount(ctx, userID)
		// An account still active is a refusal another attempt would only
		// repeat.
		if err == nil || attempt >= j.Attempts || ctx.Err() != nil || errors.Is(err, documentremoval.ErrStillActive) {
			return err
		}
		select {
		case <-time.After(j.Backoff * time.Duration(attempt)):
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		}
	}
}
