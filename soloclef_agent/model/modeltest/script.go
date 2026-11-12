// Package modeltest is a model that says what a test tells it to. It is
// exported, and not hidden in a _test file, because code built on the agent
// loop needs the same thing to test itself without a network.
package modeltest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// Reply is one scripted answer to one model call.
type Reply struct {
	// Text is the answer. It is streamed a word at a time.
	Text string

	// Reasoning is streamed before the text, a word at a time.
	Reasoning string

	// Calls are the tool calls the model makes.
	Calls []model.ToolCall

	// Silent makes the reply arrive without any chunks, the way an adapter
	// that cannot stream behaves.
	Silent bool

	// Hang makes the call block, after streaming, until its context ends.
	// It is how a test holds a run in the middle of a model call.
	Hang bool

	// Err, when set, is returned instead of a response, after streaming.
	Err error

	FinishReason string
	Usage        model.Usage
}

// Script is a model.Model that plays a list of replies in order, one per
// call, and records what it was sent. A call beyond the end of the list is an
// error naming the call, so a loop that runs longer than a test expected
// fails loudly instead of hanging or repeating.
type Script struct {
	mu       sync.Mutex
	replies  []Reply
	requests []model.Request
}

// NewScript returns a model that will give the replies in order.
func NewScript(replies ...Reply) *Script {
	return &Script{replies: replies}
}

// Requests returns every request received so far, in order.
func (s *Script) Requests() []model.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.Request(nil), s.requests...)
}

func (s *Script) Generate(ctx context.Context, req model.Request, stream func(model.Chunk)) (model.Response, error) {
	s.mu.Lock()
	n := len(s.requests)
	// The messages are copied because the caller goes on appending to its
	// conversation, and a test must see the request as it was sent.
	s.requests = append(s.requests, model.Request{
		Messages: append([]model.Message(nil), req.Messages...),
		Tools:    append([]model.ToolSpec(nil), req.Tools...),
	})
	if n >= len(s.replies) {
		s.mu.Unlock()
		return model.Response{}, fmt.Errorf("modeltest: call %d was made but only %d replies were scripted", n+1, len(s.replies))
	}
	reply := s.replies[n]
	s.mu.Unlock()

	if !reply.Silent {
		for _, word := range strings.SplitAfter(reply.Reasoning, " ") {
			if word != "" {
				stream(model.Chunk{Kind: model.ChunkReasoning, Text: word})
			}
		}
		for _, word := range strings.SplitAfter(reply.Text, " ") {
			if word != "" {
				stream(model.Chunk{Kind: model.ChunkText, Text: word})
			}
		}
		for i, call := range reply.Calls {
			stream(model.Chunk{Kind: model.ChunkToolStart, Index: i, ID: call.ID, Name: call.Name})
			// Two fragments, so a consumer that only handles whole arguments
			// in one piece is caught.
			half := len(call.Args) / 2
			for _, part := range []string{call.Args[:half], call.Args[half:]} {
				if part != "" {
					stream(model.Chunk{Kind: model.ChunkToolArgs, Index: i, Text: part})
				}
			}
		}
	}
	if reply.Hang {
		<-ctx.Done()
		return model.Response{}, ctx.Err()
	}
	if reply.Err != nil {
		return model.Response{}, reply.Err
	}

	finish := reply.FinishReason
	if finish == "" {
		finish = "stop"
		if len(reply.Calls) > 0 {
			finish = "tool_calls"
		}
	}
	return model.Response{
		Message:      model.Message{Role: model.RoleAssistant, Content: reply.Text, ToolCalls: reply.Calls},
		FinishReason: finish,
		Usage:        reply.Usage,
	}, nil
}

// Func adapts a function to model.Model, for the test whose model has to
// decide its reply from the request.
type Func func(ctx context.Context, req model.Request, stream func(model.Chunk)) (model.Response, error)

func (f Func) Generate(ctx context.Context, req model.Request, stream func(model.Chunk)) (model.Response, error) {
	return f(ctx, req, stream)
}
