package agent

import (
	"context"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// lengthFinish is the finish reason the chat-completions API gives for an
// answer stopped by the output cap.
const lengthFinish = "length"

// ContinueCutOff returns a model hook that finishes an answer the output cap
// cut short. When a call ends because of the cap, with no tool call in it,
// the hook calls the model again with the text so far and an instruction to
// go on, up to max more times, and returns one answer stitched from the
// pieces. A reader sees one answer streaming without a break, and what is
// stored is the whole of it.
//
// Only a plain answer is continued. A cut-off tool call has broken arguments
// that the loop already answers with an error the model can act on, and
// continuing a call's JSON half-way is not something models do reliably.
//
// If the answer is still cut off after max continuations, the response says
// so with its finish reason, "length", which reaches the caller in
// Result.FinishReason: the caller decides what to tell the reader.
func ContinueCutOff(max int) (ModelHook, error) {
	if max < 1 {
		return nil, fmt.Errorf("agent: ContinueCutOff needs at least one continuation, got %d", max)
	}
	return func(ctx context.Context, call ModelCall, next ModelFunc) (model.Response, error) {
		resp, err := next(ctx, call)
		if err != nil {
			return resp, err
		}
		text, usage := resp.Message.Content, resp.Usage
		for n := 0; n < max && resp.FinishReason == lengthFinish && len(resp.Message.ToolCalls) == 0; n++ {
			// The text so far goes in as the model's own message, so it
			// carries on from it rather than starting a new answer. A new
			// slice: the request's is the turn's conversation.
			follow := call
			msgs := make([]model.Message, 0, len(call.Request.Messages)+2)
			msgs = append(msgs, call.Request.Messages...)
			msgs = append(msgs, model.Message{Role: model.RoleAssistant, Content: text}, model.User(continueText))
			follow.Request.Messages = msgs
			if resp, err = next(ctx, follow); err != nil {
				// The reader has already seen the first part. Retrying would
				// write a second version under it, so the turn fails here
				// like any model call that fails after streaming.
				return model.Response{}, err
			}
			text += resp.Message.Content
			usage.PromptTokens += resp.Usage.PromptTokens
			usage.CompletionTokens += resp.Usage.CompletionTokens
			usage.TotalTokens += resp.Usage.TotalTokens
		}
		resp.Message.Content, resp.Usage = text, usage
		return resp, nil
	}, nil
}
