package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// transcriptFromRows turns a thread's saved messages into the conversation
// the model is shown, for a thread the loop holds no transcript of: one
// started before this engine, or copied from another thread.
//
// Only what was said is carried over: the text parts of each message. Tool
// calls and their results are not rebuilt, because the saved cards hold only
// what was safe to show (a length, not the output), and a conversation with
// tool calls whose results are missing is one a model provider refuses. The
// model can search again.
func transcriptFromRows(rows []HistoryMessage) []model.Message {
	out := make([]model.Message, 0, len(rows))
	for _, row := range rows {
		text := textOf(row.Content)
		if text == "" {
			continue
		}
		role := model.RoleUser
		if row.Assistant {
			role = model.RoleAssistant
		}
		// Two messages of one role in a row (an answer that failed and was
		// asked again) are merged: several providers refuse a conversation
		// that does not alternate.
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content += "\n\n" + text
			continue
		}
		out = append(out, model.Message{Role: role, Content: text})
	}
	return out
}

// withQuestion appends the new question to a transcript. If the transcript
// ends with a question that was never answered (the turn failed before any
// text), the two are joined into one message, by the same rule that merges
// rows above: the model still sees that it was asked, and the conversation
// still alternates. The joined message carries the new question's mark: it
// is where the new turn begins.
func withQuestion(transcript []model.Message, question model.Message) []model.Message {
	if n := len(transcript); n > 0 && transcript[n-1].Role == model.RoleUser {
		transcript[n-1].Content += "\n\n" + question.Content
		transcript[n-1].Mark = question.Mark
		return transcript
	}
	return append(transcript, question)
}

// turnMark is what the message that opens a turn is marked with in the
// loop's conversation: the turn it opens and the Tools menu pick it was
// asked with. The pick is here because it belongs to the question and is
// kept nowhere else once the next question has been asked: asking a
// question again asks it with the tool it was asked with.
type turnMark struct {
	Turn string `json:"turn"`
	Tool string `json:"tool,omitempty"`
}

func markOf(turnID, preferredTool string) string {
	// A struct of two strings always encodes.
	raw, _ := json.Marshal(turnMark{Turn: turnID, Tool: preferredTool})
	return string(raw)
}

// markedTurn finds the message that opens a turn. found is false for a turn
// the conversation does not hold: one from before this engine, or one a
// summary has replaced.
func markedTurn(messages []model.Message, turnID string) (index int, mark turnMark, found bool) {
	if turnID == "" {
		return 0, turnMark{}, false
	}
	for i := range messages {
		if messages[i].Mark == "" || messages[i].Mark == summaryMark {
			continue
		}
		var m turnMark
		if json.Unmarshal([]byte(messages[i].Mark), &m) == nil && m.Turn == turnID {
			return i, m, true
		}
	}
	return 0, turnMark{}, false
}

// textOf joins the text parts of a message's content. Content that is not a
// list of parts (a row written by something else) has no text.
func textOf(content []byte) string {
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	var texts []string
	for _, part := range parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n\n")
}

// savedTurns keeps a turn's choices (scope, the picked tool, the mentioned
// sources) while it waits for approval, so that the resumed turn is built
// with the tools the paused one had.
//
// The resume request cannot be trusted to repeat them and does not try: it
// carries decisions. Without this, a turn that paused inside the picked tool
// would be resumed by an agent that no longer has that tool.
//
// They are kept in the loop's own store, under a name beside the thread's,
// because that store is already where a paused turn lives: the two are
// saved by the same process and survive the same restart.
type savedTurns struct {
	store checkpoint.Store
}

func turnKey(threadID int64) string {
	// A dot, not a slash: slashes name nested agents' threads.
	return agentThread(threadID) + ".turn"
}

// keep replaces what is kept for the turn's thread. It is called at the
// start of every new turn, under the thread's lease, so it cannot race.
func (s savedTurns) keep(ctx context.Context, turn Turn) error {
	raw, err := json.Marshal(turn)
	if err != nil {
		return err
	}
	key := turnKey(turn.ThreadID)
	// Delete then save at sequence 1: the store's sequence check is for
	// state that is edited, and this is replaced whole.
	if err := s.store.Delete(ctx, key); err != nil {
		return err
	}
	return s.store.Save(ctx, checkpoint.Checkpoint{Thread: key, Seq: 1, Status: checkpoint.Done, Node: "turn", State: raw})
}

// load returns the turn kept for the thread. found is false when there is
// none.
func (s savedTurns) load(ctx context.Context, threadID int64) (turn Turn, found bool, err error) {
	cp, err := s.store.Load(ctx, turnKey(threadID))
	if errors.Is(err, checkpoint.ErrNotFound) {
		return Turn{}, false, nil
	}
	if err != nil {
		return Turn{}, false, err
	}
	if err := json.Unmarshal(cp.State, &turn); err != nil {
		return Turn{}, false, err
	}
	return turn, true, nil
}
