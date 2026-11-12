package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Verdict is a person's answer to one waiting call.
type Verdict string

const (
	// Approve runs the call. With Decision.Args set, it runs with those
	// arguments instead: that is "edit".
	Approve Verdict = "approve"

	// ApproveAlways runs the call and stops asking about that tool for the
	// rest of the turn. It is refused for a tool.AlwaysAsk tool. Remembering
	// the choice beyond the turn is the caller's job, done before it calls
	// Resume; the loop then learns of it through Config.Trusted.
	ApproveAlways Verdict = "approve_always"

	// Reject does not run the call. The model is told, with Decision.Reason
	// if the person gave one.
	Reject Verdict = "reject"

	// Answer replies to a question a tool asked with tool.AskUser. The tool
	// runs again with Decision.Answer.
	Answer Verdict = "answer"
)

// Decision answers one waiting call.
type Decision struct {
	// CallID is the id from the ApprovalNeeded event, in full.
	CallID string `json:"call_id"`

	Verdict Verdict `json:"verdict"`

	// Args replaces the call's arguments when Verdict is Approve or
	// ApproveAlways. Optional. It must be a JSON object.
	Args json.RawMessage `json:"args,omitempty"`

	// Reason is the person's own words when Verdict is Reject. Optional.
	Reason string `json:"reason,omitempty"`

	// Answer is the reply when Verdict is Answer.
	Answer json.RawMessage `json:"answer,omitempty"`
}

// Approval is one call waiting for a person, as Pending reports it and as an
// ApprovalNeeded event announced it.
type Approval struct {
	// CallID is what a Decision must carry to answer this.
	CallID string
	Tool   string
	Args   string

	// AllowAlways says an ApproveAlways answer would be accepted. A client
	// should offer "always allow" only when this is true, so that the button
	// is never a promise the loop will refuse.
	AllowAlways bool

	// Question is set when the call is a tool's own question rather than a
	// gate, and is the payload the tool passed to tool.AskUser. Such a call
	// is answered with the Answer verdict and no other.
	Question json.RawMessage
}

// checkDecisions verifies that decisions answer exactly the waiting calls,
// each with a verdict that call can take. It runs before anything is changed,
// over the whole tree of nested agents, so a wrong resume is refused whole:
// no call runs on half a set of answers, and nothing is matched by position
// or by guessing.
func checkDecisions(waiting []Approval, decisions []Decision) error {
	byID := make(map[string]*Approval, len(waiting))
	for i := range waiting {
		byID[waiting[i].CallID] = &waiting[i]
	}
	seen := make(map[string]bool, len(decisions))
	for _, d := range decisions {
		w := byID[d.CallID]
		if w == nil {
			return fmt.Errorf("agent: a decision names call %q, which is not waiting", d.CallID)
		}
		if seen[d.CallID] {
			return fmt.Errorf("agent: call %q was given two decisions", d.CallID)
		}
		seen[d.CallID] = true

		if w.Question != nil {
			if d.Verdict != Answer {
				return fmt.Errorf("agent: call %q asked a question and takes the %q verdict, not %q", d.CallID, Answer, d.Verdict)
			}
			if !json.Valid(d.Answer) {
				return fmt.Errorf("agent: the answer to call %q is not valid JSON", d.CallID)
			}
			continue
		}
		switch d.Verdict {
		case Approve, Reject:
		case ApproveAlways:
			if !w.AllowAlways {
				return fmt.Errorf("agent: tool %s asks every time and cannot be always allowed (call %q)", w.Tool, d.CallID)
			}
		default:
			return fmt.Errorf("agent: call %q is waiting for approval and cannot take the verdict %q", d.CallID, d.Verdict)
		}
		if d.Args != nil && !isJSONObject(d.Args) {
			return fmt.Errorf("agent: the edited arguments for call %q are not a JSON object", d.CallID)
		}
	}
	for _, w := range waiting {
		if !seen[w.CallID] {
			return fmt.Errorf("agent: call %q (%s) is waiting and was given no decision", w.CallID, w.Tool)
		}
	}
	return nil
}

// applyDecisions writes checked decisions into the batch. A decision for a
// call of this agent changes that call. A decision for a call inside a
// delegate is recognised by its id starting with the delegate call's id and a
// slash, and is parked on the delegate call with that prefix removed, which
// is the id the delegate's own agent knows the call by.
func (s *State) applyDecisions(decisions []Decision) {
	for i := range s.Batch {
		c := &s.Batch[i]
		switch c.Status {
		case CallAwaitingApproval, CallAwaitingAnswer:
			for _, d := range decisions {
				if d.CallID == c.ID {
					s.applyOne(c, d)
					break
				}
			}
		case CallDelegated:
			prefix := c.ID + "/"
			c.Forward = nil
			for _, d := range decisions {
				if rest, ok := strings.CutPrefix(d.CallID, prefix); ok {
					d.CallID = rest
					c.Forward = append(c.Forward, d)
				}
			}
		}
	}
}

func (s *State) applyOne(c *Call, d Decision) {
	switch d.Verdict {
	case Answer:
		c.Status, c.Answer, c.Question = CallAnswered, d.Answer, nil
	case Reject:
		c.Status, c.Result = CallRejected, rejectionText(d.Reason)
	case Approve, ApproveAlways:
		c.Status = CallApproved
		if d.Args != nil {
			c.Args = string(d.Args)
		}
		if d.Verdict == ApproveAlways && !slices.Contains(s.TurnTrust, c.Name) {
			s.TurnTrust = append(s.TurnTrust, c.Name)
		}
	}
}

// isJSONObject reports whether raw is valid JSON whose value is an object.
// Tool arguments must be an object: that is what a tool's schema describes,
// and a bare string or number is a model that has lost the thread.
func isJSONObject(raw []byte) bool {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(raw)
}
