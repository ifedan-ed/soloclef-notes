package agent_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// recorder is a sink that keeps what it is sent. It locks because tests read
// it from their own goroutine while a run is still emitting, not because the
// run needs it to: the run already calls a sink from one goroutine at a time.
type recorder struct {
	mu     sync.Mutex
	events []event.Event
}

func (r *recorder) sink(ev event.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recorder) all() []event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]event.Event(nil), r.events...)
}

// of returns the recorded events of one kind.
func (r *recorder) of(kind event.Kind) []event.Event {
	var out []event.Event
	for _, ev := range r.all() {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// trace renders the stream as one line of kinds, with the call id where
// there is one, which is the readable way to assert an order.
func (r *recorder) trace() string {
	var parts []string
	for _, ev := range r.all() {
		part := ev.Kind.String()
		if ev.CallID != "" {
			part += "(" + ev.CallID + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

// text joins the text deltas of the top-level run: the answer as a reader of
// the stream saw it.
func (r *recorder) text() string {
	var b strings.Builder
	for _, ev := range r.of(event.TextDelta) {
		if ev.Parent == "" {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

func call(id, name, args string) model.ToolCall {
	return model.ToolCall{ID: id, Name: name, Args: args}
}

const anyObject = `{"type":"object"}`

// countingTool returns a tool that counts its runs and answers with its name
// and arguments, so a test can see both that it ran and what it was given.
func countingTool(name string, approval tool.Approval, runs *atomic.Int32) tool.Tool {
	return tool.Tool{
		Name:       name,
		Parameters: []byte(anyObject),
		Approval:   approval,
		Run: func(_ context.Context, c tool.Call) (string, error) {
			runs.Add(1)
			return name + " got " + string(c.Args), nil
		},
	}
}

// build makes an agent with the limits most tests do not care about filled
// in. A test that is about a limit sets it and the value here is not used.
func build(t *testing.T, cfg agent.Config) *agent.Agent {
	t.Helper()
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 20
	}
	if cfg.MaxParallelTools == 0 {
		cfg.MaxParallelTools = 4
	}
	if cfg.Store == nil {
		cfg.Store = checkpoint.NewMemory()
	}
	a, err := agent.New(cfg)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	return a
}

func script(replies ...modeltest.Reply) *modeltest.Script {
	return modeltest.NewScript(replies...)
}

func user(text string) []model.Message {
	return []model.Message{model.User(text)}
}

// roles renders a conversation as "role:content" entries, enough to assert
// what the model was shown without comparing whole structs.
func roles(msgs []model.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		switch {
		case m.Role == model.RoleTool:
			out[i] = "tool[" + m.ToolCallID + "]:" + m.Content
		case len(m.ToolCalls) > 0:
			ids := make([]string, len(m.ToolCalls))
			for j, c := range m.ToolCalls {
				ids[j] = c.ID
			}
			out[i] = string(m.Role) + ":" + m.Content + "->" + strings.Join(ids, ",")
		default:
			out[i] = string(m.Role) + ":" + m.Content
		}
	}
	return out
}

func equal(t *testing.T, what string, got, want any) {
	t.Helper()
	g, w := render(got), render(want)
	if g != w {
		t.Fatalf("%s\n got: %s\nwant: %s", what, g, w)
	}
}

func render(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []string:
		return strings.Join(x, " | ")
	}
	panic("render: unsupported type")
}
