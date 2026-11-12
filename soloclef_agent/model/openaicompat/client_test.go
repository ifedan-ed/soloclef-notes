package openaicompat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/openaicompat"
)

// endpoint starts a server that answers every request with handler and
// returns a client pointed at it.
func endpoint(t *testing.T, handler http.HandlerFunc) *openaicompat.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := openaicompat.New(openaicompat.Config{
		BaseURL: srv.URL + "/v1/", APIKey: "test-key", Model: "test-model",
		HTTPClient: srv.Client(), Params: map[string]any{"temperature": 0.2},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// sse writes lines as server-sent events, each followed by a blank line.
func sse(w http.ResponseWriter, lines ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, line := range lines {
		fmt.Fprintf(w, "%s\n\n", line)
	}
}

func chunks(c *[]string) func(model.Chunk) {
	return func(ch model.Chunk) {
		switch ch.Kind {
		case model.ChunkText:
			*c = append(*c, "text:"+ch.Text)
		case model.ChunkReasoning:
			*c = append(*c, "reasoning:"+ch.Text)
		case model.ChunkToolStart:
			*c = append(*c, fmt.Sprintf("start[%d]:%s:%s", ch.Index, ch.ID, ch.Name))
		case model.ChunkToolArgs:
			*c = append(*c, fmt.Sprintf("args[%d]:%s", ch.Index, ch.Text))
		}
	}
}

func TestARequestIsSentInTheChatCompletionsShape(t *testing.T) {
	var got map[string]any
	var path, auth string
	c := endpoint(t, func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		sse(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`, `data: [DONE]`)
	})

	_, err := c.Generate(context.Background(), model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "Be brief."},
			{Role: model.RoleUser, Content: "hi"},
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c1", Name: "search", Args: `{"q":"x"}`}}},
			{Role: model.RoleTool, ToolCallID: "c1", Content: ""},
		},
		Tools: []model.ToolSpec{{Name: "search", Description: "Searches.", Parameters: []byte(`{"type":"object"}`)}},
	}, func(model.Chunk) {})
	if err != nil {
		t.Fatal(err)
	}

	if path != "/v1/chat/completions" || auth != "Bearer test-key" {
		t.Fatalf("path %q, authorization %q", path, auth)
	}
	want := `{
		"model": "test-model", "stream": true, "stream_options": {"include_usage": true}, "temperature": 0.2,
		"messages": [
			{"role": "system", "content": "Be brief."},
			{"role": "user", "content": "hi"},
			{"role": "assistant", "tool_calls": [{"id": "c1", "type": "function", "function": {"name": "search", "arguments": "{\"q\":\"x\"}"}}]},
			{"role": "tool", "tool_call_id": "c1", "content": ""}
		],
		"tools": [{"type": "function", "function": {"name": "search", "description": "Searches.", "parameters": {"type": "object"}}}]
	}`
	var wantBody map[string]any
	if err := json.Unmarshal([]byte(want), &wantBody); err != nil {
		t.Fatal(err)
	}
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(wantBody)
	if string(g) != string(w) {
		t.Fatalf("request body\n got: %s\nwant: %s", g, w)
	}
}

func TestAStreamIsForwardedAndAssembled(t *testing.T) {
	c := endpoint(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`: keepalive`,
			`data: {"choices":[{"delta":{"role":"assistant","reasoning_content":"Thinking."}}]}`,
			`data: {"choices":[{"delta":{"content":"Let me "}}]}`,
			`data: {"choices":[{"delta":{"content":"check."}}]}`,
			// Two calls whose fragments interleave, indexed from 1 as an
			// endpoint that passes content-block indexes through does.
			`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_a","function":{"name":"search","arguments":""}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":2,"id":"call_b","function":{"name":"read","arguments":"{\"id\""}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"q\":\"go\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":2,"function":{"arguments":":7}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":16}}`,
			`data: [DONE]`,
		)
	})

	var seen []string
	resp, err := c.Generate(context.Background(), model.Request{Messages: []model.Message{model.User("hi")}}, chunks(&seen))
	if err != nil {
		t.Fatal(err)
	}
	wantSeen := []string{
		"reasoning:Thinking.", "text:Let me ", "text:check.",
		"start[0]:call_a:search", "start[1]:call_b:read", `args[1]:{"id"`,
		`args[0]:{"q":"go"}`, "args[1]::7}",
	}
	if strings.Join(seen, " | ") != strings.Join(wantSeen, " | ") {
		t.Fatalf("chunks\n got: %s\nwant: %s", strings.Join(seen, " | "), strings.Join(wantSeen, " | "))
	}
	want := model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: "Let me check.", ToolCalls: []model.ToolCall{
			{ID: "call_a", Name: "search", Args: `{"q":"go"}`},
			{ID: "call_b", Name: "read", Args: `{"id":7}`},
		}},
		FinishReason: "tool_calls",
		Usage:        model.Usage{PromptTokens: 11, CompletionTokens: 5, TotalTokens: 16},
	}
	g, _ := json.Marshal(resp)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("response\n got: %s\nwant: %s", g, w)
	}
}

func TestARefusalIsNotRetryableAndAnOverloadIs(t *testing.T) {
	status, header, body := 0, "", ""
	c := endpoint(t, func(w http.ResponseWriter, r *http.Request) {
		if header != "" {
			w.Header().Set("Retry-After", header)
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
	cases := []struct {
		status     int
		header     string
		body       string
		retryable  bool
		retryAfter time.Duration
		message    string
	}{
		{400, "", `{"error":{"message":"context length exceeded"}}`, false, 0, "context length exceeded"},
		{401, "", `{"error":{"message":"bad key"}}`, false, 0, "bad key"},
		{429, "7", `{"error":{"message":"slow down"}}`, true, 7 * time.Second, "slow down"},
		{503, "", `upstream unavailable`, true, 0, "upstream unavailable"},
		{408, "soon", ``, true, 0, ""},
	}
	for _, tc := range cases {
		status, header, body = tc.status, tc.header, tc.body
		_, err := c.Generate(context.Background(), model.Request{}, func(model.Chunk) {})
		var got *model.Error
		if !errors.As(err, &got) {
			t.Fatalf("status %d: err = %v, want *model.Error", tc.status, err)
		}
		if got.Status != tc.status || got.Retryable != tc.retryable || got.RetryAfter != tc.retryAfter || got.Message != tc.message {
			t.Errorf("status %d: got %+v", tc.status, got)
		}
	}
}

func TestAStreamThatFailsPartwayIsAnErrorNotAHalfAnswer(t *testing.T) {
	cases := map[string][]string{
		"upstream overloaded":                        {`data: {"choices":[{"delta":{"content":"Half"}}]}`, `data: {"error":{"message":"upstream overloaded"}}`},
		"the stream ended before the model finished": {`data: {"choices":[{"delta":{"content":"Half"}}]}`},
		"not JSON": {`data: {"choices":[{"delta":`},
	}
	for want, lines := range cases {
		c := endpoint(t, func(w http.ResponseWriter, r *http.Request) { sse(w, lines...) })
		resp, err := c.Generate(context.Background(), model.Request{}, func(model.Chunk) {})
		var got *model.Error
		if !errors.As(err, &got) || !strings.Contains(got.Message, want) {
			t.Errorf("err = %v, want a model error containing %q", err, want)
		}
		if resp.Message.Content != "" {
			t.Errorf("a failed stream returned text %q", resp.Message.Content)
		}
	}

	// An endpoint that closes without [DONE] but after a finish reason has
	// finished: the marker is a courtesy, the finish reason is the fact.
	c := endpoint(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, `data: {"choices":[{"delta":{"content":"Whole."},"finish_reason":"stop"}]}`)
	})
	resp, err := c.Generate(context.Background(), model.Request{}, func(model.Chunk) {})
	if err != nil || resp.Message.Content != "Whole." {
		t.Fatalf("resp = %+v, err = %v", resp, err)
	}
}

func TestCancellingDuringAStreamReturnsTheContextError(t *testing.T) {
	started := make(chan struct{})
	c := endpoint(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, `data: {"choices":[{"delta":{"content":"Half"}}]}`)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	_, err := c.Generate(ctx, model.Request{}, func(model.Chunk) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestAnEndpointThatCannotBeReachedIsARetryableModelError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	c, err := openaicompat.New(openaicompat.Config{BaseURL: srv.URL, APIKey: "k", Model: "m", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	srv.Close()
	_, err = c.Generate(context.Background(), model.Request{}, func(model.Chunk) {})
	var got *model.Error
	if !errors.As(err, &got) || got.Status != 0 || !got.Retryable {
		t.Fatalf("err = %v, want a retryable model error with no status", err)
	}
}

func TestNewRefusesAnIncompleteConfig(t *testing.T) {
	ok := openaicompat.Config{BaseURL: "http://proxy.example:4000/v1", APIKey: "k", Model: "m", HTTPClient: http.DefaultClient}
	cases := map[string]func(*openaicompat.Config){
		"BaseURL is not set":          func(c *openaicompat.Config) { c.BaseURL = "" },
		"is not an http or https":     func(c *openaicompat.Config) { c.BaseURL = "proxy.example:4000" },
		"APIKey is not set":           func(c *openaicompat.Config) { c.APIKey = "" },
		"Model is not set":            func(c *openaicompat.Config) { c.Model = "" },
		"HTTPClient is not set":       func(c *openaicompat.Config) { c.HTTPClient = nil },
		`Params may not set "stream"`: func(c *openaicompat.Config) { c.Params = map[string]any{"stream": false} },
		`Params may not set "model"`:  func(c *openaicompat.Config) { c.Params = map[string]any{"model": "other"} },
		// The key is APIKey's to set, in any spelling of the name.
		`Headers may not set "authorization"`: func(c *openaicompat.Config) { c.Headers = map[string]string{"authorization": "Bearer other"} },
		`Headers may not set "Content-Type"`:  func(c *openaicompat.Config) { c.Headers = map[string]string{"Content-Type": "text/plain"} },
		"is not a header name":                func(c *openaicompat.Config) { c.Headers = map[string]string{"X Bad": "v"} },
		"has a line break":                    func(c *openaicompat.Config) { c.Headers = map[string]string{"X-Org": "a\r\nX-Injected: b"} },
	}
	for want, breakIt := range cases {
		cfg := ok
		breakIt(&cfg)
		if _, err := openaicompat.New(cfg); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("New error = %v, want one containing %q", err, want)
		}
	}
	if _, err := openaicompat.New(ok); err != nil {
		t.Fatalf("a complete config was refused: %v", err)
	}
}

// A connection's own headers are sent beside the key on the request, and
// the key is still the one APIKey names.
func TestTheCallersHeadersAreSentWithTheKey(t *testing.T) {
	var org, auth, accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org, auth, accept = r.Header.Get("X-Org"), r.Header.Get("Authorization"), r.Header.Get("Accept")
		sse(w, `data: {"choices":[{"delta":{"content":"hi"}}]}`, "data: [DONE]")
	}))
	t.Cleanup(srv.Close)
	c, err := openaicompat.New(openaicompat.Config{
		BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "m", HTTPClient: srv.Client(),
		Headers: map[string]string{"X-Org": "org-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Generate(context.Background(), model.Request{Messages: []model.Message{model.User("hello")}}, func(model.Chunk) {}); err != nil {
		t.Fatal(err)
	}
	if org != "org-1" || auth != "Bearer test-key" || accept != "text/event-stream" {
		t.Fatalf("headers = X-Org %q, Authorization %q, Accept %q", org, auth, accept)
	}
}
