// Package parts builds an assistant message's saved content: the list of
// parts the web and phone apps render a finished answer from.
//
// The apps build the same list live, from the stream, as an answer arrives
// (soloclef_web/lib/chat/streaming-state.ts). What is saved has to be what
// they built, or an answer changes shape when the page is reloaded. So this
// builder consumes the same events the stream writer does and applies the
// reader's rules: text and reasoning are runs that a tool call ends, a tool
// call is one part that its arguments and its result fill in, and a nested
// agent appears only as the cards of its tool calls.
//
// The parts of a message are a contract with those readers:
//
//	{"type":"text","text":…}
//	{"type":"reasoning","text":…,"id":…,"status":"running|completed|interrupted","startedAt":…,"completedAt":…}
//	{"type":"tool-call","toolCallId":…,"toolName":…,"args":{…},"argsText":…,"result":…,"langchainToolCallId":…,"state":"aborted"}
//	{"type":"status","code":"no_response","text":…}
package parts

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
)

// Part is one part. Which fields mean anything depends on Type, as in the
// reader's own union; MarshalJSON writes only those.
type Part struct {
	Type string

	// Text is the content of a text, reasoning or status part.
	Text string

	// Reasoning.
	ID          string
	Status      string
	StartedAt   string
	CompletedAt string

	// Tool call. Args is always a JSON object. ArgsText is the argument
	// text as shown on the card: the raw fragments while they stream, then
	// the indented final arguments. Result is nil until the call finishes.
	ToolCallID  string
	ToolName    string
	Args        json.RawMessage
	ArgsText    string
	Result      json.RawMessage
	ModelCallID string
	// State is "aborted" for a call the turn ended without finishing.
	State string
	// Metadata is what ties the card to things beside it (its line in the
	// step display); nil when there is nothing to tie.
	Metadata json.RawMessage

	// Code is a status part's code.
	Code string
}

// The part types and the values the readers switch on.
const (
	TypeText      = "text"
	TypeReasoning = "reasoning"
	TypeToolCall  = "tool-call"
	TypeStatus    = "status"

	reasoningRunning     = "running"
	reasoningCompleted   = "completed"
	reasoningInterrupted = "interrupted"
	stateAborted         = "aborted"
)

// MarshalJSON writes the part in the readers' field names. It is written
// out by hand, one type at a time, because the optional fields differ by
// type and "omitempty" cannot say "text is required here, absent there":
// an empty text part must still carry "text".
func (p Part) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	field := func(name string, value any) error {
		encoded, err := marshal(value)
		if err != nil {
			return err
		}
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		b.WriteString(`"` + name + `":`)
		b.Write(encoded)
		return nil
	}
	b.WriteByte('{')
	_ = field("type", p.Type)
	switch p.Type {
	case TypeText:
		_ = field("text", p.Text)
	case TypeReasoning:
		_ = field("text", p.Text)
		_ = field("id", p.ID)
		_ = field("status", p.Status)
		_ = field("startedAt", p.StartedAt)
		if p.CompletedAt != "" {
			_ = field("completedAt", p.CompletedAt)
		}
	case TypeToolCall:
		_ = field("toolCallId", p.ToolCallID)
		_ = field("toolName", p.ToolName)
		args := p.Args
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		if err := field("args", args); err != nil {
			return nil, err
		}
		if p.ArgsText != "" {
			_ = field("argsText", p.ArgsText)
		}
		if p.Result != nil {
			if err := field("result", p.Result); err != nil {
				return nil, err
			}
		}
		if p.ModelCallID != "" {
			_ = field("langchainToolCallId", p.ModelCallID)
		}
		if p.State != "" {
			_ = field("state", p.State)
		}
		if p.Metadata != nil {
			if err := field("metadata", p.Metadata); err != nil {
				return nil, err
			}
		}
	case TypeStatus:
		_ = field("code", p.Code)
		_ = field("text", p.Text)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshal encodes without HTML escaping: the content is stored in a jsonb
// column and read by JSON.parse, and "<" in an answer is "<".
func marshal(value any) ([]byte, error) {
	return Encode(value)
}

// Encode is the encoding of everything saved in a message's content: JSON
// without HTML escaping. It is exported so that a part built beside the
// builder's own (the step display's) is written the same way.
func Encode(value any) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// Builder turns a run's events into parts. Its methods may be called from
// any goroutine: the run's sink feeds it while a timer snapshots it to save
// progress.
type Builder struct {
	// card decides what of a tool's result is saved; it is the function the
	// stream writer was given, so the saved card is the streamed one.
	card func(event.Event) any
	// metadata is asked what a call's card carries beside its result; it is
	// the stream writer's own, for the same reason card is. May be nil.
	metadata func(event.Event) any
	now      func() time.Time
	newID    func(kind string) string

	mu    sync.Mutex
	parts []Part
	// text and reasoning are the index of the open run, or -1. At most one
	// is open: text closes reasoning and reasoning closes text.
	text, reasoning int
	// run is the open run's content. Deltas are appended here and written
	// to the part when the run closes or the parts are read, because
	// appending each delta to the part's string would copy the whole answer
	// once per token (BenchmarkTextDelta: 4000 tokens of a long answer is
	// megabytes of copying against kilobytes).
	run strings.Builder
	// calls maps a call's id to its part.
	calls map[string]int
	// late holds results for calls this builder has no card for: calls made
	// in an earlier stretch of the turn, before a pause. See Late.
	late map[string]json.RawMessage
	// unsaved names the tools whose arguments are not kept; see
	// LeaveOutArguments.
	unsaved map[string]bool
	// version counts changes, so a saver can tell whether there is anything
	// new to write without comparing content.
	version uint64
}

// LeaveOutArguments names tools whose calls are saved with empty arguments.
// A card's arguments are saved in the message like its result, and so are
// synced to every reader of the thread and published with it; a tool whose
// arguments are themselves private content (a whole memory document) is
// named here, and its card is saved as a call with "{}". Call it before the
// first event.
func (b *Builder) LeaveOutArguments(tools ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.unsaved == nil {
		b.unsaved = map[string]bool{}
	}
	for _, name := range tools {
		b.unsaved[name] = true
	}
}

// NewBuilder returns an empty builder. card, now and newID are required and
// are normally the stream writer's own; metadata is the writer's too and,
// as there, optional: nil attaches nothing to any card.
func NewBuilder(card, metadata func(event.Event) any, now func() time.Time, newID func(kind string) string) *Builder {
	return &Builder{card: card, metadata: metadata, now: now, newID: newID, text: -1, reasoning: -1, calls: map[string]int{}}
}

// Event applies one event. It has the signature of an event.Sink.
func (b *Builder) Event(ev event.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ev.Parent != "" {
		// A nested agent's words are its working, not the answer; only its
		// finished tool calls and their results are shown, as cards.
		switch ev.Kind {
		case event.ToolCallReady:
			b.closeRuns()
			b.startCall(ev)
			b.fillCall(ev)
		case event.ToolResult:
			b.result(ev)
		}
		return
	}
	switch ev.Kind {
	case event.TextDelta:
		b.closeReasoning(reasoningCompleted)
		if b.text < 0 {
			b.parts = append(b.parts, Part{Type: TypeText})
			b.text = len(b.parts) - 1
		}
		b.run.WriteString(ev.Text)
		b.version++
	case event.ReasoningDelta:
		b.closeText()
		if b.reasoning < 0 {
			b.parts = append(b.parts, Part{Type: TypeReasoning, ID: b.newID("reasoning"), Status: reasoningRunning, StartedAt: b.timestamp()})
			b.reasoning = len(b.parts) - 1
		}
		b.run.WriteString(ev.Text)
		b.version++
	case event.ToolCallStarted:
		b.closeRuns()
		b.startCall(ev)
	case event.ToolCallDelta:
		if i, known := b.calls[ev.CallID]; known && !b.unsaved[b.parts[i].ToolName] {
			b.parts[i].ArgsText += ev.Text
			b.version++
		}
	case event.ToolCallReady:
		if _, known := b.calls[ev.CallID]; !known {
			// A model that did not stream the call: the card starts here.
			b.closeRuns()
			b.startCall(ev)
		}
		b.fillCall(ev)
	case event.ToolResult:
		b.result(ev)
	case event.Paused, event.Done, event.Error:
		b.closeRuns()
	}
}

func (b *Builder) closeRuns() {
	b.closeText()
	b.closeReasoning(reasoningCompleted)
}

// settle writes the open run's content to its part without closing it, so
// that the parts can be read while a run is still growing.
func (b *Builder) settle() {
	switch {
	case b.text >= 0:
		b.parts[b.text].Text = b.run.String()
	case b.reasoning >= 0:
		b.parts[b.reasoning].Text = b.run.String()
	}
}

func (b *Builder) closeText() {
	if b.text >= 0 {
		b.settle()
		b.run.Reset()
		b.text = -1
	}
}

func (b *Builder) closeReasoning(status string) {
	if b.reasoning >= 0 {
		b.settle()
		b.run.Reset()
		b.parts[b.reasoning].Status = status
		b.parts[b.reasoning].CompletedAt = b.timestamp()
		b.reasoning = -1
		b.version++
	}
}

// modelCallID is the id the model gave a call, without the path that makes
// it unique among nested agents; the readers keep it beside the card's id.
func modelCallID(ev event.Event) string {
	if ev.Parent == "" {
		return ev.CallID
	}
	return ev.CallID[len(ev.Parent)+1:]
}

func (b *Builder) startCall(ev event.Event) {
	if _, known := b.calls[ev.CallID]; known {
		return
	}
	part := Part{Type: TypeToolCall, ToolCallID: ev.CallID, ToolName: ev.Tool, ModelCallID: modelCallID(ev)}
	if b.metadata != nil {
		if value := b.metadata(ev); value != nil {
			// Metadata that cannot be encoded is left off: the card still
			// draws, without its tie.
			if encoded, err := marshal(value); err == nil {
				part.Metadata = encoded
			}
		}
	}
	b.parts = append(b.parts, part)
	b.calls[ev.CallID] = len(b.parts) - 1
	b.version++
}

// fillCall sets a call's final arguments. Arguments that are not a JSON
// object are shown as an empty one, as the stream shows them; the call's
// result, which follows, is the error saying what was wrong.
func (b *Builder) fillCall(ev event.Event) {
	i := b.calls[ev.CallID]
	args := json.RawMessage(ev.Args)
	if len(args) == 0 || args[0] != '{' || !json.Valid(args) || b.unsaved[b.parts[i].ToolName] {
		args = json.RawMessage(`{}`)
	}
	var indented bytes.Buffer
	// Valid JSON always indents.
	_ = json.Indent(&indented, args, "", "  ")
	b.parts[i].Args = args
	b.parts[i].ArgsText = indented.String()
	b.version++
}

func (b *Builder) result(ev event.Event) {
	encoded, err := marshal(b.card(ev))
	if err != nil {
		// A card that cannot be encoded is a bug in that card; the call is
		// still shown as finished, with nothing of the output.
		encoded = []byte(`{"status":"completed"}`)
	}
	i, known := b.calls[ev.CallID]
	if !known {
		if b.late == nil {
			b.late = map[string]json.RawMessage{}
		}
		b.late[ev.CallID] = encoded
		return
	}
	b.parts[i].Result = encoded
	b.version++
}

// Late returns the results that arrived for calls this builder never saw
// start, by call id.
//
// That happens in exactly one case: a turn resumed after a pause. The call
// was made, and its card saved, in the message of the stretch that paused;
// the resumed stretch is a new message, and the first thing it produces is
// that call's result. The result belongs on the card that is already saved,
// so the caller writes it there with Fill. Dropping it, which is what the
// chat used to do, left an approved call looking interrupted for ever.
func (b *Builder) Late() map[string]json.RawMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]json.RawMessage, len(b.late))
	for id, result := range b.late {
		out[id] = result
	}
	return out
}

// Fill writes results onto the tool-call parts of a saved message's content
// and returns the new content, or changed false if no part matched. Parts
// are otherwise left exactly as stored, unknown fields included: the content
// may have been written by another version of the application.
func Fill(content []byte, results map[string]json.RawMessage) (filled []byte, changed bool, err error) {
	var stored []map[string]json.RawMessage
	if err := json.Unmarshal(content, &stored); err != nil {
		return nil, false, err
	}
	for _, part := range stored {
		var kind, id string
		if json.Unmarshal(part["type"], &kind) != nil || kind != TypeToolCall || json.Unmarshal(part["toolCallId"], &id) != nil {
			continue
		}
		result, has := results[id]
		if !has {
			continue
		}
		part["result"] = result
		// A call with a result was not cut off.
		delete(part, "state")
		changed = true
	}
	if !changed {
		return content, false, nil
	}
	filled, err = marshal(stored)
	return filled, true, err
}

// timestamp is the time in the form the readers have always been sent:
// Python's isoformat of a UTC time.
func (b *Builder) timestamp() string {
	now := b.now().UTC()
	if now.Nanosecond()/1000 == 0 {
		return now.Format("2006-01-02T15:04:05+00:00")
	}
	return now.Format("2006-01-02T15:04:05.000000+00:00")
}

// Version changes whenever the parts do.
func (b *Builder) Version() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.version
}

// Snapshot returns the parts as they stand, for saving progress while the
// turn runs. Nothing is closed: an open reasoning run is saved as running.
func (b *Builder) Snapshot() []Part {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.settle()
	return append([]Part(nil), b.parts...)
}

// Finish returns the parts of a turn that is over, however it ended.
//
// A reasoning run still open is marked interrupted. A tool call with no
// result is marked aborted unless waiting says it is waiting for a person:
// a call paused for approval is not aborted, its card is the approval card.
// rewrite is applied to every text part (the citation markers). A turn that
// produced nothing at all gets one status part saying so, because an empty
// message renders as a blank bubble that looks like a bug.
func (b *Builder) Finish(waiting map[string]bool, rewrite func(string) string) []Part {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeText()
	b.closeReasoning(reasoningInterrupted)
	out := make([]Part, 0, len(b.parts)+1)
	for _, part := range b.parts {
		switch part.Type {
		case TypeText:
			part.Text = rewrite(part.Text)
		case TypeToolCall:
			if part.Result == nil && !waiting[part.ToolCallID] {
				part.State = stateAborted
			}
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		out = append(out, Part{Type: TypeStatus, Code: "no_response", Text: "No response was produced."})
	}
	return out
}
