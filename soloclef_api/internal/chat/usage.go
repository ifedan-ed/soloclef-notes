package chat

import (
	"encoding/json"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
)

// tokens is one count of tokens, in the field names the apps and the
// token_usage table have always used.
type tokens struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type usageCall struct {
	Model string `json:"model"`
	tokens
}

// usageFrame is the "data-token-usage" frame's payload. Cost is absent on
// purpose: billing is not part of the product, and a zero would read as
// "this was free" rather than "nobody is counting".
type usageFrame struct {
	Usage map[string]tokens `json:"usage"`
	tokens
	CallDetails []usageCall `json:"call_details"`
	// Truncated says some model call hit its output cap, which the page
	// warns about.
	Truncated bool `json:"truncated"`
}

// meter adds up what a turn's model calls cost, the specialist's included:
// every model call in the tree ends with a ModelFinished event, whichever
// agent made it.
type meter struct {
	model string

	mu        sync.Mutex
	calls     []usageCall
	total     tokens
	truncated bool
}

func (m *meter) observe(ev event.Event) {
	if ev.Kind != event.ModelFinished {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	call := usageCall{Model: m.model, tokens: tokens{
		PromptTokens: int64(ev.Usage.PromptTokens), CompletionTokens: int64(ev.Usage.CompletionTokens), TotalTokens: int64(ev.Usage.TotalTokens),
	}}
	m.calls = append(m.calls, call)
	m.total.PromptTokens += call.PromptTokens
	m.total.CompletionTokens += call.CompletionTokens
	m.total.TotalTokens += call.TotalTokens
	if ev.FinishReason == "length" {
		m.truncated = true
	}
}

// frame is the turn's usage, or false when no model call finished.
func (m *meter) frame() (usageFrame, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return usageFrame{}, false
	}
	return usageFrame{
		Usage: map[string]tokens{m.model: m.total}, tokens: m.total,
		CallDetails: append([]usageCall(nil), m.calls...), Truncated: m.truncated,
	}, true
}

// row is the usage as the token_usage row stores it.
func (f usageFrame) row() (*Usage, error) {
	breakdown, err := json.Marshal(f.Usage)
	if err != nil {
		return nil, err
	}
	calls, err := json.Marshal(struct {
		Calls []usageCall `json:"calls"`
	}{f.CallDetails})
	if err != nil {
		return nil, err
	}
	return &Usage{
		PromptTokens: f.PromptTokens, CompletionTokens: f.CompletionTokens, TotalTokens: f.TotalTokens,
		Breakdown: breakdown, Calls: calls,
	}, nil
}
