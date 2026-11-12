package model

import "context"

// Model is one model reached one way. An implementation is bound to a single
// model name and endpoint when it is built: the request carries no model
// field, so there is nowhere for a router, an alias or a second-choice model
// to hide.
//
// Anything that should happen around a model call (retrying, logging, timing)
// is a Model that wraps another Model; WithRetry is the pattern. That is the
// whole "middleware" mechanism: ordinary nesting, read top to bottom in the
// code that builds the agent, with no slot list to keep in order.
type Model interface {
	// Generate sends the conversation and returns the complete assistant
	// message. While the answer arrives it calls stream once per fragment, on
	// the calling goroutine, in the order received; stream is never nil.
	//
	// The returned Response is authoritative. The chunks are a preview of it
	// for a person watching, and an implementation that cannot stream may
	// emit none.
	Generate(ctx context.Context, req Request, stream func(Chunk)) (Response, error)
}

// Request is everything the model is shown for one call.
type Request struct {
	Messages []Message
	Tools    []ToolSpec
}

// Response is the model's complete answer to one call.
type Response struct {
	Message Message

	// FinishReason is the provider's word for why generation stopped:
	// "stop", "tool_calls", "length" and so on. It is passed through
	// untranslated; "length" is how a caller learns an answer was cut off at
	// the output cap.
	FinishReason string

	Usage Usage
}

// ChunkKind says which part of the answer a Chunk carries.
type ChunkKind uint8

const (
	// ChunkText is a fragment of the answer.
	ChunkText ChunkKind = iota + 1
	// ChunkReasoning is a fragment of the model's reasoning, for models that
	// expose it.
	ChunkReasoning
	// ChunkToolStart announces a tool call: its position, id and name.
	ChunkToolStart
	// ChunkToolArgs is a fragment of a tool call's argument text.
	ChunkToolArgs
)

// Chunk is one fragment of a streamed answer.
type Chunk struct {
	Kind ChunkKind

	// Text is the fragment for ChunkText, ChunkReasoning and ChunkToolArgs.
	Text string

	// Index is set for the two tool kinds: the position of the call in
	// Response.Message.ToolCalls. Positions, not ids, tie a fragment to its
	// call because providers send the id once and the fragments by position.
	Index int

	// ID and Name are set for ChunkToolStart only.
	ID   string
	Name string
}
