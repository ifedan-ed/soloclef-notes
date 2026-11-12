package agent

import (
	"context"
	"errors"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// Hooks are how behaviour is put around the two things a turn does: call the
// model, and run a tool. The Python chat hung twenty-six add-ons on its loop
// for this, each in a numbered slot whose order mattered and was written
// down nowhere but the list. Here a hook is one function that is handed the
// call and the next step, and does whatever it likes before and after
// calling it, or instead of calling it. The order is the order of the slice,
// the first hook outermost, which is all there is to know.
//
// What is deliberately not a hook:
//
//   - Changing what the model is shown without storing it (a memory block,
//     attached documents, trimming old tool results) is Config.Prepare: one
//     function, because there is one conversation to prepare.
//   - Deciding whether a call needs a person is a tool's Approval and
//     Config.Trusted, because that order of checks is a security property
//     and must not depend on how hooks happen to be stacked.
//   - Retrying a failed request belongs to the endpoint, not the turn, and
//     is model.WithRetry around the model.
//
// Hooks are called from the goroutines that run the turn, and tool hooks run
// side by side for calls that run in parallel, so a hook that keeps state
// must lock it.

// ModelCall is one model call of a turn, as a model hook sees it.
type ModelCall struct {
	// Thread and Step say where in which conversation the call is made. A
	// delegate's calls carry the delegate's own thread, which is named after
	// its parent's.
	Thread string
	Step   int

	// Request is what will be sent. A hook may send something else by
	// passing a changed copy to next; it must not modify the slices it was
	// given, which are the turn's own.
	Request model.Request

	// Stream receives the answer's fragments as they arrive. A hook may
	// replace it to watch or filter them.
	Stream func(model.Chunk)
}

// ModelFunc makes a model call: the agent's model at the bottom of the
// stack, or the next hook above it.
type ModelFunc func(ctx context.Context, call ModelCall) (model.Response, error)

// ModelHook is a function put around every model call an agent makes. It
// calls next to make the call, or returns without calling it.
type ModelHook func(ctx context.Context, call ModelCall, next ModelFunc) (model.Response, error)

// ToolHook is a function put around every tool call an agent runs, and every
// call to a delegate. It calls next to run the call, possibly with changed
// arguments; it may return its own result instead, or an error, which the
// model is shown as a failed result, as for any tool.
//
// A hook can also pause the turn to ask a person something, exactly as a tool
// can, by returning tool.AskUser: the call is run again, hooks and all, with
// Call.Answer set. That is how a "the model has made this same call three
// times, carry on?" check is written, with no machinery of its own.
//
// Two errors that come back from next are not failures and must be returned
// as they are, never swallowed or replaced: a question (tool.AskUser) and a
// delegate that is waiting for approval. IsPause recognises both.
type ToolHook func(ctx context.Context, call tool.Call, next tool.Func) (string, error)

// errDelegateWaiting is how a delegate call says, through any hooks around
// it, that its agent paused for approval. It is not an error to show
// anybody; the tool step turns it into a call that waits.
var errDelegateWaiting = errors.New("agent: the delegate is waiting for approval")

// IsPause reports whether err is a call pausing the turn rather than failing:
// a question asked with tool.AskUser, or a delegate waiting for approval. A
// tool hook that handles errors must pass these through untouched.
func IsPause(err error) bool {
	_, asked := tool.AsQuestion(err)
	return asked || errors.Is(err, errDelegateWaiting)
}

// chainModel stacks the hooks over the model once, when the agent is built:
// the stack is the same for every call the agent makes.
func chainModel(m model.Model, hooks []ModelHook) ModelFunc {
	next := func(ctx context.Context, call ModelCall) (model.Response, error) {
		return m.Generate(ctx, call.Request, call.Stream)
	}
	for i := len(hooks) - 1; i >= 0; i-- {
		hook, inner := hooks[i], next
		next = func(ctx context.Context, call ModelCall) (model.Response, error) {
			return hook(ctx, call, inner)
		}
	}
	return next
}

// chainTool stacks the hooks over one call's own work. Unlike the model's
// stack it is built per call, because what is at the bottom differs: a
// tool's Run, or the run of a delegate's agent for this very call. With no
// hooks it is the bottom function itself, at no cost.
func chainTool(base tool.Func, hooks []ToolHook) tool.Func {
	next := base
	for i := len(hooks) - 1; i >= 0; i-- {
		hook, inner := hooks[i], next
		next = func(ctx context.Context, call tool.Call) (string, error) {
			return hook(ctx, call, inner)
		}
	}
	return next
}
