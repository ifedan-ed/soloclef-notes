package tool

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Question is the error a tool returns to ask a person something from the
// middle of its own work: "which of these three artifacts did you mean?",
// "move this file to the bin?". It is for questions only the tool can phrase.
// A tool that simply must not run unapproved sets Approval instead and never
// sees any of this.
type Question struct {
	// Payload is the question as the client will render it. The loop passes
	// it through untouched; its shape is between the tool and the client.
	Payload json.RawMessage
}

func (q *Question) Error() string {
	return "tool is waiting for an answer"
}

// AskUser builds the error that pauses the run with a question. The run saves its
// state and ends; when it is resumed with an answer, the tool is called again
// with the same arguments and Call.Answer set.
//
// The tool therefore runs twice, and everything it did before returning it
// happens twice. That is the tool's own choice and is visible in its body,
// which is the difference from a framework that reruns a whole step, with
// every other tool in it, to deliver one answer.
func AskUser(payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("tool question cannot be encoded: %w", err)
	}
	return &Question{Payload: raw}
}

// AsQuestion reports whether err is a question from AskUser, and returns it.
func AsQuestion(err error) (*Question, bool) {
	var q *Question
	if errors.As(err, &q) {
		return q, true
	}
	return nil, false
}
