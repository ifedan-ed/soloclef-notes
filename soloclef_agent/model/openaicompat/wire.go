package openaicompat

import (
	"encoding/json"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// The types in this file are the request body, field for field. They exist
// so the wire shape is written down once, as data, and not assembled from
// string keys at each call.

type wireMessage struct {
	Role string `json:"role"`

	// Content is a pointer so that it can be left out entirely. An assistant
	// message that only calls tools has no text, and "content": "" beside
	// tool_calls is refused by some providers while an absent content is
	// accepted by all of them.
	Content    *string        `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireToolSpec `json:"function"`
}

type wireToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

func encodeRequest(modelName string, params map[string]any, req model.Request) ([]byte, error) {
	messages := make([]wireMessage, len(req.Messages))
	for i := range req.Messages {
		m := &req.Messages[i]
		w := wireMessage{Role: string(m.Role), ToolCallID: m.ToolCallID}
		if m.Content != "" || len(m.ToolCalls) == 0 {
			w.Content = &m.Content
		}
		for _, c := range m.ToolCalls {
			w.ToolCalls = append(w.ToolCalls, wireToolCall{ID: c.ID, Type: "function", Function: wireFunction{Name: c.Name, Arguments: c.Args}})
		}
		messages[i] = w
	}

	body := make(map[string]any, len(params)+5)
	for k, v := range params {
		body[k] = v
	}
	body["model"] = modelName
	body["messages"] = messages
	body["stream"] = true
	// Without this the endpoint streams no token counts at all, and usage
	// would have to be estimated, which is to say invented.
	body["stream_options"] = map[string]bool{"include_usage": true}
	if len(req.Tools) > 0 {
		// An empty "tools" array is refused by some providers, so the field
		// is sent only when there is something in it.
		tools := make([]wireTool, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = wireTool{Type: "function", Function: wireToolSpec{Name: t.Name, Description: t.Description, Parameters: t.Parameters}}
		}
		body["tools"] = tools
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: encode request: %w", err)
	}
	return raw, nil
}
