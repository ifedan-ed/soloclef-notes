// Package hostedapi holds what every parser reached over HTTP has in common:
// turning a transport failure or a status code into the typed parse.Error
// the caller acts on, and reading a reply without trusting its size.
//
// Each client still owns its protocol. This is only the part where three
// APIs would otherwise disagree about what a 429 means to the job queue.
package hostedapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// errorBodyLimit bounds how much of an error reply is read: enough for any
// API's message, never a megabyte of HTML error page in a log line.
const errorBodyLimit = 2048

// TransportError types a failure to get any response at all.
func TransportError(ctx context.Context, parser parse.ID, action string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return &parse.Error{Kind: parse.TimedOut, Parser: parser, Message: action + " ran out of time", Err: err}
		}
		// Cancelled by the caller (shutdown): not the parser's failure, and
		// the caller recognises its own cancellation by errors.Is.
		return ctxErr
	}
	return &parse.Error{Kind: parse.Unavailable, Parser: parser, Message: action + " did not get an answer", Err: err}
}

// StatusError types a response whose status is not a success. It reads the
// start of the body for the message and leaves closing it to the caller.
func StatusError(parser parse.ID, action string, response *http.Response) *parse.Error {
	snippet, _ := io.ReadAll(io.LimitReader(response.Body, errorBodyLimit))
	message := fmt.Sprintf("%s was answered %d", action, response.StatusCode)
	if text := strings.TrimSpace(string(snippet)); text != "" {
		message += ": " + text
	}
	return &parse.Error{Kind: kindOfStatus(response.StatusCode), Parser: parser, Message: message, Status: response.StatusCode}
}

func kindOfStatus(status int) parse.Kind {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden,
		// 402: the key is right and the account is out of credit. The same
		// person fixes both, in the same place.
		status == http.StatusPaymentRequired:
		return parse.Unauthorized
	case status == http.StatusRequestEntityTooLarge:
		return parse.TooLarge
	case status == http.StatusUnsupportedMediaType:
		return parse.UnsupportedFormat
	case status == http.StatusTooManyRequests:
		return parse.RateLimited
	case status >= 500:
		return parse.Unavailable
	case status >= 400:
		return parse.Rejected
	default:
		// A 1xx or 3xx reaching here means the API did something no client
		// here expects.
		return parse.BadResponse
	}
}

// DecodeJSON reads a successful reply into value, from the stream. limit
// bounds the reply: a parser's Markdown can be large, but not unbounded.
func DecodeJSON(parser parse.ID, action string, body io.Reader, limit int64, value any) error {
	limited := &io.LimitedReader{R: body, N: limit + 1}
	if err := json.NewDecoder(limited).Decode(value); err != nil {
		if limited.N <= 0 {
			return &parse.Error{
				Kind: parse.BadResponse, Parser: parser,
				Message: fmt.Sprintf("the reply to %s is larger than %d bytes", action, limit),
			}
		}
		return &parse.Error{Kind: parse.BadResponse, Parser: parser, Message: "the reply to " + action + " is not the JSON expected", Err: err}
	}
	return nil
}
