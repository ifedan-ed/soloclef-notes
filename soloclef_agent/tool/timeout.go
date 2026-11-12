package tool

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// WithTimeout returns the tool with a limit on how long one call may run.
//
// A call that passes the limit ends with an error the model can read, and the
// turn goes on: one stuck remote server should cost one tool result, not the
// conversation. This is also the whole pattern for putting anything around a
// tool call (logging, an audit row, moving an oversized result elsewhere):
// copy the tool, replace Run.
func WithTimeout(t Tool, limit time.Duration) (Tool, error) {
	if t.Name == "" {
		return Tool{}, ErrMissingName
	}
	if t.Run == nil {
		return Tool{}, fmt.Errorf("tool %s has no Run function to put a time limit on", t.Name)
	}
	if limit <= 0 {
		return Tool{}, fmt.Errorf("tool %s: a time limit must be positive, got %s", t.Name, limit)
	}
	run := t.Run
	t.Run = func(ctx context.Context, call Call) (string, error) {
		limited, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		out, err := run(limited, call)
		// Only our own deadline becomes a tool error. If the caller's context
		// ended too, the run is being stopped and the loop must see that, not
		// a timeout it would feed to the model.
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return "", fmt.Errorf("%s did not finish within %s", t.Name, limit)
		}
		return out, err
	}
	return t, nil
}
