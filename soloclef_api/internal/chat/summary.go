package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// Summarising long threads.
//
// A thread's stored conversation is sent to the model on every call and
// written to the database after every step, so both the cost of a question
// and the cost of saving it grow with the thread. When the conversation
// passes Above, everything before its most recent part is replaced by a
// summary the model writes, once, and the thread goes on from the summary
// and the recent messages.
//
// The choices here, and why:
//
//   - It happens at the start of a turn, under the thread's hold, through
//     agent.Edit. The loop is not running then, so nothing can be saved
//     over it, and the edit is refused rather than lost if something is.
//   - The cut is always at a message that opens a turn. A tool call and its
//     result are never separated, so what is left is a conversation every
//     provider accepts.
//   - It is rare on purpose. Editing the head of a conversation throws away
//     a provider's cached prefix for the next call, so trimming a little on
//     every turn would cost more than it saved. Above and Keep are far
//     enough apart that a thread is summarised once per many turns.
//   - A failure to summarise does not fail the turn. The conversation is
//     left whole and the question is answered at the old cost; the next
//     turn tries again. The failure is logged.
//   - The summary is a system message at the head of the stored
//     conversation, marked as one. A turn that has been summarised away can
//     no longer be asked again from the loop's transcript; regenerate.go
//     rebuilds from the saved messages in that case.
//   - The tokens it costs are counted with the turn's.

// SummaryLimits say when a thread is summarised and how much is kept. Both
// are counted in characters of stored conversation, the unit the state is
// saved in; a token is about four of them.
type SummaryLimits struct {
	// Above is the size past which a thread is summarised.
	Above int
	// Keep is how much of the most recent conversation is kept word for
	// word. It must be well below Above.
	Keep int
	// ResultChars is how much of one old tool result the summariser is
	// shown. A result is a search's passages or a page's text: the model
	// can fetch it again, and the summary needs what was concluded from it,
	// not the passages.
	ResultChars int
}

func (l SummaryLimits) check() error {
	if l.Keep < 1 || l.Above < 2*l.Keep || l.ResultChars < 1 {
		return fmt.Errorf("chat: the summary limits must be positive with Above at least twice Keep, got %+v", l)
	}
	return nil
}

// summaryMark labels the stored message that is a summary.
const summaryMark = "summary"

const summaryPrompt = `You are compacting the earlier part of a conversation between a user and an assistant so that the conversation can continue without it. Write the summary the assistant will read in place of those messages.

Keep, precisely:
- what the user is trying to do, and any instructions or preferences they gave;
- facts, names, numbers, dates and decisions that later messages may rely on;
- what was found in the user's documents or other sources and what was concluded from it, keeping any [n] citation labels exactly as written next to the claims they support;
- anything that was made (its title and kind);
- questions still open and what was about to be done.

Leave out pleasantries, repetition and the wording of tool calls. Write plain prose and short lists, in the language of the conversation. Do not address the user and do not add anything that was not said.

The conversation so far:
`

// size is how much conversation a message is, in characters.
func size(m model.Message) int {
	n := len(m.Content)
	for _, call := range m.ToolCalls {
		n += len(call.Name) + len(call.Args)
	}
	return n
}

// cutPoint returns the index of the message the kept part starts at: the
// earliest message that opens a turn and from which no more than keep
// characters follow. It returns 0 when there is no such message after the
// first, which is a conversation that is one long turn: nothing can be
// summarised without separating a call from its result.
func cutPoint(messages []model.Message, keep int) int {
	kept, cut := 0, 0
	for i := len(messages) - 1; i > 0; i-- {
		kept += size(messages[i])
		if messages[i].Role != model.RoleUser {
			continue
		}
		// The newest turn is kept whatever its size: a summary that
		// swallowed the question being answered would be no conversation.
		if kept <= keep || cut == 0 {
			cut = i
		}
		if kept > keep {
			break
		}
	}
	return cut
}

// render writes old messages as the text the summariser reads.
func render(messages []model.Message, resultChars int) string {
	var b strings.Builder
	for _, m := range messages {
		switch {
		case m.Mark == summaryMark:
			b.WriteString("[Summary of what came before]\n" + m.Content + "\n\n")
		case m.Role == model.RoleUser:
			b.WriteString("User: " + m.Content + "\n\n")
		case m.Role == model.RoleAssistant:
			if strings.TrimSpace(m.Content) != "" {
				b.WriteString("Assistant: " + m.Content + "\n\n")
			}
			for _, call := range m.ToolCalls {
				b.WriteString("Assistant used the tool " + call.Name + " with " + clipped(call.Args, 300) + "\n\n")
			}
		case m.Role == model.RoleTool:
			b.WriteString("Tool result: " + clipped(m.Content, resultChars) + "\n\n")
		}
	}
	return b.String()
}

// errUnchanged stops an edit that found nothing to do.
var errUnchanged = errors.New("chat: the conversation needs no summary")

// condense summarises the thread's older conversation if it has passed the
// limit. sink is told about the model call, so its tokens are counted with
// the turn's. It reports whether the thread was summarised.
func (s *Service) condense(ctx context.Context, built Built, thread string, sink event.Sink) (bool, error) {
	state, status, err := built.Agent.Load(ctx, thread)
	if errors.Is(err, checkpoint.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if status == checkpoint.Paused {
		// A paused turn's conversation is not to be touched; the route
		// refuses a new question on it anyway.
		return false, nil
	}
	total := 0
	for _, m := range state.Messages {
		total += size(m)
	}
	if total <= s.Summary.Above || len(state.Batch) != 0 {
		// A batch left open by a turn that was cut short is closed by the
		// loop when the next turn starts; until then the conversation is
		// not whole, and is not summarised.
		return false, nil
	}
	cut := cutPoint(state.Messages, s.Summary.Keep)
	if cut == 0 {
		return false, nil
	}
	old := state.Messages[:cut]
	response, err := built.Chat.Generate(ctx, model.Request{
		Messages: []model.Message{model.User(summaryPrompt + render(old, s.Summary.ResultChars))},
	}, func(model.Chunk) {})
	if err != nil {
		return false, fmt.Errorf("chat: summarising the thread: %w", err)
	}
	sink(event.Event{Kind: event.ModelFinished, Usage: response.Usage, FinishReason: response.FinishReason})
	summary := strings.TrimSpace(response.Message.Content)
	if summary == "" {
		return false, errors.New("chat: the model wrote an empty summary")
	}
	head := model.Message{
		Role: model.RoleSystem, Mark: summaryMark,
		Content: "<conversation_summary>\nThe earlier part of this conversation was replaced by this summary to keep the conversation short. Treat it as what was said.\n" + summary + "\n</conversation_summary>",
	}
	err = built.Agent.Edit(ctx, thread, func(st *agent.State) error {
		// The state was read once to decide and is read again here to
		// change. Under the thread's hold the two are the same; if they
		// are not, something else wrote the thread, and its conversation
		// is not one this summary describes.
		if len(st.Messages) != len(state.Messages) || len(st.Batch) != 0 {
			return errUnchanged
		}
		st.Messages = append([]model.Message{head}, st.Messages[cut:]...)
		return nil
	})
	if errors.Is(err, errUnchanged) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
