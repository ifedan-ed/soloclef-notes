package openaicompat

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// maxErrorBody is how much of a failed response is read for its message. An
// error page can be any size; the first few kilobytes say what went wrong.
const maxErrorBody = 8 << 10

// failure turns a non-200 response into a model.Error.
func failure(resp *http.Response) *model.Error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	message := strings.TrimSpace(string(raw))
	// The usual body is {"error": {"message": "..."}}; when it is, the
	// message alone reads better in a log than the whole envelope. When it
	// is anything else, the body is the message.
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil && envelope.Error.Message != "" {
		message = envelope.Error.Message
	}

	return &model.Error{
		Status:     resp.StatusCode,
		Retryable:  retryable(resp.StatusCode),
		RetryAfter: retryAfter(resp.Header.Get("Retry-After")),
		Message:    message,
	}
}

// retryable says whether a status means "not now" rather than "not this".
// The 4xx family is the request being refused for what it is, and sending it
// again changes nothing, with two exceptions that are about timing: 408 and
// 429. Everything from 500 up is the far side failing.
func retryable(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

// retryAfter reads a Retry-After header, which is either a number of seconds
// or an HTTP date. Anything else is treated as no header at all.
func retryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if wait := time.Until(at); wait > 0 {
			return wait
		}
	}
	return 0
}
