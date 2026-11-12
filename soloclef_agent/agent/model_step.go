package agent

import (
	"context"
	"fmt"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// modelStep calls the model once, forwards what it streams as events, and
// records its message. If the message asks for tools, they become the batch
// the next step runs.
func (t *turn) modelStep(ctx context.Context) error {
	cfg := &t.agent.cfg
	step := t.state.Steps

	sent := t.state.Messages
	if cfg.Prepare != nil {
		var err error
		if sent, err = cfg.Prepare(ctx, t.state.Messages); err != nil {
			return fmt.Errorf("agent: prepare messages: %w", err)
		}
	}
	if cfg.System != "" {
		// A fresh slice, so the system prompt is never written into the
		// stored conversation by an append that happened to have capacity.
		withSystem := make([]model.Message, 0, len(sent)+1)
		withSystem = append(withSystem, model.Message{Role: model.RoleSystem, Content: cfg.System})
		sent = append(withSystem, sent...)
	}

	t.stepIDs = t.stepIDs[:0]
	stream := func(c model.Chunk) {
		switch c.Kind {
		case model.ChunkText:
			t.emit(event.Event{Kind: event.TextDelta, Step: step, Text: c.Text})
		case model.ChunkReasoning:
			t.emit(event.Event{Kind: event.ReasoningDelta, Step: step, Text: c.Text})
		case model.ChunkToolStart:
			if id, isNew := t.claimID(c.Index, c.ID); isNew {
				t.emit(event.Event{Kind: event.ToolCallStarted, Step: step, CallID: id, Tool: c.Name})
			}
		case model.ChunkToolArgs:
			// A fragment for a call that was never announced has no card to
			// land on. The chunks are a preview; the complete arguments
			// arrive with ToolCallReady either way.
			if c.Index >= 0 && c.Index < len(t.stepIDs) && t.stepIDs[c.Index] != "" {
				t.emit(event.Event{Kind: event.ToolCallDelta, Step: step, CallID: t.stepIDs[c.Index], Text: c.Text})
			}
		}
	}
	resp, err := t.agent.generate(ctx, ModelCall{
		Thread: t.thread, Step: step,
		Request: model.Request{Messages: sent, Tools: t.agent.specs},
		Stream:  stream,
	})
	if err != nil {
		return fmt.Errorf("agent: model call: %w", err)
	}

	msg := resp.Message
	msg.Role = model.RoleAssistant
	// Copy the calls: their ids are about to be settled, and the adapter's
	// slice is not ours to write to.
	msg.ToolCalls = append([]model.ToolCall(nil), msg.ToolCalls...)
	batch := make([]Call, len(msg.ToolCalls))
	for i := range msg.ToolCalls {
		call := &msg.ToolCalls[i]
		id, isNew := t.claimID(i, call.ID)
		call.ID = id
		if call.Args == "" {
			// Providers send no argument text at all for a call to a tool
			// that takes none. That is a call with an empty object, and is
			// stored as one so that everything after this line can rely on
			// arguments being an object or being wrong.
			call.Args = "{}"
		}
		if isNew {
			// The adapter did not stream this call. Announce it now, so that
			// "started, ready, result" holds for every call and a consumer
			// needs one code path, not one per provider.
			t.emit(event.Event{Kind: event.ToolCallStarted, Step: step, CallID: id, Tool: call.Name})
		}
		t.emit(event.Event{Kind: event.ToolCallReady, Step: step, CallID: id, Tool: call.Name, Args: call.Args})
		batch[i] = Call{ID: id, Name: call.Name, Args: call.Args, Status: CallPending}
	}
	if len(batch) == 0 {
		batch = nil
	}

	t.state.Messages = append(t.state.Messages, msg)
	t.state.Batch = batch
	t.finishReason = resp.FinishReason
	t.emit(event.Event{Kind: event.ModelFinished, Step: step, Usage: resp.Usage, FinishReason: resp.FinishReason})
	return nil
}

// claimID settles the id of the call at position index of the current model
// step, and reports whether this is the first time that position was seen.
//
// The provider's id is kept whenever it can be: it is what the provider
// expects back, and what logs elsewhere are joined on. It is replaced in two
// cases only, when it is empty and when it has already been used in this
// thread. Both happen with real providers, and both break things downstream:
// an id is the key of a card in the client and the address of an approval,
// so two calls sharing one would be shown as one and approved as one. The
// replacement is built from the step and the position, which cannot repeat
// within a turn, and is what gets stored and sent back, so the conversation
// stays consistent with itself.
//
// An id holding a slash is replaced too. The slash is the separator in the
// path that names a delegate's calls ("k1/w1": call w1 inside the delegate
// called by k1), and an id that contained one would be split at the wrong
// place when an approval is routed down to it.
func (t *turn) claimID(index int, proposed string) (id string, isNew bool) {
	if index < 0 {
		index = 0
	}
	for len(t.stepIDs) <= index {
		t.stepIDs = append(t.stepIDs, "")
	}
	if t.stepIDs[index] != "" {
		return t.stepIDs[index], false
	}

	if t.usedIDs == nil {
		// Built on first need, once per run: most runs of a chat make no
		// tool call at all and never pay for it.
		t.usedIDs = make(map[string]struct{})
		for i := range t.state.Messages {
			for _, c := range t.state.Messages[i].ToolCalls {
				t.usedIDs[c.ID] = struct{}{}
			}
		}
	}
	id = proposed
	for n := 0; ; n++ {
		if _, used := t.usedIDs[id]; id != "" && !used && !strings.Contains(id, "/") {
			break
		}
		id = fmt.Sprintf("call_%d_%d", t.state.Steps, index)
		if n > 0 {
			id = fmt.Sprintf("call_%d_%d_%d", t.state.Steps, index, n)
		}
	}
	t.usedIDs[id] = struct{}{}
	t.stepIDs[index] = id
	return id, true
}
