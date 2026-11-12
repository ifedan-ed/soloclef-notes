// Package event is what a run says while it runs: one flat, typed stream that
// a chat layer turns into whatever its client speaks.
//
// The stream is the library's only output channel during a run. Nothing here
// knows about HTTP, SSE or any client protocol; package uistream is one
// translation, kept apart so the protocol can change without touching the
// loop.
package event

import "git.danvics.com/danvics/soloclef/soloclef_agent/model"

// Kind says what happened. The order of the constants is the order events of
// one step normally arrive in.
type Kind uint8

const (
	// StepStarted opens a step. Step and Node are set.
	StepStarted Kind = iota + 1

	// TextDelta is a fragment of the answer, in Text.
	TextDelta

	// ReasoningDelta is a fragment of the model's reasoning, in Text.
	ReasoningDelta

	// ToolCallStarted says the model has begun asking for a tool. CallID and
	// Tool are set; the arguments are still arriving.
	ToolCallStarted

	// ToolCallDelta is a fragment of a call's argument text, in Text.
	ToolCallDelta

	// ToolCallReady says a call's arguments are complete. CallID, Tool and
	// Args are set. Every call gets exactly one, after its ToolCallStarted
	// and before its ToolResult, whether or not the model streamed it.
	ToolCallReady

	// ToolResult is the outcome of one call: CallID, Tool, the text the model
	// will be shown in Text, and IsError.
	ToolResult

	// Notice is something a tool said about its own work while it ran, for
	// the client and not for the model: "rendering slide 3 of 12", a file
	// that changed, a job id to poll. CallID and Tool name the call, Name
	// says what kind of notice it is and Payload is its JSON, both chosen by
	// the tool (tool.Call.Notify). The loop passes it through and gives it
	// no meaning. It is how a long-running tool inside a nested agent, such
	// as the one that builds a podcast, shows progress on the page while its
	// call is still running.
	Notice

	// ApprovalNeeded says a call is waiting for a person. CallID, Tool and
	// Args are set. For a tool that is gated, AllowAlways says whether an
	// "always allow" answer would be honoured. For a tool that asked a
	// question from inside its own body, Payload is that question.
	//
	// They are sent together, once the paused turn is saved and just before
	// Paused, by the run the caller started: one per waiting call in the
	// whole tree of nested agents, in the order Pending lists them. A call
	// inside a delegate has Parent set like any event from inside one.
	ApprovalNeeded

	// ModelFinished closes one model call: Usage and FinishReason are set.
	ModelFinished

	// StepFinished closes a step, after its state has been saved. An observer
	// that has seen StepFinished knows a restart would resume after it.
	StepFinished

	// Paused, Done and Error end a run; exactly one of them is the last
	// event of every run. A nested agent's run ends with one too, and that
	// event reaches the sink with Parent set, so a consumer that acts on the
	// end of the turn checks that Parent is empty.

	// Paused ends a run that is waiting for approvals.
	Paused

	// Done ends a run that finished.
	Done

	// Error ends a run that failed, with the cause in Err.
	Error
)

var kindNames = [...]string{
	StepStarted:     "step-started",
	TextDelta:       "text-delta",
	ReasoningDelta:  "reasoning-delta",
	ToolCallStarted: "tool-call-started",
	ToolCallDelta:   "tool-call-delta",
	ToolCallReady:   "tool-call-ready",
	ToolResult:      "tool-result",
	Notice:          "notice",
	ApprovalNeeded:  "approval-needed",
	ModelFinished:   "model-finished",
	StepFinished:    "step-finished",
	Paused:          "paused",
	Done:            "done",
	Error:           "error",
}

func (k Kind) String() string {
	if int(k) < len(kindNames) && kindNames[k] != "" {
		return kindNames[k]
	}
	return "unknown"
}

// Event is one thing a run reports. It is a single struct passed by value,
// not an interface with a type per kind: the text delta is the hot path of a
// chat (thousands per answer), and a value type reaches the sink without an
// allocation. The price is that only the fields named in a Kind's comment
// mean anything for that kind; the rest are zero.
type Event struct {
	Kind Kind

	// Parent is empty for the run the caller started. For an event from a
	// nested agent it is the CallID of the call that delegated to it, so a
	// consumer can tell the answer from a specialist's working (which a chat
	// does not print) and can group a specialist's steps under its card.
	Parent string

	// Step counts the steps of the turn from 1. Node names the step's kind:
	// for the agent loop "model" or "tools", for a graph the node's name.
	Step int
	Node string

	Text string

	// CallID identifies a tool call across the whole tree of a run. For a
	// call made by a nested agent it is the delegating call's id, a slash,
	// and the call's own id, which is what makes it unique when two
	// specialists' models happen to pick the same id. It is the id an
	// approval is answered to.
	CallID string
	Tool   string

	// Args is the call's argument text as the model wrote it. It is valid
	// JSON whenever a tool is going to run; a call whose text is not valid
	// JSON is reported here as written and then answered with an error
	// result.
	Args string

	IsError     bool
	AllowAlways bool

	// Name is the kind of a Notice, as the tool named it.
	Name string

	// Payload is JSON: a tool's question on ApprovalNeeded, a tool's notice
	// on Notice.
	Payload []byte

	Usage        model.Usage
	FinishReason string

	Err error
}

// Sink receives a run's events. The run calls it from one goroutine at a
// time, in order, and waits for it to return: a slow sink slows the run
// rather than letting events pile up in memory, and a sink needs no lock of
// its own even while tools run side by side.
//
// A sink cannot fail. A consumer whose client has gone away cancels the run's
// context if it wants the run to stop, and keeps consuming if it wants the
// turn to finish for a reader who will come back.
type Sink func(Event)

// Discard is the sink for a run nobody is watching. It exists so that
// "nobody is watching" is written at the call site instead of being what a
// nil sink quietly means.
func Discard(Event) {}
