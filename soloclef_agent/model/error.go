package model

import (
	"fmt"
	"time"
)

// Error is a model call the endpoint refused or failed. Adapters return it
// for every HTTP-level failure so that callers decide what to do from fields
// and never from matching on message text.
type Error struct {
	// Status is the HTTP status, or 0 when no response arrived.
	Status int

	// Retryable is the adapter's judgement that the same request could
	// succeed later: overload, rate limit, a gateway timeout. A refusal of
	// the request itself (bad request, authentication, context too long) is
	// never retryable, because sending it again can only fail again.
	Retryable bool

	// RetryAfter is how long the endpoint asked the caller to wait, or 0 when
	// it did not say.
	RetryAfter time.Duration

	// Message is the endpoint's own explanation, for logs. It is not written
	// for a reader of the app and must not be shown to one.
	Message string

	// Cause is the failure behind a call that got no response (a refused or
	// dropped connection), or nil. It is kept so that a caller can tell its
	// own dialer's refusal from a network that failed (errors.As through
	// Unwrap), which the message text must not be matched for.
	Cause error
}

// Unwrap is Cause.
func (e *Error) Unwrap() error { return e.Cause }

func (e *Error) Error() string {
	if e.Status == 0 {
		return "model call failed: " + e.Message
	}
	return fmt.Sprintf("model call failed with status %d: %s", e.Status, e.Message)
}
