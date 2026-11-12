// Package model is the vocabulary every model adapter speaks: messages, tool
// definitions, and one interface with one method.
//
// It is deliberately the OpenAI chat-completions shape and nothing wider. The
// project reaches every model through one proxy that speaks that shape, so a
// second vocabulary here would be a translation layer with nothing on the
// other side of it.
package model

import "encoding/json"

// Role says who wrote a message. The four values are the ones the
// chat-completions API accepts; there is no "developer" or "function" role
// because nothing in this project sends one.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one entry of a conversation, as it is stored in a checkpoint and
// as it is handed to a model adapter. The JSON tags are the checkpoint's
// spelling; the bytes sent to an endpoint are the adapter's business.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content,omitempty"`

	// ToolCalls is set on an assistant message that asks for tools. The API
	// requires every call listed here to be answered by exactly one tool
	// message before the next assistant message; the agent loop keeps that
	// invariant, which is why callers append to a conversation only through
	// it.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// ToolCallID is set on a tool message and names the call it answers.
	ToolCallID string `json:"tool_call_id,omitempty"`

	// Mark is the caller's own label for the message. It is stored with the
	// conversation and never sent to a model: no adapter reads it. Optional.
	//
	// It exists so that a caller can find its place in a stored conversation
	// again without counting. A chat marks the message that opens each turn
	// with that turn's id; "ask this question again" is then "cut the
	// conversation at the message with this mark" (Agent.Edit), which stays
	// true after older messages have been replaced by a summary. An index
	// kept beside the conversation would have to be corrected every time
	// the conversation was edited, and would be wrong the first time it was
	// not.
	Mark string `json:"mark,omitempty"`
}

// ToolCall is the model asking for one tool to be run.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// Args is the argument text exactly as the model wrote it. It is a string
	// and not json.RawMessage because a model can write broken JSON, and a
	// RawMessage holding broken JSON makes the whole conversation
	// unmarshalable: one bad call would lose the thread. The loop validates
	// it before any tool sees it.
	Args string `json:"args"`
}

// ToolSpec describes one tool to the model.
type ToolSpec struct {
	Name        string
	Description string

	// Parameters is the tool's JSON Schema, sent as written.
	Parameters json.RawMessage
}

// Usage is what one model call cost, as the provider counted it.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// User builds a user message. It exists because starting a turn is the one
// place callers construct a message by hand.
func User(content string) Message {
	return Message{Role: RoleUser, Content: content}
}
