package agent

import (
	"encoding/json"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// State is everything saved for a thread. It is plain data with exported
// fields because it is the caller's conversation: Load returns it and Edit
// lets a caller change it, with no private channel to read around.
type State struct {
	// Messages is the conversation as the model will be shown it, without
	// the system prompt and without anything Config.Prepare adds.
	Messages []model.Message `json:"messages"`

	// Batch is the tool calls of the last assistant message, kept here until
	// every one has a result. Results are held in the batch and written to
	// Messages together, in the order of the calls, because the API wants
	// each call answered once and a half-answered batch in Messages would be
	// a conversation no provider accepts.
	//
	// Holding them here is also what lets a turn pause partway through a
	// batch: the calls that ran keep their results, and are not run again
	// when the turn resumes.
	Batch []Call `json:"batch,omitempty"`

	// Steps is how many steps the current turn has used.
	Steps int `json:"steps"`

	// TurnTrust lists the tools the user answered "always allow" for during
	// the current turn. It is cleared when a new turn starts: trust that
	// lasts longer than a turn is the caller's to store and to revoke, and
	// reaches the loop through Config.Trusted.
	TurnTrust []string `json:"turn_trust,omitempty"`

	// Citations are the sources the thread's tools have numbered, in number
	// order; see citation.Registry. They are kept for the thread's whole
	// life, not per turn, because the conversation the model reads still
	// holds the labels earlier turns wrote, and a "[1]" must not come to
	// mean a second source. A delegate's thread keeps a copy of the same
	// list as it stood when the delegate last saved, so a delegate picked
	// up after a crash restores numbers it gave out that its parent never
	// saved.
	Citations []citation.Source `json:"citations,omitempty"`
}

// CallStatus is where one tool call of a batch stands.
type CallStatus string

const (
	// CallPending has not been looked at yet.
	CallPending CallStatus = "pending"

	// CallAwaitingApproval is gated and waiting for a person to approve or
	// reject it.
	CallAwaitingApproval CallStatus = "awaiting_approval"

	// CallApproved may run: it was not gated, or was trusted, or a person
	// approved it.
	CallApproved CallStatus = "approved"

	// CallAwaitingAnswer ran, asked a question with tool.AskUser, and is
	// waiting for the answer.
	CallAwaitingAnswer CallStatus = "awaiting_answer"

	// CallAnswered has its answer and will run again with it.
	CallAnswered CallStatus = "answered"

	// CallDelegated is a delegate whose agent has paused for approvals of
	// its own.
	CallDelegated CallStatus = "delegated"

	// CallRejected was refused by a person. It is a status of its own, and
	// not CallDone, only so that the tool step is what announces the result:
	// a resume records the refusal before it has claimed the thread, and an
	// event sent then could describe a resume that is about to be refused.
	CallRejected CallStatus = "rejected"

	// CallDone has its result.
	CallDone CallStatus = "done"
)

// Call is one tool call of the current batch.
type Call struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// Args is the argument text. It starts as the model wrote it and is
	// replaced if a person edits the call while approving it.
	Args string `json:"args"`

	Status CallStatus `json:"status"`

	// Result and IsError are set when Status is CallDone.
	Result  string `json:"result,omitempty"`
	IsError bool   `json:"is_error,omitempty"`

	// Question is the tool's question while Status is CallAwaitingAnswer,
	// and Answer the reply while it is CallAnswered.
	Question json.RawMessage `json:"question,omitempty"`
	Answer   json.RawMessage `json:"answer,omitempty"`

	// Forward holds decisions addressed to calls inside a delegate, from the
	// moment a resume accepts them until the delegate's agent has taken
	// them. They are saved with the state so that a crash between those two
	// moments does not lose answers a person has already given.
	Forward []Decision `json:"forward,omitempty"`
}

// encode and decode are the only places the state's stored form is decided.
// It is JSON because a stored thread should be readable with psql and jq when
// something has gone wrong, and the cost (measured in the benchmarks) is
// microseconds against a model call's seconds.
func (s *State) encode() ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("agent: encode state: %w", err)
	}
	return raw, nil
}

func decodeState(raw []byte) (State, error) {
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return State{}, fmt.Errorf("agent: stored state is not readable: %w", err)
	}
	return s, nil
}

// answer returns the text of the last assistant message: what a finished turn
// said, and what a delegate hands back to the agent that called it.
func (s *State) answer() string {
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if s.Messages[i].Role == model.RoleAssistant {
			return s.Messages[i].Content
		}
	}
	return ""
}

// flushBatch moves a finished batch into the conversation as tool messages,
// one per call in the order the model made them.
func (s *State) flushBatch() {
	for i := range s.Batch {
		c := &s.Batch[i]
		s.Messages = append(s.Messages, model.Message{Role: model.RoleTool, ToolCallID: c.ID, Content: c.Result})
	}
	s.Batch = nil
}
