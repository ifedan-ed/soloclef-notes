// Package toolerror carries the one failure type tools raise to speak plainly
// to the model.
//
// A failed tool call should tell the model what to do next, not leak a stack
// trace. Anything the caller could act on — an unknown id, a rejected
// request — becomes an Error whose message is safe to surface.
package toolerror

// Error is a user-actionable failure whose message is meant for the model to read.
type Error struct {
	Message string
}

func (e *Error) Error() string { return e.Message }

// New builds an Error from an already-composed sentence.
func New(message string) *Error { return &Error{Message: message} }
