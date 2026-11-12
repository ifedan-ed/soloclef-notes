package threads

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

func failure(field, message string) errorenvelope.Failure {
	return errorenvelope.Failure{Location: []string{"body", field}, Message: message}
}

// decode reads a JSON object body into target. Unknown fields are allowed:
// a newer app must not be refused by an older server for one more field.
func decode(raw []byte, target any) []errorenvelope.Failure {
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(target); err != nil {
		return []errorenvelope.Failure{{Location: []string{"body"}, Message: "The body is not the JSON object this route reads: " + err.Error()}}
	}
	return nil
}

// cleanTitle is a title as it is stored: on one line, trimmed, and within
// the column.
func cleanTitle(title string) (string, *errorenvelope.Failure) {
	title = strings.Join(strings.Fields(title), " ")
	if utf8.RuneCountInString(title) > maxTitleChars {
		f := failure("title", fmt.Sprintf("The title is longer than %d characters", maxTitleChars))
		return "", &f
	}
	return title, nil
}

// createRequest is the body of POST /threads. There is nothing in it about
// whose the thread is: it is the caller's.
type createRequest struct {
	Title    string
	Archived bool
}

func readCreate(raw []byte) (createRequest, []errorenvelope.Failure) {
	var body struct {
		Title    *string `json:"title"`
		Archived bool    `json:"archived"`
	}
	if failures := decode(raw, &body); failures != nil {
		return createRequest{}, failures
	}
	request := createRequest{Title: defaultThreadTitle, Archived: body.Archived}
	var failures []errorenvelope.Failure
	if body.Title != nil {
		title, bad := cleanTitle(*body.Title)
		switch {
		case bad != nil:
			failures = append(failures, *bad)
		case title != "":
			// A blank title is no title: the thread gets the default one,
			// which the first answer replaces with a real one.
			request.Title = title
		}
	}
	return request, failures
}

// updateRequest is the body of PUT /threads/{id}. A nil field was left out
// (or sent as null) and keeps its value.
type updateRequest struct {
	Title    *string
	Archived *bool
}

func readUpdate(raw []byte) (updateRequest, []errorenvelope.Failure) {
	var body struct {
		Title    *string `json:"title"`
		Archived *bool   `json:"archived"`
	}
	if failures := decode(raw, &body); failures != nil {
		return updateRequest{}, failures
	}
	request := updateRequest{Archived: body.Archived}
	if body.Title != nil {
		title, bad := cleanTitle(*body.Title)
		if bad != nil {
			return request, []errorenvelope.Failure{*bad}
		}
		if title == "" {
			// Renaming a thread to nothing leaves a row nobody can find in
			// the list.
			return request, []errorenvelope.Failure{failure("title", "The title is empty")}
		}
		request.Title = &title
	}
	return request, nil
}

// appendRequest is the body of POST /threads/{id}/messages.
type appendRequest struct {
	// Role is the column's enum label.
	Role    string
	Content []byte
	TurnID  *string
}

// readAppend checks an appended message.
//
// Only "user" and "assistant" are taken. A "system" row is instructions,
// and instructions are not something a client adds to a conversation. A
// token_usage field, which older pages send, is read past: usage is written
// by the turn that spent the tokens.
func readAppend(raw []byte) (appendRequest, []errorenvelope.Failure) {
	var body struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		TurnID  *string         `json:"turn_id"`
	}
	if failures := decode(raw, &body); failures != nil {
		return appendRequest{}, failures
	}
	var request appendRequest
	var failures []errorenvelope.Failure
	switch strings.ToLower(body.Role) {
	case "user":
		request.Role = "USER"
	case "assistant":
		request.Role = "ASSISTANT"
	case "":
		failures = append(failures, failure("role", "role is required"))
	default:
		failures = append(failures, failure("role", "role must be user or assistant"))
	}
	content := bytes.TrimSpace(body.Content)
	switch {
	case len(content) == 0 || bytes.Equal(content, []byte("null")):
		failures = append(failures, failure("content", "content is required"))
	case len(content) > maxAppendedContentBytes:
		failures = append(failures, failure("content", fmt.Sprintf("content is larger than %d bytes", maxAppendedContentBytes)))
	case bytes.Contains(content, []byte(`\u0000`)):
		// The column is jsonb, which cannot hold this character; say so
		// here instead of failing in the database.
		failures = append(failures, failure("content", "content holds a character that cannot be stored (U+0000)"))
	default:
		request.Content = content
	}
	if body.TurnID != nil {
		if turn := strings.TrimSpace(*body.TurnID); turn != "" {
			if len(turn) > maxTurnIDChars {
				failures = append(failures, failure("turn_id", fmt.Sprintf("turn_id is longer than %d characters", maxTurnIDChars)))
			}
			request.TurnID = &turn
		}
	}
	return request, failures
}

// readExpiry reads the body of POST /threads/{id}/snapshots: for how many
// days the link lives. The second result is false for a link that never
// expires.
//
// No body, an empty one, and one without the field all mean seven days; an
// explicit null means never. The two are different requests, so the field
// is read as present-or-not and not as a number that is zero when missing.
func readExpiry(raw []byte) (days int, expires bool, failures []errorenvelope.Failure) {
	const standard = 7
	if len(bytes.TrimSpace(raw)) == 0 {
		return standard, true, nil
	}
	var body map[string]json.RawMessage
	if failures := decode(raw, &body); failures != nil {
		return 0, false, failures
	}
	value, sent := body["expires_in_days"]
	if !sent {
		return standard, true, nil
	}
	switch strings.TrimSpace(string(value)) {
	case "null":
		return 0, false, nil
	case "7":
		return 7, true, nil
	case "30":
		return 30, true, nil
	}
	return 0, false, []errorenvelope.Failure{failure("expires_in_days", "expires_in_days must be 7, 30 or null")}
}

// queryNumber reads a whole number from the query string. absent is true
// when the parameter was not sent.
func queryNumber(query url.Values, name string) (value int64, absent bool, bad *errorenvelope.Failure) {
	text, sent := requestnumber.QueryValue(query, name)
	if !sent {
		return 0, true, nil
	}
	value, bad, rangeErr := requestnumber.Parse(text, "query", name)
	if bad == nil && rangeErr != nil {
		bad = &errorenvelope.Failure{Location: []string{"query", name}, Message: name + " is out of range"}
	}
	return value, false, bad
}
