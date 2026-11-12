package openaicompat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// maxLine bounds one line of the stream. A line is one chunk of JSON, a few
// hundred bytes; the bound is far above that and exists so that an endpoint
// sending an endless line costs a clear error and not the process's memory.
const maxLine = 4 << 20

// streamChunk is one "data:" line, with only the fields this package reads.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`

			// ReasoningContent is the proxy's name for a model's reasoning
			// text. It is the one spelling read; a different endpoint that
			// calls it something else needs its own adapter, not a list of
			// names to try here.
			ReasoningContent string `json:"reasoning_content"`

			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *model.Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// pendingCall is a tool call being assembled from its fragments.
type pendingCall struct {
	// wireIndex is the index the endpoint uses for this call. It is kept
	// only to recognise later fragments; see position below.
	wireIndex int
	id, name  string
	args      strings.Builder
	announced bool
}

// readStream reads a server-sent-event body to its end, forwarding fragments
// to stream and returning the whole answer.
func readStream(body io.Reader, stream func(model.Chunk)) (model.Response, error) {
	var (
		text   strings.Builder
		calls  []*pendingCall
		finish string
		usage  model.Usage
		done   bool
	)
	// position finds the call a fragment belongs to and returns its place in
	// the answer. The endpoint's own index is not used as the place: most
	// endpoints count calls from 0, but some pass through a provider's
	// content-block index, which starts after the text block. Order of first
	// appearance is the one thing all of them agree on.
	position := func(wireIndex int) int {
		for i, c := range calls {
			if c.wireIndex == wireIndex {
				return i
			}
		}
		calls = append(calls, &pendingCall{wireIndex: wireIndex})
		return len(calls) - 1
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)
	for scanner.Scan() {
		line := scanner.Bytes()
		data, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			// Blank separators, comments (": keepalive") and other SSE
			// fields carry nothing for us.
			continue
		}
		data = bytes.TrimSpace(data)
		if bytes.Equal(data, []byte("[DONE]")) {
			done = true
			break
		}

		var chunk streamChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return model.Response{}, &model.Error{Message: fmt.Sprintf("the stream sent a line that is not JSON: %v", err)}
		}
		if chunk.Error != nil {
			// The endpoint answered 200 and then failed partway. Whether a
			// retry is safe depends on whether anything was streamed, which
			// the retry wrapper checks; from here it is a failure that might
			// not repeat.
			return model.Response{}, &model.Error{Retryable: true, Message: chunk.Error.Message}
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		for i := range chunk.Choices {
			choice := &chunk.Choices[i]
			if choice.FinishReason != "" {
				finish = choice.FinishReason
			}
			if s := choice.Delta.ReasoningContent; s != "" {
				stream(model.Chunk{Kind: model.ChunkReasoning, Text: s})
			}
			if s := choice.Delta.Content; s != "" {
				text.WriteString(s)
				stream(model.Chunk{Kind: model.ChunkText, Text: s})
			}
			for _, tc := range choice.Delta.ToolCalls {
				at := position(tc.Index)
				call := calls[at]
				if tc.ID != "" {
					call.id = tc.ID
				}
				call.name += tc.Function.Name
				call.args.WriteString(tc.Function.Arguments)
				if !call.announced && call.name != "" {
					// Announced once the name is known, with any argument
					// text that arrived before it, so a consumer never sees
					// arguments for a call it has not been told about.
					call.announced = true
					stream(model.Chunk{Kind: model.ChunkToolStart, Index: at, ID: call.id, Name: call.name})
					if early := call.args.String(); early != "" {
						stream(model.Chunk{Kind: model.ChunkToolArgs, Index: at, Text: early})
					}
				} else if call.announced && tc.Function.Arguments != "" {
					stream(model.Chunk{Kind: model.ChunkToolArgs, Index: at, Text: tc.Function.Arguments})
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return model.Response{}, &model.Error{Retryable: true, Message: "the stream broke: " + err.Error()}
	}
	if !done && finish == "" {
		// The connection closed cleanly but the model never said it was
		// finished. Returning what arrived as if it were the answer would
		// store half a message as a whole one.
		return model.Response{}, &model.Error{Retryable: true, Message: "the stream ended before the model finished"}
	}

	msg := model.Message{Role: model.RoleAssistant, Content: text.String()}
	for _, c := range calls {
		msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{ID: c.id, Name: c.name, Args: c.args.String()})
	}
	return model.Response{Message: msg, FinishReason: finish, Usage: usage}, nil
}
