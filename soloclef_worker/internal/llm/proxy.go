// Package llm is how a job asks the model proxy something: which model an
// account's work runs on, a client for that model, and a question whose
// answer must be a JSON object of a known shape.
//
// Every call goes through the proxy (LiteLLM) with the client the API
// tier's chat uses (soloclef_agent/model/openaicompat). No provider is ever
// called directly and no model runs here.
package llm

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/openaicompat"
)

// Proxy is the model proxy and how patient a job is with it.
type Proxy struct {
	// BaseURL is the proxy's OpenAI-compatible root, ending in /v1.
	BaseURL string
	// Key is the proxy's key for this application.
	Key string
	// HTTP makes the requests. Its deadlines come from each call's context
	// (CallTimeout), because an answer streams for as long as it is long.
	HTTP *http.Client
	// CallTimeout bounds one model call, the retries inside it included.
	CallTimeout time.Duration
	// Retry is how a call that failed for a reason that may pass (a 429, a
	// 5xx, a dropped connection) is tried again.
	Retry model.RetryPolicy
}

// Check reports what is missing. Called once at start-up.
func (p Proxy) Check() error {
	switch {
	case p.BaseURL == "":
		return errors.New("llm: the proxy's address is not set")
	case p.Key == "":
		return errors.New("llm: the proxy's key is not set")
	case p.HTTP == nil:
		return errors.New("llm: no HTTP client")
	case p.CallTimeout <= 0:
		return errors.New("llm: no call timeout")
	}
	return nil
}

// Client is a model.Model for one model name, retrying as Retry says.
//
// thinkingOff asks a model that reasons by default to answer at once. It is
// for the short calls (a notebook's name), where reasoning was measured at
// over half the output tokens and five seconds for a four-word answer, and
// it is sent only to a family known to accept the field: a provider that
// does not know it refuses the whole request.
func (p Proxy) Client(name string, thinkingOff bool) (model.Model, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("llm: no model name")
	}
	var params map[string]any
	if thinkingOff && takesThinkingSwitch(name) {
		params = map[string]any{"thinking": map[string]string{"type": "disabled"}}
	}
	client, err := openaicompat.New(openaicompat.Config{
		BaseURL: p.BaseURL, APIKey: p.Key, Model: name, HTTPClient: p.HTTP, Params: params,
	})
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}
	return model.WithRetry(client, p.Retry)
}

// thinkingFamilies are the model families that take `thinking: disabled`
// through the proxy (DeepSeek, measured 2026-09-23: 5.4s and ~1,200
// reasoning tokens became under a second and none).
var thinkingFamilies = []string{"deepseek"}

func takesThinkingSwitch(name string) bool {
	lowered := strings.ToLower(name)
	for _, family := range thinkingFamilies {
		if strings.Contains(lowered, family) {
			return true
		}
	}
	return false
}
