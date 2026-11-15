package deliverables

import (
	"context"
	"time"
)

// # Waiting for a sandbox
//
// An account has a limit of sandboxes at once (SANDBOX_MAX_PER_ACCOUNT).
// When it is reached, the run that asks for one more is either told at once
// or waits for a place, by who is watching it:
//
//   - A chat turn is told at once. A person is watching a turn that would
//     otherwise sit on "Loading instructions" with no word of why, the
//     places it would wait for are mostly other conversations' sandboxes,
//     which free only at their idle time to live (ten minutes on a typical
//     deployment), and every minute waited is taken from the specialist's
//     own twenty. The person can act on the sentence; the turn could not.
//   - A Studio output or an automation's step waits, up to
//     UnattendedSandboxWait. Nobody is watching it, and the places it waits
//     for are usually the person's other outputs, which release theirs the
//     moment they finish. Only a wait that runs out fails it, with
//     FailureSandboxBusy. While it waits, its row's phase says so.
//
// The waiting itself is the sandbox service's (soloclef_sandboxd,
// waiting.go there): one held request per waiting run, woken when a place
// frees, served in order. Nothing here polls, and nothing holds a database
// connection while it waits.

// SandboxWait is how a run waits for a sandbox when its account has none
// free. The zero value is "do not wait", a chat's.
type SandboxWait struct {
	// For is the longest the run waits for a place.
	For time.Duration
	// Waiting is told true when the run starts to wait and false when it
	// stops, whether or not it got a place, so that what the run shows can
	// say so. It may be nil. It is called with a context that outlives a
	// cancel, so the "stopped waiting" write is made even then.
	Waiting func(ctx context.Context, waiting bool)
}

// tell reports a start or an end of waiting to whoever asked.
func (w SandboxWait) tell(ctx context.Context, waiting bool) {
	if w.Waiting != nil {
		w.Waiting(ctx, waiting)
	}
}
