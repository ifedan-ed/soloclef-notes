package chat

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"
)

// This file is the web app's reader, in Go, for the tests to read a stream
// with. It is a port of two files of soloclef_web/lib/chat:
//
//   - streaming-state.ts: the SSEEvent union (which fields each frame must
//     carry, and their types) and the content-parts helpers appendText,
//     appendReasoning, startReasoning, endReasoning, addToolCall,
//     updateToolCall, appendToolInputDelta, upsertActivity and
//     upsertActivityTiming;
//   - activity-journal.ts: parseActivityData, mergeActivity and
//     mergeActivityTiming, the rules a step-display snapshot must pass;
//   - stream-pipeline.ts: processSharedStreamEvent, which says which helper
//     each frame reaches.
//
// A stream that this reader accepts and builds the right parts from is a
// stream the page accepts and renders. If the web reader changes, this port
// changes with it; that is the point of having it written out.

// required lists, per frame type, the fields the SSEEvent union declares
// without "?", with the JSON type each must have.
var required = map[string]map[string]string{
	"start":                     {},
	"finish":                    {},
	"start-step":                {},
	"finish-step":               {},
	"text-start":                {"id": "string"},
	"text-delta":                {"delta": "string"},
	"text-end":                  {"id": "string"},
	"reasoning-start":           {"id": "string"},
	"reasoning-delta":           {"delta": "string"},
	"reasoning-end":             {"id": "string"},
	"tool-input-start":          {"toolCallId": "string", "toolName": "string"},
	"tool-input-delta":          {"toolCallId": "string", "inputTextDelta": "string"},
	"tool-input-available":      {"toolCallId": "string", "toolName": "string", "input": "object"},
	"tool-output-available":     {"toolCallId": "string", "output": "object"},
	"data-thread-title-update":  {"data": "object"},
	"data-interrupt-request":    {"data": "object"},
	"data-turn-info":            {"data": "object"},
	"data-user-message-id":      {"data": "object"},
	"data-assistant-message-id": {"data": "object"},
	"data-turn-status":          {"data": "object"},
	"data-token-usage":          {"data": "object"},
	"data-activity":             {"data": "object"},
	"data-activity-timing":      {"data": "object"},
	"error":                     {"message": "string"},
}

// requiredData is the same for the "data" object of the chat layer's own
// frames, as the union and the engine's readers (readStreamedMessageId,
// readStreamedChatTurnId) require them.
var requiredData = map[string]map[string]string{
	"data-thread-title-update":  {"threadId": "number", "title": "string"},
	"data-turn-info":            {"chat_turn_id": "string"},
	"data-user-message-id":      {"message_id": "number", "turn_id": "string"},
	"data-assistant-message-id": {"message_id": "number", "turn_id": "string"},
	"data-turn-status":          {"status": "string"},
	"data-activity":             {"id": "string", "sequence": "number", "kind": "string", "status": "string", "title": "string", "category": "string", "iconKey": "string", "startedAt": "string"},
	"data-activity-timing":      {"status": "string", "activeDurationMs": "number"},
	"data-token-usage":          {"usage": "object", "prompt_tokens": "number", "completion_tokens": "number", "total_tokens": "number", "call_details": "array"},
}

func jsonType(value any) string {
	switch value.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case nil:
		return "null"
	}
	return "unknown"
}

func checkFields(where string, object map[string]any, fields map[string]string) error {
	for name, want := range fields {
		if got := jsonType(object[name]); got != want {
			return fmt.Errorf("%s: field %q is %s, the reader needs %s", where, name, got, want)
		}
	}
	return nil
}

// webReader is the page's ContentPartsState and what the engine keeps
// beside it.
type webReader struct {
	parts            []map[string]any
	currentText      int
	currentReasoning int
	reasoningID      string
	reasoningStarted string
	toolCalls        map[string]int
	// activities and timing are the step display: ContentPartsState's
	// activities map and activityTiming.
	activities map[string]map[string]any
	timing     map[string]any
	display    map[string]any

	turnID             string
	userMessageID      float64
	assistantMessageID float64
	title              string
	statuses           []string
	interrupts         []map[string]any
	tokenUsage         map[string]any
	failure            map[string]any
}

func newWebReader() *webReader {
	return &webReader{currentText: -1, currentReasoning: -1, toolCalls: map[string]int{}, activities: map[string]map[string]any{}}
}

// read applies one frame, as processSharedStreamEvent and the engine's own
// switch do. A frame type the union does not have, or one missing a field
// the union requires, is an error: the page would ignore or mis-render it.
func (r *webReader) read(f frame) error {
	kind := f.kind()
	fields, known := required[kind]
	if !known {
		return fmt.Errorf("frame type %q is not in the reader's union", kind)
	}
	if err := checkFields(kind, f, fields); err != nil {
		return err
	}
	if dataFields, has := requiredData[kind]; has {
		if err := checkFields(kind+".data", f.data(), dataFields); err != nil {
			return err
		}
	}
	text := func(name string) string { s, _ := f[name].(string); return s }
	switch kind {
	case "reasoning-start": // startReasoning
		r.currentText, r.currentReasoning = -1, -1
		r.reasoningID, r.reasoningStarted = text("id"), text("startedAt")
	case "text-delta": // appendText
		r.currentReasoning = -1
		if r.currentText >= 0 && r.parts[r.currentText]["type"] == "text" {
			r.parts[r.currentText]["text"] = r.parts[r.currentText]["text"].(string) + text("delta")
		} else {
			r.parts = append(r.parts, map[string]any{"type": "text", "text": text("delta")})
			r.currentText = len(r.parts) - 1
		}
	case "reasoning-delta": // appendReasoning
		r.currentText = -1
		if r.currentReasoning >= 0 && r.parts[r.currentReasoning]["type"] == "reasoning" {
			r.parts[r.currentReasoning]["text"] = r.parts[r.currentReasoning]["text"].(string) + text("delta")
		} else {
			r.parts = append(r.parts, map[string]any{"type": "reasoning", "text": text("delta"), "id": r.reasoningID, "status": "running", "startedAt": r.reasoningStarted})
			r.currentReasoning = len(r.parts) - 1
		}
	case "reasoning-end": // endReasoning
		if r.currentReasoning >= 0 {
			current := r.parts[r.currentReasoning]
			if id := text("id"); id == "" || current["id"] == "" || current["id"] == id {
				current["status"] = "completed"
				current["completedAt"] = text("completedAt")
			}
		}
		r.currentReasoning, r.reasoningID, r.reasoningStarted = -1, "", ""
	case "tool-input-start": // addToolCall
		r.addToolCall(text("toolCallId"), text("toolName"), map[string]any{}, text("langchainToolCallId"))
		r.mergeMetadata(text("toolCallId"), f["metadata"])
	case "tool-input-delta": // appendToolInputDelta
		if i, known := r.toolCalls[text("toolCallId")]; known {
			previous, _ := r.parts[i]["argsText"].(string)
			r.parts[i]["argsText"] = previous + text("inputTextDelta")
		}
	case "tool-input-available":
		input, _ := f["input"].(map[string]any)
		pretty, _ := json.MarshalIndent(input, "", "  ")
		if _, known := r.toolCalls[text("toolCallId")]; !known {
			r.addToolCall(text("toolCallId"), text("toolName"), input, text("langchainToolCallId"))
		}
		part := r.parts[r.toolCalls[text("toolCallId")]]
		part["args"], part["argsText"] = input, string(pretty)
		r.mergeMetadata(text("toolCallId"), f["metadata"])
	case "tool-output-available": // updateToolCall
		i, known := r.toolCalls[text("toolCallId")]
		if !known {
			return fmt.Errorf("a result arrived for tool call %q, which the reader has no card for", text("toolCallId"))
		}
		r.parts[i]["result"] = f["output"]
	case "data-activity": // upsertActivity
		return r.upsertActivity(f.data())
	case "data-activity-timing": // upsertActivityTiming
		data := f.data()
		status, _ := data["status"].(string)
		if status != "running" && status != "paused" && status != "completed" {
			return fmt.Errorf("activity timing status %q is not one the reader knows", status)
		}
		duration, _ := data["activeDurationMs"].(float64)
		if duration < 0 || duration != float64(int64(duration)) {
			return fmt.Errorf("activity timing %v is not a whole number of milliseconds", data["activeDurationMs"])
		}
		// mergeActivityTiming: a finished clock stays finished and a clock
		// never runs backwards. The page drops such a frame; a server that
		// sends one has a bug, so here it is an error.
		if r.timing != nil && (r.timing["status"] == "completed" || duration < r.timing["activeDurationMs"].(float64)) {
			return fmt.Errorf("activity timing %v after %v: the reader would drop it", data, r.timing)
		}
		r.timing = data
		r.journalPart()
	case "data-interrupt-request":
		// The engine pairs each action with a tool card and files the
		// request under its tool_call_id; one with neither id is dropped.
		data := f.data()
		if id, _ := data["tool_call_id"].(string); id == "" {
			return fmt.Errorf("an interrupt request carries no tool_call_id")
		}
		r.interrupts = append(r.interrupts, data)
	case "data-turn-info":
		r.turnID, _ = f.data()["chat_turn_id"].(string)
	case "data-user-message-id":
		r.userMessageID, _ = f.data()["message_id"].(float64)
	case "data-assistant-message-id":
		r.assistantMessageID, _ = f.data()["message_id"].(float64)
	case "data-thread-title-update":
		r.title, _ = f.data()["title"].(string)
	case "data-turn-status":
		status, _ := f.data()["status"].(string)
		if status != "idle" && status != "busy" && status != "cancelling" {
			return fmt.Errorf("turn status %q is not one the reader knows", status)
		}
		r.statuses = append(r.statuses, status)
	case "data-token-usage":
		r.tokenUsage = f.data()
	case "error": // the pipeline throws; the engine shows the message
		r.failure = f
	}
	return nil
}

// mergeMetadata is mergeToolPartMetadata: keys already on the card stay.
func (r *webReader) mergeMetadata(id string, incoming any) {
	metadata, _ := incoming.(map[string]any)
	i, known := r.toolCalls[id]
	if !known || len(metadata) == 0 {
		return
	}
	into, _ := r.parts[i]["metadata"].(map[string]any)
	if into == nil {
		into = map[string]any{}
		r.parts[i]["metadata"] = into
	}
	for key, value := range metadata {
		if _, has := into[key]; !has {
			into[key] = value
		}
	}
}

var (
	activityStatuses   = map[string]bool{"running": true, "awaiting_approval": true, "completed": true, "error": true, "cancelled": true, "interrupted": true}
	activityCategories = map[string]bool{"file": true, "research": true, "artifact": true, "connector": true, "action": true}
)

func terminalActivity(status any) bool {
	return status != "running" && status != "awaiting_approval"
}

// upsertActivity is parseActivityData, mergeActivity and upsertActivity. A
// snapshot the page would drop (it fails the parse, changes an activity's
// identity, or reopens a finished one) is an error here: the server must
// not send what the page throws away.
func (r *webReader) upsertActivity(next map[string]any) error {
	id, _ := next["id"].(string)
	status, _ := next["status"].(string)
	category, _ := next["category"].(string)
	sequence, _ := next["sequence"].(float64)
	switch {
	case id == "" || next["kind"] == "" || next["title"] == "" || next["iconKey"] == "" || next["startedAt"] == "":
		return fmt.Errorf("activity %v has an empty required field", next)
	case sequence < 0 || sequence != float64(int64(sequence)):
		return fmt.Errorf("activity %q sequence %v is not a whole number", id, next["sequence"])
	case !activityStatuses[status] || !activityCategories[category]:
		return fmt.Errorf("activity %q has status %q and category %q", id, status, category)
	}
	completedAt, _ := next["completedAt"].(string)
	if terminalActivity(status) != (completedAt != "") {
		return fmt.Errorf("activity %q is %s with completedAt %q: finished and only finished activities carry one", id, status, completedAt)
	}
	if source, has := next["integration"].(map[string]any); has {
		if s := source["source"]; s != "native" && s != "connector" && s != "mcp" {
			return fmt.Errorf("activity %q integration source %v", id, s)
		}
	}
	if current, known := r.activities[id]; known {
		for _, field := range []string{"sequence", "kind", "category", "iconKey", "startedAt"} {
			if current[field] != next[field] {
				return fmt.Errorf("activity %q changed its %s from %v to %v: the reader would keep the first", id, field, current[field], next[field])
			}
		}
		if terminalActivity(current["status"]) {
			return fmt.Errorf("activity %q is %v and was sent again as %s", id, current["status"], status)
		}
	}
	r.activities[id] = next
	r.journalPart()
	return nil
}

// journalPart rebuilds the step display's part. The page keeps it at the
// head of its parts and moves every index it holds by one when it first
// puts it there; this reader keeps it beside the parts instead, in display,
// so that a test's "the second part" is the second part of the answer
// whether or not a step display precedes it.
func (r *webReader) journalPart() {
	list := make([]any, 0, len(r.activities))
	for _, activity := range r.activities {
		list = append(list, activity)
	}
	sort.Slice(list, func(a, b int) bool {
		return list[a].(map[string]any)["sequence"].(float64) < list[b].(map[string]any)["sequence"].(float64)
	})
	data := map[string]any{"activities": list}
	if r.timing != nil {
		data["timing"] = r.timing
	}
	r.display = map[string]any{"type": "data-activities", "data": data}
}

// steps is the step display the reader built: its activities in order and
// its clock.
func (r *webReader) steps() ([]map[string]any, map[string]any) {
	return stepsIn(r.display)
}

func stepsIn(display map[string]any) ([]map[string]any, map[string]any) {
	if display == nil {
		return nil, nil
	}
	data := display["data"].(map[string]any)
	var out []map[string]any
	list, _ := data["activities"].([]any)
	for _, activity := range list {
		out = append(out, activity.(map[string]any))
	}
	timing, _ := data["timing"].(map[string]any)
	return out, timing
}

func (r *webReader) addToolCall(id, name string, args map[string]any, modelID string) {
	part := map[string]any{"type": "tool-call", "toolCallId": id, "toolName": name, "args": args}
	if modelID != "" {
		part["langchainToolCallId"] = modelID
	}
	r.parts = append(r.parts, part)
	r.toolCalls[id] = len(r.parts) - 1
	r.currentText, r.currentReasoning = -1, -1
}

// seed is what the engine's resume flow does before it reads the stream: it
// starts from the parts of the message that paused, so that a result for a
// call made before the pause lands on the card already on the page.
func (r *webReader) seed(parts []map[string]any) {
	for _, part := range parts {
		copied := map[string]any{}
		for key, value := range part {
			copied[key] = value
		}
		switch part["type"] {
		case "data-activities":
			// The paused message's step display: its activities are known
			// to the reader, and its clock is where the resumed one starts.
			data, _ := part["data"].(map[string]any)
			list, _ := data["activities"].([]any)
			for _, item := range list {
				if activity, is := item.(map[string]any); is {
					id, _ := activity["id"].(string)
					r.activities[id] = activity
				}
			}
			r.timing, _ = data["timing"].(map[string]any)
			r.journalPart()
		case "text":
			r.parts = append(r.parts, copied)
			r.currentText = len(r.parts) - 1
		case "reasoning":
			r.parts = append(r.parts, copied)
			r.currentText = -1
		case "tool-call":
			id, _ := part["toolCallId"].(string)
			r.toolCalls[id] = len(r.parts)
			r.parts = append(r.parts, copied)
			r.currentText = -1
		}
	}
}

// readResumed reads a resumed turn's stream as the page does: on top of the
// paused message.
func readResumed(t *testing.T, paused []map[string]any, frames []frame) *webReader {
	t.Helper()
	reader := newWebReader()
	reader.seed(paused)
	for i, f := range frames {
		if err := reader.read(f); err != nil {
			t.Fatalf("frame %d: %v\n%v", i, err, f)
		}
	}
	return reader
}

// readAll reads a whole stream, failing the test at the first frame the
// page would not accept.
func readAll(t *testing.T, frames []frame) *webReader {
	t.Helper()
	reader := newWebReader()
	for i, f := range frames {
		if err := reader.read(f); err != nil {
			t.Fatalf("frame %d: %v\n%v", i, err, f)
		}
	}
	return reader
}

// sameParts compares what the reader built from the stream with what was
// saved, part by part, on the fields a part is rendered from. Three
// differences are expected and allowed for: saved text has its citation
// labels rewritten (the caller passes the text it expects), and a reasoning
// part's id and times are each side's own.
func sameParts(t *testing.T, live, saved []map[string]any, savedText map[int]string) {
	t.Helper()
	if len(live) != len(saved) {
		t.Fatalf("the page built %d parts and %d were saved:\n live %v\nsaved %v", len(live), len(saved), live, saved)
	}
	for i := range live {
		l, s := live[i], saved[i]
		if l["type"] != s["type"] {
			t.Fatalf("part %d is %v live and %v saved", i, l["type"], s["type"])
		}
		switch l["type"] {
		case "text":
			want := l["text"]
			if replaced, has := savedText[i]; has {
				want = replaced
			}
			if s["text"] != want {
				t.Errorf("part %d text: saved %q, want %q", i, s["text"], want)
			}
		case "reasoning":
			if l["text"] != s["text"] || l["status"] != s["status"] {
				t.Errorf("part %d reasoning: live %v, saved %v", i, l, s)
			}
		case "tool-call":
			for _, field := range []string{"toolCallId", "toolName", "argsText", "langchainToolCallId"} {
				if l[field] != s[field] {
					t.Errorf("part %d %s: live %v, saved %v", i, field, l[field], s[field])
				}
			}
			for _, field := range []string{"args", "result", "metadata"} {
				lj, _ := json.Marshal(l[field])
				sj, _ := json.Marshal(s[field])
				if string(lj) != string(sj) {
					t.Errorf("part %d %s: live %s, saved %s", i, field, lj, sj)
				}
			}
		}
	}
}
