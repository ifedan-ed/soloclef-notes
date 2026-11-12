// Package checkpoint is where a run's state is kept between steps, behind an
// interface small enough to implement in an afternoon.
//
// A thread has one checkpoint: the latest. Saving replaces it. There is no
// history of earlier steps, on purpose. A history is what makes the stock
// checkpointers grow without bound, and nothing here needs one: resuming
// needs the newest state, and "go back to an earlier message" is an edit to a
// conversation, which the agent package offers as exactly that.
package checkpoint

import (
	"context"
	"errors"
)

// Status says where a thread stands.
type Status string

const (
	// Running means a turn has finished at least one step and not its last.
	// A thread is left like this by a turn in flight, and equally by one that
	// was cancelled, crashed, or hit its step limit: from the store alone the
	// four look the same, and which it is belongs to whoever tracks turns.
	Running Status = "running"

	// Paused means the turn is waiting for a person.
	Paused Status = "paused"

	// Done means the last turn finished.
	Done Status = "done"
)

// Checkpoint is one thread's saved position.
type Checkpoint struct {
	Thread string

	// Seq counts saves, from 1. It is how two processes are kept from
	// running the same thread: see Store.Save.
	Seq int64

	Status Status

	// Node is the step that runs next, or empty when Status is Done.
	Node string

	// State is the run's state, encoded by whoever owns the thread. The store
	// does not look inside it.
	State []byte
}

var (
	// ErrNotFound means the thread has never been saved.
	ErrNotFound = errors.New("checkpoint: thread not found")

	// ErrConflict means another writer saved the thread first. The caller's
	// copy of the state is stale and the run that holds it must stop: two
	// runs on one thread would each append to a conversation the other
	// cannot see.
	ErrConflict = errors.New("checkpoint: thread was changed by another run")
)

// Store keeps the latest checkpoint of each thread.
//
// Implementations must be safe for use by many goroutines: tools of one batch
// run side by side, and a nested agent saves its own thread from one of them.
type Store interface {
	// Load returns the thread's checkpoint, or ErrNotFound.
	Load(ctx context.Context, thread string) (Checkpoint, error)

	// Save stores cp as the thread's checkpoint if, and only if, cp.Seq is
	// one more than the stored Seq (or 1 for a thread never saved).
	// Otherwise it changes nothing and returns ErrConflict.
	//
	// That one rule is a lock that works across processes and survives a
	// crash: a run reads Seq when it loads, and its saves succeed only while
	// nobody else has saved in between. There is no lock to leak when a
	// process dies holding it.
	Save(ctx context.Context, cp Checkpoint) error

	// Delete removes the thread's checkpoint. Deleting a thread that does not
	// exist is not an error.
	Delete(ctx context.Context, thread string) error

	// DeleteTree removes the thread's checkpoint and the checkpoint of every
	// thread nested under it: every thread whose name is this thread's
	// followed by "/" and anything. That is how a delegate's thread is named
	// after its parent's (agent.Delegate), to any depth, so this is "forget
	// this conversation and everything it started".
	//
	// It exists beside Delete because the caller that needs it has only the
	// name. Finding the nested threads from the parent's state needs the
	// agent that wrote it, and a nested thread left by a crash may not be in
	// that state at all. A thread whose name merely begins the same way
	// ("chat-1" and "chat-10") is not nested and is not touched. Deleting a
	// tree that does not exist is not an error.
	DeleteTree(ctx context.Context, thread string) error
}
