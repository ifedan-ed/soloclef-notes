// Package tool is what the model can ask for: a name, a schema, a function,
// and the two facts the loop needs before running it, whether a person must
// approve it and whether it may run beside other calls.
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
)

// Approval says whether a person must agree before a tool runs. It has no
// usable zero value: a tool that forgot to say is refused when the agent is
// built, because the safe reading (ask) and the convenient one (run) are both
// wrong for half the tools, and a guess here is a tool running unasked.
type Approval uint8

const (
	// Auto runs without asking. For tools that only read.
	Auto Approval = iota + 1

	// Ask asks first, unless the agent's Trusted function says this call is
	// trusted or the user answered "always allow" earlier in the same turn.
	Ask

	// AlwaysAsk asks every time. No trust list and no earlier answer can
	// waive it, and an "always allow" answer to it is refused. For tools that
	// send data somewhere the model chose, where one approval says nothing
	// about the next call.
	AlwaysAsk
)

// Tool is one tool. It is a struct of plain fields, not an interface, so that
// wrapping a tool is copying it and replacing Run; see WithTimeout.
type Tool struct {
	// Name is what the model calls the tool. It must be 1 to 64 characters
	// of letters, digits, underscore and hyphen: that is what providers
	// accept, and a name outside it fails the whole request at the provider
	// with an error that does not say which tool was at fault.
	Name        string
	Description string

	// Parameters is the JSON Schema of the arguments, an object schema.
	Parameters json.RawMessage

	Approval Approval

	// Parallel says the tool is safe to run at the same time as other calls
	// of the same batch: it reads, or its writes cannot collide. Calls that
	// are not Parallel run alone, in the order the model gave them, because
	// "write the file, then edit it" asked for in one batch means that order.
	// The zero value is the safe one.
	Parallel bool

	// Run does the work. The string it returns is what the model reads.
	//
	// An error is also shown to the model, as a failed result, and the turn
	// goes on: models recover from a failed tool, and a turn that dies on one
	// bad call loses everything before it. Two things are not shown to the
	// model. If the run's context is cancelled, the run stops. And the error
	// from AskUser pauses the run for an answer.
	Run Func
}

// Func runs one call: the type of Tool.Run, and of the next step a hook
// around tool calls is handed.
type Func func(ctx context.Context, call Call) (string, error)

// Call is one invocation of a tool.
type Call struct {
	// ID is the call's id, unique across the run; see event.Event.CallID.
	ID   string
	Name string

	// Args is the argument text. The loop has checked it is a JSON object, so
	// a tool can unmarshal it without first asking whether it is JSON at all.
	Args json.RawMessage

	// Thread is the thread the calling agent is running on.
	Thread string

	// Answer is nil the first time a call runs. It is set when the tool
	// returned AskUser on an earlier run of this same call and a person has now
	// answered: the tool runs again from its first line with the answer here.
	Answer json.RawMessage

	// Citations is the thread's citation registry. A tool that shows the
	// model a source it may cite numbers the source here and labels it with
	// that number. The loop sets it on every call, and a nested agent's calls
	// get the same registry as the agent that called it, so every label in a
	// conversation, whichever agent wrote it, comes from one sequence. It is
	// saved with the thread, so the numbering survives a pause and a restart.
	Citations *citation.Registry

	// Notify sends the client a notice about this call while it runs: a
	// progress line, a job id, a file that changed. name says what kind of
	// notice it is and payload is encoded as JSON; both are between the tool
	// and the client, and the model never sees either. The run reports it
	// as an event.Notice carrying this call's id, and from inside a nested
	// agent it reaches the outermost run's stream like every other event of
	// that agent. It fails only if payload cannot be encoded.
	//
	// The loop sets it on every call. It may be called from any goroutine
	// the tool starts, but not after Run has returned: by then the run may
	// have ended and its stream with it.
	Notify func(name string, payload any) error
}

// ValidName reports whether name is one every provider accepts for a tool:
// 1 to 64 characters of letters, digits, underscore and hyphen. It is a loop
// over bytes rather than a regular expression because an agent is built per
// turn and checks every tool name each time.
func ValidName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// Validate reports the first thing wrong with a tool. The agent calls it for
// every tool when it is built, so a bad tool is an error at start-up and not
// a refused request in the middle of someone's conversation.
func (t Tool) Validate() error {
	if !ValidName(t.Name) {
		return fmt.Errorf("tool name %q must be 1 to 64 characters of letters, digits, underscore and hyphen", t.Name)
	}
	if t.Run == nil {
		return fmt.Errorf("tool %s has no Run function", t.Name)
	}
	if t.Approval < Auto || t.Approval > AlwaysAsk {
		return fmt.Errorf("tool %s does not say whether it needs approval: set Approval to tool.Auto, tool.Ask or tool.AlwaysAsk", t.Name)
	}
	// Checked for being a JSON object, not decoded: decoding forty schemas
	// into maps on every turn would cost more than everything else New does.
	if trimmed := bytes.TrimLeft(t.Parameters, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return fmt.Errorf("tool %s: Parameters must be a JSON Schema object", t.Name)
	}
	return nil
}

// ErrMissingName is returned by helpers that are handed a tool with no name,
// which is nearly always a zero Tool passed by mistake.
var ErrMissingName = errors.New("tool has no name")
