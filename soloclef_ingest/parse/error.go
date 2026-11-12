package parse

import (
	"errors"
	"fmt"
)

// Kind says why a parse did not produce a document, in terms the caller acts
// on: tell the person, fix the deployment, or try again later.
type Kind string

const (
	// About the file. Sending the same bytes again ends the same way.

	// NoText: the file parsed and held no text. From the local parser this is
	// a scan with no text layer; OCR is not run there.
	NoText Kind = "no_text"
	// UnsupportedFormat: this parser does not read this kind of file.
	UnsupportedFormat Kind = "unsupported_format"
	// ParseFailed: the parser tried and failed on this file.
	ParseFailed Kind = "parse_failed"
	// TooLarge: the file is over the parser's size limit.
	TooLarge Kind = "too_large"

	// About the deployment. A person has to change something.

	// NotConfigured: the chosen parser has no key or address here. It is
	// reported; no other parser is used in its place.
	NotConfigured Kind = "not_configured"
	// Unauthorized: the service refused the key, or the account cannot pay.
	Unauthorized Kind = "unauthorized"
	// Rejected: the service refused the request itself (a 4xx that is not
	// about the key, the size or the rate). A bug here or a changed API.
	Rejected Kind = "rejected"
	// BadResponse: the service answered something this client cannot read.
	BadResponse Kind = "bad_response"

	// About the moment. The same job may succeed later.

	// RateLimited: the service is throttling this account.
	RateLimited Kind = "rate_limited"
	// Unavailable: the service could not be reached or answered 5xx.
	Unavailable Kind = "unavailable"
	// TimedOut: the parse outlived the time this client allows it. Not
	// retried automatically: a hosted job sent again is billed again.
	TimedOut Kind = "timed_out"
)

// Error is the one error type parsers return.
type Error struct {
	Kind   Kind
	Parser ID
	// Message is for a person: a log line or a document's failure reason.
	Message string
	// Status is the HTTP status behind the error, 0 when there was none.
	Status int
	// Err is the underlying error, when there is one.
	Err error
}

func (e *Error) Error() string {
	text := fmt.Sprintf("%s parser: %s: %s", e.Parser, e.Kind, e.Message)
	if e.Err != nil {
		text += ": " + e.Err.Error()
	}
	return text
}

func (e *Error) Unwrap() error { return e.Err }

// Retryable reports whether running the same job again later may succeed
// without anyone changing anything. It is advice to the job queue; no parser
// loops on it.
func (e *Error) Retryable() bool {
	return e.Kind == RateLimited || e.Kind == Unavailable
}

// KindOf is err's Kind, or "" when err is not a parse error.
func KindOf(err error) Kind {
	var parseError *Error
	if errors.As(err, &parseError) {
		return parseError.Kind
	}
	return ""
}
