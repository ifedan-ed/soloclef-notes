package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// Rejection is a reply that was read and is not acceptable: what was wrong
// with it, written for the model, which is shown it once and asked again.
type Rejection struct{ Problems string }

func (r *Rejection) Error() string { return r.Problems }

// Reject says what is wrong with a reply.
func Reject(format string, arguments ...any) *Rejection {
	return &Rejection{Problems: fmt.Sprintf(format, arguments...)}
}

// ErrUnusable is a reply that was still not acceptable after it had been
// asked for again.
var ErrUnusable = errors.New("llm: the model's reply could not be used")

// correction is what the model is told after a reply was refused. The
// problems are precise instructions already; a model shown them usually
// fixes the reply at once.
const correction = `Your previous reply could not be accepted:
%s
Reply again with the corrected JSON object only: no prose, no code fence, no commentary. Keep every field that was already valid.`

// AskJSON asks for a JSON object and hands it to read, which decodes and
// checks it. A reply read refuses (with a *Rejection, or any error from
// decoding) is asked for once more, with the model's own reply and the
// problems shown to it. One retry and not a loop: a model that cannot
// satisfy the shape when shown exactly what it broke is not helped by a
// third ask.
//
// A provider failure or a timeout is returned as it is: those are the
// platform failing, not the model answering badly, and asking again with a
// correction would only spend more.
func AskJSON[T any](ctx context.Context, m model.Model, timeout time.Duration, messages []model.Message, read func(raw []byte) (T, error)) (T, error) {
	var zero T
	reply, err := ask(ctx, m, timeout, messages)
	if err != nil {
		return zero, err
	}
	value, problem := parse(reply, read)
	if problem == nil {
		return value, nil
	}
	retry := append(append([]model.Message{}, messages...),
		model.Message{Role: model.RoleAssistant, Content: reply},
		model.Message{Role: model.RoleUser, Content: fmt.Sprintf(correction, problem.Error())})
	reply, err = ask(ctx, m, timeout, retry)
	if err != nil {
		return zero, err
	}
	value, problem = parse(reply, read)
	if problem != nil {
		return zero, fmt.Errorf("%w after one retry: %v (the reply began %q)", ErrUnusable, problem, clip(reply, 300))
	}
	return value, nil
}

// Ask is one call for plain text.
func Ask(ctx context.Context, m model.Model, timeout time.Duration, messages []model.Message) (string, error) {
	return ask(ctx, m, timeout, messages)
}

func ask(ctx context.Context, m model.Model, timeout time.Duration, messages []model.Message) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := m.Generate(ctx, model.Request{Messages: messages}, func(model.Chunk) {})
	if err != nil {
		return "", err
	}
	return response.Message.Content, nil
}

// parse finds the JSON object in a reply and reads it. Models wrap JSON in
// prose and code fences, and put raw newlines inside long strings; each of
// those is a reply that is right in substance, so each is accepted rather
// than spent a retry on.
func parse[T any](reply string, read func(raw []byte) (T, error)) (T, error) {
	var zero T
	content := stripFence(reply)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return zero, Reject("no JSON object was found in the reply")
	}
	candidate := []byte(content[start : end+1])
	if !json.Valid(candidate) {
		candidate = escapeControlsInStrings(candidate)
		if !json.Valid(candidate) {
			return zero, Reject("the reply is not valid JSON")
		}
	}
	return read(candidate)
}

// stripFence removes a Markdown code fence around the whole reply.
func stripFence(reply string) string {
	trimmed := strings.TrimSpace(reply)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	trimmed = strings.TrimPrefix(trimmed, "```")
	if newline := strings.IndexByte(trimmed, '\n'); newline >= 0 {
		trimmed = trimmed[newline+1:] // the language tag, if any
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(trimmed), "```"))
}

// escapeControlsInStrings escapes the raw control characters a model leaves
// inside a string (a newline in a long line of dialogue). They are valid
// content, which strict JSON wants written as escapes; outside strings they
// are whitespace and are left alone.
func escapeControlsInStrings(raw []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(raw) + 16)
	inString, escaped := false, false
	for _, b := range raw {
		switch {
		case escaped:
			escaped = false
		case inString && b == '\\':
			escaped = true
		case b == '"':
			inString = !inString
		case inString && b < 0x20:
			fmt.Fprintf(&out, `\u%04x`, b)
			continue
		}
		out.WriteByte(b)
	}
	return out.Bytes()
}

func clip(text string, length int) string {
	if len(text) > length {
		return text[:length]
	}
	return text
}
