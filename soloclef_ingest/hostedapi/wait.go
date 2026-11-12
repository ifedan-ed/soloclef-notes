package hostedapi

import (
	"context"
	"time"
)

// Wait pauses between two polls of a job, and stops early when the context
// ends so a shutdown or a deadline is not held up by a sleep.
func Wait(ctx context.Context, pause time.Duration) error {
	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
