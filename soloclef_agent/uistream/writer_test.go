package uistream_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/uistream"
)

// stream is a Writer over a buffer with a clock and ids a test controls.
type stream struct {
	*uistream.Writer
	mu  sync.Mutex
	out bytes.Buffer
	now time.Time
	ids int
}

func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.Write(p)
}

func (s *stream) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.String()
}

func newStream(t *testing.T, change func(*uistream.Config)) *stream {
	t.Helper()
	s := &stream{now: time.Date(2026, 11, 11, 9, 30, 0, 250_000_000, time.UTC)}
	cfg := uistream.Config{
		Out:   s,
		Flush: func() error { return nil },
		Now:   func() time.Time { return s.now },
		NewID: func(kind string) string { s.ids++; return kind + "_" + strconv.Itoa(s.ids) },
		Card:  uistream.LengthOnly,
		Approval: func(event.Event) uistream.ApprovalCard {
			return uistream.ApprovalCard{InterruptType: "permission_ask"}
		},
	}
	if change != nil {
		change(&cfg)
	}
	w, err := uistream.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.Writer = w
	return s
}

// read parses a stream the way the web app's readSSEStream does: split on
// blank lines, take the lines that start "data: ", skip "[DONE]", and parse
// the rest as JSON. A frame that the app's reader would drop fails the test.
func read(t *testing.T, raw string) []map[string]any {
	t.Helper()
	if !strings.HasSuffix(raw, "\n\n") {
		t.Fatalf("stream does not end with a blank line: %q", raw)
	}
	var frames []map[string]any
	for _, block := range strings.Split(strings.TrimSuffix(raw, "\n\n"), "\n\n") {
		for _, line := range strings.Split(block, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimSpace(line[len("data: "):])
			if data == "[DONE]" {
				frames = append(frames, map[string]any{"type": "[DONE]"})
				continue
			}
			var frame map[string]any
			if err := json.Unmarshal([]byte(data), &frame); err != nil {
				t.Fatalf("the app's reader would drop this frame (%v): %s", err, data)
			}
			frames = append(frames, frame)
		}
	}
	return frames
}

func types(frames []map[string]any) string {
	var out []string
	for _, f := range frames {
		out = append(out, f["type"].(string))
	}
	return strings.Join(out, " ")
}

// golden reads a stream printed by the Python service's own encoder; see
// testdata/python_frames.py, which wrote the files. They are the wire as it
// is today.
func golden(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The same awkward text testdata/python_frames.py sends, built from code
// points so that this file, like that one, stays plain ASCII and no editor
// or transport can quietly change what is being tested.
var (
	awkward = "Caf" + string(rune(0xe9)) + " " + string(rune(0x2615)) + " na" + string(rune(0xef)) + "ve " + string(rune(0x1f600)) +
		" " + string(rune(0x7f)) + string(rune(0x01)) + "\t\\ end" + string(rune(0x2028))
	ete = string(rune(0xe9)) + "t" + string(rune(0xe9))
)

func TestFramesAreByteForByteWhatThePythonServiceWrites(t *testing.T) {
	s := newStream(t, func(c *uistream.Config) {
		c.Approval = func(event.Event) uistream.ApprovalCard {
			return uistream.ApprovalCard{InterruptType: "permission_ask", Context: struct {
				Server string `json:"mcp_server"`
			}{"Files"}}
		}
	})
	s.Begin("msg_1")
	s.Data("turn-info", struct {
		TurnID string `json:"chat_turn_id"`
	}{"turn-9"})
	s.Event(event.Event{Kind: event.ReasoningDelta, Text: "Hmm, \"quotes\" & <tags>\n"})
	s.now = time.Date(2026, 11, 11, 9, 30, 1, 0, time.UTC)
	s.Event(event.Event{Kind: event.TextDelta, Text: awkward})
	s.Event(event.Event{Kind: event.ToolCallStarted, CallID: "call_a", Tool: "search"})
	s.Event(event.Event{Kind: event.ToolCallDelta, CallID: "call_a", Text: `{"q":`})
	s.Event(event.Event{Kind: event.ToolCallReady, CallID: "call_a", Tool: "search", Args: `{"q":"` + ete + `","n":[1,2,{"deep":true}],"none":null}`})
	s.Event(event.Event{Kind: event.ToolResult, CallID: "call_a", Tool: "search", Text: "twelve chars"})
	s.Event(event.Event{Kind: event.ApprovalNeeded, CallID: "call_b", Tool: "delete", Args: `{"id":7}`, AllowAlways: true})
	s.Event(event.Event{Kind: event.Paused})
	s.End()

	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if got, want := s.String(), golden(t, "python_turn.sse"); got != want {
		t.Fatalf("stream differs from the Python service's bytes\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAFailedTurnEndsWithTheErrorFrame(t *testing.T) {
	s := newStream(t, nil)
	s.Begin("msg_1")
	s.Event(event.Event{Kind: event.TextDelta, Text: "Half"})
	s.Event(event.Event{Kind: event.Error, Err: errors.New("raw cause, never shown")})
	s.Fail("Something went wrong "+string(rune(0x2014))+" try again", "SERVER_ERROR")
	// Rule 6: nothing follows the error but the end marker, whatever is
	// sent afterwards.
	s.Event(event.Event{Kind: event.TextDelta, Text: "late"})
	s.End()

	// The last two frames are, again, Python's own bytes.
	want := golden(t, "python_failure.sse")
	got := s.String()
	if !strings.HasSuffix(got, want) {
		t.Fatalf("stream ends\n%s\nwant it to end\n%s", got, want)
	}
	if types(read(t, got)) != "start start-step text-start text-delta text-end error [DONE]" {
		t.Fatalf("frames = %s", types(read(t, got)))
	}
	if strings.Contains(got, "raw cause") {
		t.Fatal("the run's raw error reached the reader")
	}

	// With no code, the field is absent, not empty.
	s = newStream(t, nil)
	s.Fail("No.", "")
	if got := s.String(); got != `data: {"type": "error", "message": "No."}`+"\n\n"+"data: [DONE]\n\n" {
		t.Fatalf("got %q", got)
	}
}

func TestBlocksAreClosedBeforeTheNextKindOfOutputStarts(t *testing.T) {
	s := newStream(t, nil)
	s.Begin("m")
	for _, ev := range []event.Event{
		{Kind: event.ReasoningDelta, Text: "a"},
		{Kind: event.ReasoningDelta, Text: "b"},
		{Kind: event.TextDelta, Text: "c"},
		{Kind: event.ReasoningDelta, Text: "d"},
		{Kind: event.TextDelta, Text: "e"},
		{Kind: event.ToolCallStarted, CallID: "c1", Tool: "t"},
		{Kind: event.ToolCallReady, CallID: "c1", Tool: "t", Args: `{}`},
		{Kind: event.ReasoningDelta, Text: "f"},
		{Kind: event.ToolCallStarted, CallID: "c2", Tool: "t"},
		{Kind: event.TextDelta, Text: "g"},
		{Kind: event.Done},
	} {
		s.Event(ev)
	}
	s.End()

	frames := read(t, s.String())
	want := "start start-step " +
		"reasoning-start reasoning-delta reasoning-delta reasoning-end " +
		"text-start text-delta text-end " +
		"reasoning-start reasoning-delta reasoning-end " +
		"text-start text-delta text-end " +
		"tool-input-start tool-input-available " +
		"reasoning-start reasoning-delta reasoning-end " +
		"tool-input-start " +
		"text-start text-delta text-end " +
		"finish-step finish [DONE]"
	if got := types(frames); got != want {
		t.Fatalf("frames\n got: %s\nwant: %s", got, want)
	}

	// Each block's deltas and its end carry the id its start announced, and
	// no id is used for two blocks.
	open, used := "", map[string]bool{}
	for _, f := range frames {
		id, _ := f["id"].(string)
		switch f["type"] {
		case "text-start", "reasoning-start":
			if used[id] {
				t.Fatalf("block id %q was used twice", id)
			}
			open, used[id] = id, true
		case "text-delta", "reasoning-delta", "text-end", "reasoning-end":
			if id != open {
				t.Fatalf("%s carries id %q inside block %q", f["type"], id, open)
			}
		}
	}
}

func TestOneToolCallUsesOneIDOnEveryFrame(t *testing.T) {
	s := newStream(t, func(c *uistream.Config) {
		c.Metadata = func(ev event.Event) any {
			if ev.Tool == "task" {
				return struct {
					SpanID string `json:"spanId"`
				}{"span-1"}
			}
			return nil
		}
	})
	s.Event(event.Event{Kind: event.ToolCallStarted, CallID: "c1", Tool: "task"})
	s.Event(event.Event{Kind: event.ToolCallDelta, CallID: "c1", Text: `{"task":`})
	s.Event(event.Event{Kind: event.ToolCallReady, CallID: "c1", Tool: "task", Args: `{"task":"x"}`})
	s.Event(event.Event{Kind: event.ToolResult, CallID: "c1", Tool: "task", Text: "ok"})
	s.Event(event.Event{Kind: event.ToolCallReady, CallID: "c2", Tool: "search", Args: `not json`})

	frames := read(t, s.String())
	for _, f := range frames[:4] {
		if f["toolCallId"] != "c1" {
			t.Fatalf("frame %v has toolCallId %v, want c1", f["type"], f["toolCallId"])
		}
	}
	// The metadata hook's value rides on the tool frames it answered for,
	// and is absent, not null, where it answered nil.
	if md, _ := frames[0]["metadata"].(map[string]any); md["spanId"] != "span-1" {
		t.Fatalf("metadata on tool-input-start = %v", frames[0]["metadata"])
	}
	if _, present := frames[4]["metadata"]; present {
		t.Fatalf("metadata present on a frame the hook declined: %v", frames[4])
	}
	// "input" is always an object, because the app reads it as one.
	if input, ok := frames[4]["input"].(map[string]any); !ok || len(input) != 0 {
		t.Fatalf("input for unparseable arguments = %v, want {}", frames[4]["input"])
	}
}

func TestANestedAgentShowsAsCardsAndItsWordsAreNeverSent(t *testing.T) {
	s := newStream(t, nil)
	s.Begin("m")
	s.Event(event.Event{Kind: event.ToolCallStarted, CallID: "k1", Tool: "knowledge_base"})
	s.Event(event.Event{Kind: event.ToolCallReady, CallID: "k1", Tool: "knowledge_base", Args: `{"task":"find X"}`})
	// The specialist, working: everything below carries Parent.
	s.Event(event.Event{Kind: event.StepStarted, Parent: "k1"})
	s.Event(event.Event{Kind: event.ReasoningDelta, Parent: "k1", Text: "SECRET REASONING"})
	s.Event(event.Event{Kind: event.TextDelta, Parent: "k1", Text: "SPECIALIST PROSE"})
	s.Event(event.Event{Kind: event.ToolCallStarted, Parent: "k1", CallID: "k1/s1", Tool: "search"})
	s.Event(event.Event{Kind: event.ToolCallDelta, Parent: "k1", CallID: "k1/s1", Text: `{"q"`})
	s.Event(event.Event{Kind: event.ToolCallReady, Parent: "k1", CallID: "k1/s1", Tool: "search", Args: `{"q":"X"}`})
	s.Event(event.Event{Kind: event.ToolResult, Parent: "k1", CallID: "k1/s1", Tool: "search", Text: "PRIVATE SEARCH OUTPUT"})
	s.Event(event.Event{Kind: event.Done, Parent: "k1"})
	s.Event(event.Event{Kind: event.ToolResult, CallID: "k1", Tool: "knowledge_base", Text: "summary"})
	s.Event(event.Event{Kind: event.TextDelta, Text: "The answer."})
	s.Event(event.Event{Kind: event.Done})
	s.End()

	raw := s.String()
	for _, leaked := range []string{"SECRET REASONING", "SPECIALIST PROSE", "PRIVATE SEARCH OUTPUT"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("%q reached the reader", leaked)
		}
	}
	frames := read(t, raw)
	want := "start start-step tool-input-start tool-input-available " +
		"tool-input-start tool-input-available tool-output-available " +
		"tool-output-available text-start text-delta text-end finish-step finish [DONE]"
	if got := types(frames); got != want {
		t.Fatalf("frames\n got: %s\nwant: %s", got, want)
	}
	// The nested card's id is unique in the stream; the id the model gave
	// the call is carried beside it for the action log to join on.
	nested := frames[4]
	if nested["toolCallId"] != "k1/s1" || nested["langchainToolCallId"] != "s1" || nested["toolName"] != "search" {
		t.Fatalf("nested tool-input-start = %v", nested)
	}
	// A nested Done did not close the top-level run's stream: text came
	// after it.
	if frames[len(frames)-5]["delta"] != "The answer." {
		t.Fatalf("the answer was lost: %v", frames)
	}
}

func TestEachWaitingCallIsOneInterruptFrameInOrder(t *testing.T) {
	s := newStream(t, nil)
	s.Event(event.Event{Kind: event.ApprovalNeeded, CallID: "a", Tool: "send", Args: `{"to":"x"}`, AllowAlways: true})
	s.Event(event.Event{Kind: event.ApprovalNeeded, CallID: "k1/b", Parent: "k1", Tool: "fetch", Args: `{}`})
	// A question from a tool's own body is that tool's payload, untouched
	// but for the id to answer to.
	s.Event(event.Event{Kind: event.ApprovalNeeded, CallID: "c", Tool: "pick", Args: `{}`,
		Payload: []byte(`{"type": "structured_question", "questions": [{"id": "style", "options": ["a", "b"]}]}`)})
	s.Event(event.Event{Kind: event.ApprovalNeeded, CallID: "d", Tool: "pick", Payload: []byte(`{}`)})

	frames := read(t, s.String())
	if types(frames) != strings.TrimSpace(strings.Repeat("data-interrupt-request ", 4)) {
		t.Fatalf("frames = %s", types(frames))
	}
	data := func(i int) map[string]any { return frames[i]["data"].(map[string]any) }

	decisions := func(i int) string {
		cfg := data(i)["review_configs"].([]any)[0].(map[string]any)
		var out []string
		for _, d := range cfg["allowed_decisions"].([]any) {
			out = append(out, d.(string))
		}
		return cfg["action_name"].(string) + ":" + strings.Join(out, ",")
	}
	// "Always allow" is offered exactly when the loop said it would honour it.
	if decisions(0) != "send:approve,edit,reject,approve_always" || decisions(1) != "fetch:approve,edit,reject" {
		t.Fatalf("allowed decisions: %s / %s", decisions(0), decisions(1))
	}
	action := data(0)["action_requests"].([]any)[0].(map[string]any)
	if action["name"] != "send" || action["args"].(map[string]any)["to"] != "x" || data(0)["interrupt_type"] != "permission_ask" {
		t.Fatalf("first interrupt = %v", data(0))
	}
	// The id to answer to is the full one, which for a nested call is what
	// routes the decision to the right agent.
	for i, want := range []string{"a", "k1/b", "c", "d"} {
		if data(i)["tool_call_id"] != want {
			t.Fatalf("interrupt %d answers to %v, want %s", i, data(i)["tool_call_id"], want)
		}
	}
	if data(2)["type"] != "structured_question" || len(data(2)["questions"].([]any)) != 1 {
		t.Fatalf("question payload was not passed through: %v", data(2))
	}
	want := `data: {"type": "data-interrupt-request", "data": {"tool_call_id": "d"}}`
	if lines := strings.Split(strings.TrimSpace(s.String()), "\n\n"); lines[3] != want {
		t.Fatalf("empty question payload:\n got %s\nwant %s", lines[3], want)
	}

	bad := newStream(t, nil)
	bad.Event(event.Event{Kind: event.ApprovalNeeded, CallID: "x", Tool: "pick", Payload: []byte(`"just a string"`)})
	if bad.Err() == nil || bad.String() != "" {
		t.Fatalf("a question that is not an object was sent: %q, err %v", bad.String(), bad.Err())
	}
}

func TestAToolsOutputReachesTheBrowserOnlyThroughItsCard(t *testing.T) {
	s := newStream(t, func(c *uistream.Config) {
		c.Card = func(ev event.Event) any {
			if ev.Tool == "save_document" {
				// This tool's card names the one field it lets through.
				return struct {
					Title string `json:"title"`
				}{"Notes on X"}
			}
			return uistream.LengthOnly(ev)
		}
	})
	s.Event(event.Event{Kind: event.ToolResult, CallID: "c1", Tool: "remote_tool", Text: "PRIVATE: h" + string(rune(0xe9)) + "llo"})
	s.Event(event.Event{Kind: event.ToolResult, CallID: "c2", Tool: "save_document", Text: "PRIVATE too"})

	raw := s.String()
	if strings.Contains(raw, "PRIVATE") {
		t.Fatalf("tool output reached the reader: %s", raw)
	}
	frames := read(t, raw)
	out := frames[0]["output"].(map[string]any)
	// The length is in characters, as the Python service counted it.
	if out["status"] != "completed" || out["result_length"] != float64(14) || len(out) != 2 {
		t.Fatalf("default card = %v", out)
	}
	if frames[1]["output"].(map[string]any)["title"] != "Notes on X" {
		t.Fatalf("named card = %v", frames[1]["output"])
	}
}

func TestAReaderThatHasGoneStopsTheWritingAndIsReported(t *testing.T) {
	gone := errors.New("broken pipe")
	writes := 0
	s := newStream(t, func(c *uistream.Config) {
		c.Out = writerFunc(func(p []byte) (int, error) {
			if writes++; writes > 2 {
				return 0, gone
			}
			return len(p), nil
		})
	})
	s.Begin("m")
	for range 5 {
		s.Event(event.Event{Kind: event.TextDelta, Text: "x"})
	}
	s.End()
	if !errors.Is(s.Err(), gone) {
		t.Fatalf("Err = %v, want the write error", s.Err())
	}
	if writes != 3 {
		t.Fatalf("%d writes attempted, want 3: nothing is written after the first failure", writes)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestKeepAliveBreaksSilenceOnly(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(1000, 0)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }

	s := newStream(t, func(c *uistream.Config) { c.Now = clock })
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- s.KeepAlive(ctx, 5*time.Millisecond) }()

	// The stream is busy by its own clock: ticks pass, nothing is written.
	time.Sleep(30 * time.Millisecond)
	if got := s.String(); got != "" {
		t.Fatalf("a keep-alive was written to a stream that was not silent: %q", got)
	}
	// Now it has been silent for longer than the interval.
	advance(time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for s.String() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	// The exact bytes the Python service sends, which every SSE reader
	// skips: the app's reader takes only lines that start "data: ".
	if got := s.String(); !strings.HasPrefix(got, ": keepalive\n\n") {
		t.Fatalf("keep-alive = %q", got)
	}
	if frames := read(t, s.String()); len(frames) != 0 {
		t.Fatalf("the app's reader would see the keep-alive as %v", frames)
	}

	// After the stream has ended, nothing more is written.
	s.End()
	ended := s.String()
	advance(time.Hour)
	time.Sleep(30 * time.Millisecond)
	if s.String() != ended {
		t.Fatal("a keep-alive was written after the end of the stream")
	}
	cancel()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := s.KeepAlive(context.Background(), 0); err == nil {
		t.Fatal("a zero interval was accepted")
	}
}

func TestNewRefusesAnIncompleteConfig(t *testing.T) {
	for want, breakIt := range map[string]func(*uistream.Config){
		"Out is not set":      func(c *uistream.Config) { c.Out = nil },
		"Flush is not set":    func(c *uistream.Config) { c.Flush = nil },
		"Now is not set":      func(c *uistream.Config) { c.Now = nil },
		"NewID is not set":    func(c *uistream.Config) { c.NewID = nil },
		"Card is not set":     func(c *uistream.Config) { c.Card = nil },
		"Approval is not set": func(c *uistream.Config) { c.Approval = nil },
	} {
		cfg := uistream.Config{
			Out: &bytes.Buffer{}, Flush: func() error { return nil }, Now: time.Now, NewID: uistream.RandomID,
			Card: uistream.LengthOnly, Approval: func(event.Event) uistream.ApprovalCard { return uistream.ApprovalCard{} },
		}
		breakIt(&cfg)
		if _, err := uistream.New(cfg); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("New error = %v, want one containing %q", err, want)
		}
	}
	if a, b := uistream.RandomID("text"), uistream.RandomID("text"); a == b || len(a) != len("text_")+32 {
		t.Fatalf("RandomID gave %q then %q", a, b)
	}
}

// The keep-alive is due when the stream has been silent for the interval,
// counted from its last frame, not at the next tick of a fixed clock. On a
// fixed tick, a frame written just after one tick makes the next tick find
// the stream not quite silent enough, and the keep-alive waits for the tick
// after: nearly twice the interval of silence.
func TestKeepAliveComesOneIntervalAfterTheLastFrame(t *testing.T) {
	const interval = 200 * time.Millisecond
	s := newStream(t, func(c *uistream.Config) { c.Now = time.Now })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.KeepAlive(ctx, interval) }()

	// The first keep-alive comes one interval after the start. A frame
	// follows it a little later, where a fixed tick would be at its worst.
	time.Sleep(interval + interval/10)
	if n := strings.Count(s.String(), ": keepalive"); n != 1 {
		t.Fatalf("%d keep-alives in the first interval and a bit, want 1", n)
	}
	s.Data("activity", map[string]string{"step": "searching"})
	wrote := time.Now()

	deadline := time.Now().Add(2 * time.Second)
	for strings.Count(s.String(), ": keepalive") < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	silence := time.Since(wrote)
	if strings.Count(s.String(), ": keepalive") < 2 {
		t.Fatal("no keep-alive followed the frame")
	}
	// A fixed tick would have left about 1.9 intervals of silence here.
	if silence < interval || silence > interval*3/2 {
		t.Fatalf("keep-alive came %s after the last frame, want about %s", silence, interval)
	}
}

// A tool's notice is the side frame of its name, from the main run or from
// inside a specialist, and it interrupts nothing: the text block that was
// open when it arrived is still the one being written to after it.
func TestAToolsNoticeIsASideFrameThatInterruptsNothing(t *testing.T) {
	s := newStream(t, nil)
	s.Begin("m")
	s.Event(event.Event{Kind: event.TextDelta, Text: "Building "})
	s.Event(event.Event{Kind: event.Notice, Parent: "k1", CallID: "k1/r1", Tool: "render", Name: "activity",
		Payload: []byte(`{"text":"Rendering ` + ete + `","n":2}`)})
	s.Event(event.Event{Kind: event.TextDelta, Text: "it."})
	s.Event(event.Event{Kind: event.Notice, CallID: "c1", Tool: "write", Name: "documents-updated", Payload: []byte(`{"ids":[7]}`)})
	s.Event(event.Event{Kind: event.Done})
	s.End()
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	frames := read(t, s.String())
	want := "start start-step text-start text-delta data-activity text-delta data-documents-updated text-end finish-step finish [DONE]"
	if got := types(frames); got != want {
		t.Fatalf("frames\n got: %s\nwant: %s", got, want)
	}
	// Written as the Python service writes any frame: spaced, and ASCII only.
	if line := `data: {"type": "data-activity", "data": {"text": "Rendering \u00e9t\u00e9", "n": 2}}` + "\n\n"; !strings.Contains(s.String(), line) {
		t.Fatalf("the notice frame is not %q in\n%s", line, s.String())
	}

	// A payload that is not JSON is a bug in whoever built the event. It is
	// reported, and not sent for the reader to choke on.
	bad := newStream(t, nil)
	bad.Begin("m")
	bad.Event(event.Event{Kind: event.Notice, Tool: "render", Name: "activity", Payload: []byte(`{"text":`)})
	if err := bad.Err(); err == nil || !strings.Contains(err.Error(), "notice activity from render") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(bad.String(), "data-activity") {
		t.Fatal("a broken notice was written")
	}
}

// What a reloaded page is told about a waiting call is the object the stream
// sent for it, byte for byte.
func TestInterruptDataIsTheFramesDataObject(t *testing.T) {
	approval := func(event.Event) uistream.ApprovalCard {
		return uistream.ApprovalCard{InterruptType: "permission_ask", Context: map[string]any{"tool": "write_file"}}
	}
	for name, ev := range map[string]event.Event{
		"a gated call": {Kind: event.ApprovalNeeded, CallID: "k1/w1", Parent: "k1", Tool: "write_file", Args: `{"path":"a.md"}`, AllowAlways: true},
		"a question":   {Kind: event.ApprovalNeeded, CallID: "q1", Tool: "style", Payload: []byte(`{"type":"structured_question","version":1}`)},
	} {
		var out bytes.Buffer
		w, err := uistream.New(uistream.Config{Out: &out, Flush: func() error { return nil }, Now: time.Now, NewID: uistream.RandomID, Card: uistream.LengthOnly, Approval: approval})
		if err != nil {
			t.Fatal(err)
		}
		w.Event(ev)
		data, err := uistream.InterruptData(ev, approval)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := `data: {"type": "data-interrupt-request", "data": ` + string(data) + "}\n\n"
		if out.String() != want {
			t.Errorf("%s:\nframe: %s\n data: %s", name, out.String(), want)
		}
	}
	if _, err := uistream.InterruptData(event.Event{}, nil); err == nil {
		t.Error("a nil approval function was accepted")
	}
}
