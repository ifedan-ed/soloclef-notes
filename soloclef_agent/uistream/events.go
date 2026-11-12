package uistream

import (
	"bytes"
	"encoding/json"
	"errors"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
)

// Event writes the frames for one event. It has the signature of an
// event.Sink, so a run can be handed w.Event directly, or a function that
// does the chat layer's own bookkeeping and then calls it.
func (w *Writer) Event(ev event.Event) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if ev.Parent != "" {
		w.nested(ev)
		return
	}
	switch ev.Kind {
	case event.TextDelta:
		w.closeReasoning()
		if w.textID == "" {
			w.textID = w.cfg.NewID("text")
			w.block("text-start", w.textID, "", "")
		}
		w.frame(func(b []byte) []byte {
			b = appendString(append(b, `{"type": "text-delta", "id": `...), w.textID)
			return appendString(append(b, `, "delta": `...), ev.Text)
		})

	case event.ReasoningDelta:
		w.closeText()
		if w.reasoningID == "" {
			w.reasoningID = w.cfg.NewID("reasoning")
			w.block("reasoning-start", w.reasoningID, "startedAt", w.timestamp())
		}
		w.frame(func(b []byte) []byte {
			b = appendString(append(b, `{"type": "reasoning-delta", "id": `...), w.reasoningID)
			return appendString(append(b, `, "delta": `...), ev.Text)
		})

	case event.ToolCallStarted:
		w.closeBlocks()
		w.toolInputStart(ev)

	case event.ToolCallDelta:
		w.frame(func(b []byte) []byte {
			b = appendString(append(b, `{"type": "tool-input-delta", "toolCallId": `...), ev.CallID)
			return appendString(append(b, `, "inputTextDelta": `...), ev.Text)
		})

	case event.ToolCallReady:
		w.toolInputAvailable(ev)

	case event.ToolResult:
		w.toolOutput(ev)

	case event.Notice:
		w.notice(ev)

	case event.ApprovalNeeded:
		w.interrupt(ev)

	case event.Paused, event.Done, event.Error:
		// The end of the run closes what is open. The closing frames
		// themselves are End's or Fail's, because the chat layer has frames
		// of its own to send before them.
		w.closeBlocks()
	}
}

// nested writes the frames for an event from a nested agent. Its model's
// text, reasoning and half-formed tool calls are not sent: that is the
// specialist's working, and printing it would put a second voice in the
// answer. What it did is sent, as cards: each of its tool calls once the
// call is complete, each result, and each approval it needs.
func (w *Writer) nested(ev event.Event) {
	switch ev.Kind {
	case event.ToolCallReady:
		w.closeBlocks()
		w.toolInputStart(ev)
		w.toolInputAvailable(ev)
	case event.ToolResult:
		w.toolOutput(ev)
	case event.Notice:
		w.notice(ev)
	case event.ApprovalNeeded:
		w.interrupt(ev)
	}
}

// notice writes a tool's notice as the side frame of its name: a notice
// named "activity" is a "data-activity" frame whose data is the tool's
// payload, as the tool built it. That is how a tool deep inside a nested
// agent moves the page's progress line while its call is still running.
//
// A side frame interrupts nothing: it is written between the deltas of an
// open text or reasoning block without closing it, which is what the readers
// expect of every "data-" frame. The call's id is not added to the payload.
// A tool that wants its notice tied to its card puts the id in the payload
// itself (tool.Call.ID), in whatever field that frame's reader looks for.
func (w *Writer) notice(ev event.Event) {
	w.frameErr(func(b []byte) ([]byte, error) {
		if !json.Valid(ev.Payload) {
			return b, errors.New("uistream: notice " + ev.Name + " from " + ev.Tool + " is not valid JSON")
		}
		b = appendString(append(b, `{"type": `...), "data-"+ev.Name)
		var compact bytes.Buffer
		// Valid JSON always compacts.
		_ = json.Compact(&compact, ev.Payload)
		return respace(append(b, `, "data": `...), compact.Bytes()), nil
	})
}

// block writes a start or end frame of a text or reasoning block, with an
// optional timestamp field.
func (w *Writer) block(frameType, id, timeField, timeValue string) {
	w.frame(func(b []byte) []byte {
		b = appendString(append(b, `{"type": `...), frameType)
		b = appendString(append(b, `, "id": `...), id)
		if timeField != "" {
			b = appendString(append(appendString(append(b, ", "...), timeField), ": "...), timeValue)
		}
		return b
	})
}

func (w *Writer) closeText() {
	if w.textID != "" {
		w.block("text-end", w.textID, "", "")
		w.textID = ""
	}
}

func (w *Writer) closeReasoning() {
	if w.reasoningID != "" {
		w.block("reasoning-end", w.reasoningID, "completedAt", w.timestamp())
		w.reasoningID = ""
	}
}

func (w *Writer) closeBlocks() {
	w.closeText()
	w.closeReasoning()
}

// timestamp formats the time as Python's datetime.isoformat does for a UTC
// time: microseconds, left out altogether when they are zero, and "+00:00".
func (w *Writer) timestamp() string {
	now := w.cfg.Now().UTC()
	if now.Nanosecond()/1000 == 0 {
		return now.Format("2006-01-02T15:04:05+00:00")
	}
	return now.Format("2006-01-02T15:04:05.000000+00:00")
}

// modelCallID returns the id the model gave the call, without the path that
// makes it unique among nested agents. The clients know it as
// "langchainToolCallId" and join the action log on it; the field keeps its
// old name because the name is part of the wire.
func modelCallID(ev event.Event) string {
	if ev.Parent == "" {
		return ev.CallID
	}
	return ev.CallID[len(ev.Parent)+1:]
}

// toolTail appends the two fields every tool frame ends with.
func (w *Writer) toolTail(b []byte, ev event.Event) ([]byte, error) {
	b = appendString(append(b, `, "langchainToolCallId": `...), modelCallID(ev))
	if w.cfg.Metadata == nil {
		return b, nil
	}
	if md := w.cfg.Metadata(ev); md != nil {
		return appendValue(append(b, `, "metadata": `...), md)
	}
	return b, nil
}

func (w *Writer) toolInputStart(ev event.Event) {
	w.frameErr(func(b []byte) ([]byte, error) {
		b = appendString(append(b, `{"type": "tool-input-start", "toolCallId": `...), ev.CallID)
		b = appendString(append(b, `, "toolName": `...), ev.Tool)
		return w.toolTail(b, ev)
	})
}

func (w *Writer) toolInputAvailable(ev event.Event) {
	w.frameErr(func(b []byte) ([]byte, error) {
		b = appendString(append(b, `{"type": "tool-input-available", "toolCallId": `...), ev.CallID)
		b = appendString(append(b, `, "toolName": `...), ev.Tool)
		b = appendArgs(append(b, `, "input": `...), ev.Args)
		return w.toolTail(b, ev)
	})
}

func (w *Writer) toolOutput(ev event.Event) {
	w.frameErr(func(b []byte) ([]byte, error) {
		b = appendString(append(b, `{"type": "tool-output-available", "toolCallId": `...), ev.CallID)
		b, err := appendValue(append(b, `, "output": `...), w.cfg.Card(ev))
		if err != nil {
			return b, err
		}
		return w.toolTail(b, ev)
	})
}

// appendArgs writes a call's arguments as the object the clients require
// "input" to be. Arguments that are not a JSON object cannot be shown as
// one; they are written as an empty object, and the call's result, which
// follows, is the error saying what was wrong with them.
func appendArgs(b []byte, args string) []byte {
	raw := []byte(args)
	if len(raw) == 0 || raw[0] != '{' || !json.Valid(raw) {
		return append(b, "{}"...)
	}
	// Valid JSON always encodes.
	b, _ = appendValue(b, json.RawMessage(raw))
	return b
}

// interrupt writes the request for a person's decision on one call.
//
// A gated call is written in the shape the approval cards read: the action,
// the decisions allowed for it, and the chat layer's card type and context.
// "approve_always" is offered only when the loop would honour it, so the
// button is never shown for a tool that asks every time.
//
// A question a tool asked from its own body is that tool's payload, sent as
// it is: its shape is between the tool and its card. In both cases
// "tool_call_id" is added last, and is what the decision is sent back with.
func (w *Writer) interrupt(ev event.Event) {
	w.frameErr(func(b []byte) ([]byte, error) {
		return appendInterrupt(append(b, `{"type": "data-interrupt-request", "data": `...), ev, w.cfg.Approval)
	})
}

// InterruptData returns the "data" object of the frame that asks a person
// about one waiting call, exactly as the stream writes it.
//
// It is exported because the stream is not the only place the request is
// read: a page that reloads asks the server which calls are still waiting,
// and the answer has to be the object the card was first drawn from. One
// function builds both, so the two cannot drift.
func InterruptData(waiting event.Event, approval func(event.Event) ApprovalCard) ([]byte, error) {
	if approval == nil {
		return nil, errors.New("uistream: InterruptData needs the approval card function")
	}
	return appendInterrupt(nil, waiting, approval)
}

// appendInterrupt appends the request's data object to b.
func appendInterrupt(b []byte, ev event.Event, approval func(event.Event) ApprovalCard) ([]byte, error) {
	b = append(b, '{')
	if ev.Payload != nil {
		inner, err := objectBody(ev.Payload)
		if err != nil {
			return b, err
		}
		if len(inner) > 0 {
			b = append(respace(b, inner), ", "...)
		}
	} else {
		card := approval(ev)
		b = appendString(append(b, `"action_requests": [{"name": `...), ev.Tool)
		b = appendArgs(append(b, `, "args": `...), ev.Args)
		b = appendString(append(b, `}], "review_configs": [{"action_name": `...), ev.Tool)
		b = append(b, `, "allowed_decisions": ["approve", "edit", "reject"`...)
		if ev.AllowAlways {
			b = append(b, `, "approve_always"`...)
		}
		b = appendString(append(b, `]}], "interrupt_type": `...), card.InterruptType)
		if card.Context != nil {
			var err error
			if b, err = appendValue(append(b, `, "context": `...), card.Context); err != nil {
				return b, err
			}
		}
		b = append(b, ", "...)
	}
	b = appendString(append(b, `"tool_call_id": `...), ev.CallID)
	return append(b, '}'), nil
}
