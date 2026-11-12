package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/cite"
)

var objectSchema = json.RawMessage(`{"type":"object"}`)

// library is a search tool with one passage, which it numbers in the
// thread's registry as the real search does.
func library() tool.Tool {
	return tool.Tool{Name: "search_knowledge_base", Parameters: objectSchema, Approval: tool.Auto, Parallel: true,
		Run: func(_ context.Context, call tool.Call) (string, error) {
			n := call.Citations.Number(citation.Source{Kind: cite.Chunk, Ref: "481"})
			if n != 1 {
				return "", errors.New("the passage was renumbered")
			}
			return "[1] Mitosis has four phases: prophase, metaphase, anaphase and telophase.", nil
		}}
}

func call(id, name, args string) []model.ToolCall {
	return []model.ToolCall{{ID: id, Name: name, Args: args}}
}

// A whole turn, as the page reads it: the model thinks, searches the user's
// documents, and answers with a citation.
func TestATurnWithAToolCallAndACitationAsTheWebAppReadsIt(t *testing.T) {
	script := modeltest.NewScript(
		modeltest.Reply{Reasoning: "I should look.", Calls: call("c1", "search_knowledge_base", `{"query":"mitosis"}`),
			Usage: model.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}},
		modeltest.Reply{Text: "Mitosis has four phases [1]. Not `x[1]`.",
			Usage: model.Usage{PromptTokens: 150, CompletionTokens: 20, TotalTokens: 170}},
	)
	a := newApp(t, script, []tool.Tool{library()})
	response := a.request(ada, http.MethodPost, TurnPath, turnBody)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status %d", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q", got)
	}
	if response.Header.Get("Cache-Control") != "no-cache" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Errorf("stream headers = %v", response.Header)
	}
	frames, done := readFrames(t, response.Body)
	if !done {
		t.Fatal("the stream did not end with [DONE]")
	}

	// The order the page depends on: ids before anything the model says,
	// status idle before the stream closes.
	want := "start start-step data-turn-info data-turn-status data-user-message-id data-assistant-message-id data-activity-timing " +
		"reasoning-start reasoning-delta reasoning-delta reasoning-delta reasoning-end " +
		"tool-input-start tool-input-delta tool-input-delta tool-input-available data-activity tool-output-available data-activity " +
		"text-start text-delta text-delta text-delta text-delta text-delta text-delta text-delta text-end " +
		"data-activity-timing data-thread-title-update data-token-usage data-turn-status finish-step finish"
	if got := kinds(frames); got != want {
		t.Fatalf("frames\n got: %s\nwant: %s", got, want)
	}

	// Every frame is one the page's reader accepts, and it builds from them
	// the parts that were saved.
	page := readAll(t, frames)
	if page.failure != nil {
		t.Fatalf("the page was sent an error: %v", page.failure)
	}
	if strings.Join(page.statuses, " ") != "busy idle" {
		t.Errorf("statuses = %v", page.statuses)
	}
	if page.title != "Cell division" || frames[len(frames)-5].data()["threadId"] != 1.0 {
		t.Errorf("title frame = %v", frames[len(frames)-5])
	}
	// While it streams, the page shows the bare label.
	if text := page.parts[2]["text"]; text != "Mitosis has four phases [1]. Not `x[1]`." {
		t.Errorf("streamed text = %q", text)
	}
	// The tool's output is not sent: only that it finished, and its length.
	output := find(frames, "tool-output-available")[0]["output"].(map[string]any)
	if len(output) != 2 || output["status"] != "completed" || output["result_length"] != float64(len("[1] Mitosis has four phases: prophase, metaphase, anaphase and telophase.")) {
		t.Errorf("tool card = %v", output)
	}
	if strings.Contains(kinds(frames), "prophase") {
		t.Error("the tool's output reached the page")
	}

	// The rows.
	if len(a.rows.messages) != 2 {
		t.Fatalf("rows = %+v", a.rows.messages)
	}
	user, assistant := a.rows.messages[0], a.rows.messages[1]
	if user.Content != `[{"type":"text","text":"What are the phases of mitosis?"}]` || user.Author != ada || user.Assistant {
		t.Errorf("user row = %+v", user)
	}
	if user.TurnID != assistant.TurnID || user.TurnID != page.turnID || !strings.HasPrefix(user.TurnID, "1:") {
		t.Errorf("turn ids: user %q, assistant %q, stream %q", user.TurnID, assistant.TurnID, page.turnID)
	}
	if page.userMessageID != float64(user.ID) || page.assistantMessageID != float64(assistant.ID) {
		t.Errorf("message ids in the stream: %v, %v", page.userMessageID, page.assistantMessageID)
	}
	saved := contentOf(t, assistant)
	// Saved text has the label turned into its marker; the one in code is
	// left alone.
	sameParts(t, page.parts, saved, map[int]string{2: "Mitosis has four phases [citation:481]. Not `x[1]`."})
	if saved[0]["status"] != "completed" || saved[0]["completedAt"] == nil {
		t.Errorf("saved reasoning = %v", saved[0])
	}

	// Usage: both model calls, in the frame and in the row.
	if page.tokenUsage["total_tokens"] != 280.0 || page.tokenUsage["truncated"] != false || len(page.tokenUsage["call_details"].([]any)) != 2 {
		t.Errorf("usage frame = %v", page.tokenUsage)
	}
	if _, priced := page.tokenUsage["cost_micros"]; priced {
		t.Error("the usage frame carries a cost; billing is not part of the product")
	}
	if usage := a.rows.usage[assistant.ID]; usage.PromptTokens != 250 || usage.CompletionTokens != 30 || usage.TotalTokens != 280 ||
		string(usage.Breakdown) != `{"test-model":{"prompt_tokens":250,"completion_tokens":30,"total_tokens":280}}` {
		t.Errorf("usage row = %+v %s", usage, usage.Breakdown)
	}

	// The thread: moved to the top, named, and nobody is being answered.
	if a.rows.touched[1] != 1 || a.rows.titles[1] != "Cell division" || len(a.rows.responding) != 0 {
		t.Errorf("thread state: touched %v, titles %v, responding %v", a.rows.touched, a.rows.titles, a.rows.responding)
	}
	if busy := a.call(ada, http.MethodGet, "/api/v1/threads/1/turn-status", ""); busy.body["status"] != "idle" {
		t.Errorf("status after the turn = %s", busy.raw)
	}
}

// The second message of a thread continues the first: the model is shown the
// whole conversation, tool results included, and the thread is not renamed.
func TestASecondTurnContinuesTheConversation(t *testing.T) {
	script := modeltest.NewScript(
		modeltest.Reply{Calls: call("c1", "search_knowledge_base", `{"query":"mitosis"}`)},
		modeltest.Reply{Text: "Four phases [1]."},
		modeltest.Reply{Text: "Prophase is first [1]."},
	)
	a := newApp(t, script, []tool.Tool{library()})
	a.ask(ada, turnBody)
	frames, _ := a.ask(ada, `{"chat_id":1,"user_query":"Which is first?"}`)
	if len(find(frames, "data-thread-title-update")) != 0 {
		t.Error("the thread was named again on its second answer")
	}
	requests := script.Requests()
	last := requests[2].Messages
	var roles []string
	for _, m := range last {
		roles = append(roles, string(m.Role))
	}
	if got := strings.Join(roles, " "); got != "system user assistant tool assistant user" {
		t.Fatalf("the model was shown: %s", got)
	}
	if !strings.Contains(last[3].Content, "prophase, metaphase") || last[5].Content != "Which is first?" {
		t.Errorf("transcript = %+v", last)
	}
	// The label still means the same source in the second answer: one
	// numbering for the thread.
	rows := a.rows.assistant(1)
	if text := contentOf(t, rows[1])[0]["text"]; text != "Prophase is first [citation:481]." {
		t.Errorf("second answer saved as %q", text)
	}
}

// A thread the loop has no transcript of (it predates this engine) starts
// from its saved messages.
func TestAThreadFromBeforeStartsFromItsSavedMessages(t *testing.T) {
	script := modeltest.NewScript(modeltest.Reply{Text: "As I said, four."})
	a := newApp(t, script, nil)
	a.rows.messages = []storedMessage{
		{ID: 1, ThreadID: 1, Content: `[{"type":"text","text":"How many phases?"},{"type":"mentioned-documents","documents":[]}]`},
		{ID: 2, ThreadID: 1, Assistant: true, Content: `[{"type":"reasoning","text":"hidden"},{"type":"text","text":"Four."},{"type":"tool-call","toolCallId":"x","toolName":"t","args":{}}]`},
		{ID: 3, ThreadID: 1, Assistant: true, Content: `[{"type":"status","code":"no_response","text":"No response was produced."}]`},
		{ID: 4, ThreadID: 9, Content: `[{"type":"text","text":"Another thread's."}]`},
	}
	frames, _ := a.ask(ada, `{"chat_id":1,"user_query":"Sure?","messages":[{"role":"user","content":"a history the client made up"}]}`)
	if len(find(frames, "data-thread-title-update")) != 0 {
		t.Error("a thread that already had answers was named")
	}
	sent := script.Requests()[0].Messages
	if len(sent) != 4 || sent[1].Content != "How many phases?" || sent[2].Content != "Four." || sent[3].Content != "Sure?" {
		t.Fatalf("the model was shown %+v", sent)
	}
	for _, m := range sent {
		if strings.Contains(m.Content, "made up") || strings.Contains(m.Content, "hidden") || strings.Contains(m.Content, "Another thread") {
			t.Errorf("the model was shown %q", m.Content)
		}
	}
}

// The step limit is counted per turn. Three turns of two steps each pass a
// limit of three; a single turn that needs four does not, and says so, and
// the thread still answers afterwards.
func TestTheStepLimitIsPerTurnNotPerThread(t *testing.T) {
	loop := tool.Tool{Name: "again", Parameters: objectSchema, Approval: tool.Auto,
		Run: func(context.Context, tool.Call) (string, error) { return "again", nil }}
	script := modeltest.NewScript(
		modeltest.Reply{Calls: call("a1", "again", `{}`)}, modeltest.Reply{Text: "One."},
		modeltest.Reply{Calls: call("a2", "again", `{}`)}, modeltest.Reply{Text: "Two."},
		modeltest.Reply{Calls: call("a3", "again", `{}`)}, modeltest.Reply{Text: "Three."},
		// The fourth turn never stops calling the tool.
		modeltest.Reply{Text: "Working.", Calls: call("b1", "again", `{}`)}, modeltest.Reply{Calls: call("b2", "again", `{}`)},
		modeltest.Reply{Text: "Fine again."},
	)
	a := newApp(t, script, []tool.Tool{loop}, func(_ *Service, s *scripted) { s.maxSteps = 3 })
	for i, want := range []string{"One.", "Two.", "Three."} {
		page := readAll(t, first(a.ask(ada, turnBody)))
		if page.failure != nil || page.parts[len(page.parts)-1]["text"] != want {
			t.Fatalf("turn %d: failure %v, parts %v", i+1, page.failure, page.parts)
		}
	}
	frames, done := a.ask(ada, turnBody)
	if !done {
		t.Fatal("a failed stream did not end with [DONE]")
	}
	page := readAll(t, frames)
	if page.failure == nil || page.failure["errorCode"] != "TURN_STEP_LIMIT" || !strings.Contains(page.failure["message"].(string), "more steps") {
		t.Fatalf("failure = %v", page.failure)
	}
	if last := frames[len(frames)-1]; last.kind() != "error" || frames[len(frames)-2].data()["status"] != "idle" {
		t.Errorf("the stream ended %s", kinds(frames[len(frames)-3:]))
	}
	// What the turn had done is saved, and nobody is "being answered".
	rows := a.rows.assistant(1)
	if text := contentOf(t, rows[3])[0]["text"]; text != "Working." || len(a.rows.responding) != 0 {
		t.Errorf("saved %v, responding %v", contentOf(t, rows[3]), a.rows.responding)
	}
	if page := readAll(t, first(a.ask(ada, turnBody))); page.failure != nil || page.parts[0]["text"] != "Fine again." {
		t.Fatalf("the thread did not answer after a turn hit the limit: %v %v", page.failure, page.parts)
	}
}

func first(frames []frame, _ bool) []frame { return frames }

// notes is a tool that writes, and so asks first.
func notes(policy tool.Approval, ran *atomic.Int64) tool.Tool {
	return tool.Tool{Name: "write_note", Parameters: objectSchema, Approval: policy,
		Run: func(_ context.Context, call tool.Call) (string, error) {
			ran.Add(1)
			return "written " + string(call.Args), nil
		}}
}

const resumePath = "/api/v1/threads/1/resume"

// A tool that asks first pauses the turn; the decision resumes it.
func TestAToolThatAsksFirstPausesTheTurnAndADecisionResumesIt(t *testing.T) {
	var ran atomic.Int64
	script := modeltest.NewScript(
		modeltest.Reply{Text: "I will note it.", Calls: call("w1", "write_note", `{"text":"hi"}`)},
		modeltest.Reply{Text: "Noted."},
	)
	a := newApp(t, script, []tool.Tool{notes(tool.Ask, &ran)})
	frames, done := a.ask(ada, turnBody)
	if !done || ran.Load() != 0 {
		t.Fatalf("done=%v ran=%d: the tool ran before it was approved", done, ran.Load())
	}
	page := readAll(t, frames)
	if len(page.interrupts) != 1 {
		t.Fatalf("interrupts = %v\n%s", page.interrupts, kinds(frames))
	}
	asked := page.interrupts[0]
	wantAsked := `{"action_requests":[{"args":{"text":"hi"},"name":"write_note"}],"interrupt_type":"permission_ask","review_configs":[{"action_name":"write_note","allowed_decisions":["approve","edit","reject","approve_always"]}],"tool_call_id":"w1"}`
	if got, _ := json.Marshal(asked); string(got) != wantAsked {
		t.Fatalf("interrupt\n got %s\nwant %s", got, wantAsked)
	}
	// A paused stream closes as a finished one: the page shows the card.
	// It is the thread's first turn, so it is named now: the resumed turn
	// will not be "first".
	if tail := kinds(frames[len(frames)-8:]); tail != "data-interrupt-request data-activity data-activity-timing data-thread-title-update data-token-usage data-turn-status finish-step finish" || page.failure != nil {
		t.Errorf("a paused stream ended: %s", tail)
	}
	// The waiting call is saved as waiting, not as aborted.
	paused := a.rows.assistant(1)[0]
	if card := contentOf(t, paused)[1]; card["state"] != nil || card["result"] != nil || card["toolCallId"] != "w1" {
		t.Errorf("the waiting call was saved as %v", card)
	}

	// A reloaded page asks what is waiting and is told the same thing.
	pending := a.call(ada, http.MethodGet, "/api/v1/threads/1/pending-interrupts", "")
	list, _ := pending.body["pending_interrupts"].([]any)
	if pending.status != 200 || len(list) != 1 || pending.body["assistant_message_id"] != float64(paused.ID) {
		t.Fatalf("pending = %s", pending.raw)
	}
	if got, _ := json.Marshal(list[0]); string(got) != wantAsked {
		t.Errorf("pending differs from the stream's request:\n%s", got)
	}

	// A new question is refused while the decision is owed, and the thread
	// is not left busy by the refusal.
	refused := a.call(ada, http.MethodPost, TurnPath, turnBody)
	if refused.status != http.StatusConflict || refused.code() != "THREAD_AWAITING_APPROVAL" {
		t.Fatalf("a new question while paused: %d %s", refused.status, refused.raw)
	}
	if len(a.rows.messages) != 2 {
		t.Errorf("the refused question was saved: %d rows", len(a.rows.messages))
	}

	response := a.request(ada, http.MethodPost, resumePath, `{"decisions":[{"type":"approve","tool_call_id":"w1"}]}`)
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("resume status %d", response.StatusCode)
	}
	resumed, done := readFrames(t, response.Body)
	// No user message id: a resumed turn has no new question. Its answer
	// is a new row, announced like any other.
	want := "start start-step data-turn-info data-turn-status data-assistant-message-id data-activity-timing tool-output-available data-activity text-start text-delta text-end data-activity-timing data-token-usage data-turn-status finish-step finish"
	if got := kinds(resumed); got != want || !done {
		t.Fatalf("resumed stream\n got: %s\nwant: %s", got, want)
	}
	if ran.Load() != 1 {
		t.Fatalf("the approved tool ran %d times", ran.Load())
	}
	if find(resumed, "tool-output-available")[0]["toolCallId"] != "w1" {
		t.Error("the result landed on another card")
	}
	rows := a.rows.assistant(1)
	if len(rows) != 2 || rows[1].TurnID == rows[0].TurnID || contentOf(t, rows[1])[0]["text"] != "Noted." {
		t.Errorf("rows after the resume = %+v", rows)
	}
	// The page reads the resumed stream on top of the paused message, so
	// the result lands on the card it already shows. The saved rows say the
	// same after a reload: the result is on the paused message's card, and
	// the resumed message holds what came after.
	live := readResumed(t, page.parts, resumed)
	sameParts(t, live.parts, append(contentOf(t, rows[0]), contentOf(t, rows[1])...), nil)
	if card := contentOf(t, rows[0])[1]; card["result"] == nil || card["state"] != nil {
		t.Errorf("the approved call's saved card = %v", card)
	}
	after := a.call(ada, http.MethodGet, "/api/v1/threads/1/pending-interrupts", "")
	if list, _ := after.body["pending_interrupts"].([]any); len(list) != 0 || after.body["assistant_message_id"] != nil {
		t.Errorf("pending after the resume = %s", after.raw)
	}
}

// A rejection is told to the model with the person's reason, and the tool
// does not run. An edit runs the tool with the edited arguments.
func TestARejectionAndAnEdit(t *testing.T) {
	var ran atomic.Int64
	script := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "w1", Name: "write_note", Args: `{"text":"one"}`}, {ID: "w2", Name: "write_note", Args: `{"text":"two"}`}}},
		modeltest.Reply{Text: "One was not written."},
	)
	a := newApp(t, script, []tool.Tool{notes(tool.Ask, &ran)})
	page := readAll(t, first(a.ask(ada, turnBody)))
	if len(page.interrupts) != 2 || page.interrupts[0]["tool_call_id"] != "w1" || page.interrupts[1]["tool_call_id"] != "w2" {
		t.Fatalf("interrupts = %v", page.interrupts)
	}
	// Sent in the other order than asked: decisions are matched by id.
	body := `{"decisions":[
		{"type":"edit","tool_call_id":"w2","edited_action":{"name":"write_note","args":{"text":"TWO"}}},
		{"type":"reject","tool_call_id":"w1","message":"not that one"}]}`
	response := a.request(ada, http.MethodPost, resumePath, body)
	defer response.Body.Close()
	readFrames(t, response.Body)
	if ran.Load() != 1 {
		t.Fatalf("ran %d times; one call was rejected", ran.Load())
	}
	shown := script.Requests()[1].Messages
	results := shown[len(shown)-2:]
	if !strings.Contains(results[0].Content, "not that one") || results[1].Content != `written {"text":"TWO"}` {
		t.Errorf("the model was told %q and %q", results[0].Content, results[1].Content)
	}
}

// "Always allow" is remembered as a row, read again at every call: the same
// tool then runs unasked, and stops doing so the moment the row is removed.
func TestAlwaysAllowIsReadFreshOnEveryCall(t *testing.T) {
	var ran atomic.Int64
	script := modeltest.NewScript(
		modeltest.Reply{Calls: call("w1", "write_note", `{"n":1}`)}, modeltest.Reply{Text: "First."},
		modeltest.Reply{Calls: call("w2", "write_note", `{"n":2}`)}, modeltest.Reply{Text: "Second."},
		modeltest.Reply{Calls: call("w3", "write_note", `{"n":3}`)},
	)
	a := newApp(t, script, []tool.Tool{notes(tool.Ask, &ran)})
	a.ask(ada, turnBody)
	response := a.request(ada, http.MethodPost, resumePath, `{"decisions":[{"type":"approve_always","tool_call_id":"w1"}]}`)
	readFrames(t, response.Body)
	response.Body.Close()
	if !a.rows.trust[trustKey(ada, "write_note")] {
		t.Fatal("always-allow was not remembered")
	}

	// A later turn: the tool runs without asking.
	page := readAll(t, first(a.ask(ada, turnBody)))
	if len(page.interrupts) != 0 || ran.Load() != 2 {
		t.Fatalf("a trusted tool asked again: interrupts %v, ran %d", page.interrupts, ran.Load())
	}

	// Ada's trust is Ada's and nobody else's. (Checked on the row, which
	// is per account.)
	if a.rows.trust[trustKey(ben, "write_note")] {
		t.Fatal("one person's always-allow was given to another")
	}

	// The rule is removed in settings. Nothing is restarted or
	// invalidated; the next call asks.
	a.rows.mu.Lock()
	delete(a.rows.trust, trustKey(ada, "write_note"))
	a.rows.mu.Unlock()
	page = readAll(t, first(a.ask(ada, turnBody)))
	if len(page.interrupts) != 1 || ran.Load() != 2 {
		t.Fatalf("a tool whose trust was removed ran unasked: interrupts %v, ran %d", page.interrupts, ran.Load())
	}
}

// A tool that must always ask cannot be allowed for good: the button is not
// offered, an "always" answer is refused and writes nothing, and a trust row
// that exists anyway is not consulted.
func TestAnAlwaysAskToolCanNeverBeAutoAllowed(t *testing.T) {
	var ran atomic.Int64
	fetch := notes(tool.AlwaysAsk, &ran)
	fetch.Name = "fetch_page"
	script := modeltest.NewScript(
		modeltest.Reply{Calls: call("f1", "fetch_page", `{"url":"https://example.org"}`)}, modeltest.Reply{Text: "Fetched."},
		modeltest.Reply{Calls: call("f2", "fetch_page", `{"url":"https://example.org/2"}`)},
	)
	a := newApp(t, script, []tool.Tool{fetch})
	// A row that should not exist, as an old version or a hand edit might
	// have left: it must change nothing.
	a.rows.trust[trustKey(ada, "fetch_page")] = true

	page := readAll(t, first(a.ask(ada, turnBody)))
	if len(page.interrupts) != 1 || ran.Load() != 0 {
		t.Fatalf("an always-ask tool ran on a trust row: interrupts %v, ran %d", page.interrupts, ran.Load())
	}
	allowed, _ := json.Marshal(page.interrupts[0]["review_configs"])
	if strings.Contains(string(allowed), "approve_always") {
		t.Fatalf("always-allow was offered for an always-ask tool: %s", allowed)
	}
	delete(a.rows.trust, trustKey(ada, "fetch_page"))

	refused := a.call(ada, http.MethodPost, resumePath, `{"decisions":[{"type":"approve_always","tool_call_id":"f1"}]}`)
	if refused.status != http.StatusUnprocessableEntity || !strings.Contains(refused.raw, "asks every time") {
		t.Fatalf("always-allow for an always-ask tool: %d %s", refused.status, refused.raw)
	}
	if len(a.rows.trust) != 0 || ran.Load() != 0 || len(a.rows.assistant(1)) != 1 {
		t.Fatalf("a refused decision changed something: trust %v, ran %d", a.rows.trust, ran.Load())
	}
	// The refusal left the turn waiting and the thread free: a plain
	// approval still works, and the next call asks again.
	response := a.request(ada, http.MethodPost, resumePath, `{"decisions":[{"type":"approve","tool_call_id":"f1"}]}`)
	readFrames(t, response.Body)
	response.Body.Close()
	if ran.Load() != 1 {
		t.Fatalf("ran %d", ran.Load())
	}
	page = readAll(t, first(a.ask(ada, turnBody)))
	if len(page.interrupts) != 1 || ran.Load() != 1 {
		t.Fatalf("the second call of an always-ask tool did not ask: %v, ran %d", page.interrupts, ran.Load())
	}
}

// Decisions that do not answer exactly the waiting calls are refused whole.
func TestDecisionsMustAnswerExactlyWhatIsWaiting(t *testing.T) {
	var ran atomic.Int64
	script := modeltest.NewScript(modeltest.Reply{Calls: []model.ToolCall{
		{ID: "w1", Name: "write_note", Args: `{}`}, {ID: "w2", Name: "write_note", Args: `{}`}}})
	a := newApp(t, script, []tool.Tool{notes(tool.Ask, &ran)})
	a.ask(ada, turnBody)
	for name, c := range map[string]struct{ body, says string }{
		"no id, which once meant 'by position'":  {`{"decisions":[{"type":"approve"},{"type":"approve"}]}`, "does not say which action"},
		"one of two":                             {`{"decisions":[{"type":"approve","tool_call_id":"w1"}]}`, "Every waiting action"},
		"a call that is not waiting":             {`{"decisions":[{"type":"approve","tool_call_id":"w1"},{"type":"approve","tool_call_id":"zz"}]}`, "not waiting"},
		"the same call twice":                    {`{"decisions":[{"type":"approve","tool_call_id":"w1"},{"type":"reject","tool_call_id":"w1"}]}`, "two decisions"},
		"an answer to a call that asked nothing": {`{"decisions":[{"type":"respond","tool_call_id":"w1"},{"type":"approve","tool_call_id":"w2"}]}`, "wrong kind"},
		"an unknown type":                        {`{"decisions":[{"type":"maybe","tool_call_id":"w1"}]}`, "type is not one"},
		"an edit with nothing edited":            {`{"decisions":[{"type":"edit","tool_call_id":"w1"}]}`, "edited arguments"},
		"no decisions":                           {`{"decisions":[]}`, "At least one"},
	} {
		got := a.call(ada, http.MethodPost, resumePath, c.body)
		if got.status != http.StatusUnprocessableEntity || !strings.Contains(got.raw, c.says) {
			t.Errorf("%s: %d %s", name, got.status, got.raw)
		}
	}
	if ran.Load() != 0 || len(a.rows.assistant(1)) != 1 {
		t.Fatalf("a refused resume ran something: ran %d, rows %d", ran.Load(), len(a.rows.assistant(1)))
	}
	if busy := a.call(ada, http.MethodGet, "/api/v1/threads/1/turn-status", ""); busy.body["status"] != "idle" {
		t.Errorf("a refused resume left the thread %s", busy.raw)
	}
	// And a thread with nothing waiting says so.
	if got := a.call(ada, http.MethodPost, "/api/v1/threads/2/resume", `{"decisions":[{"type":"approve","tool_call_id":"w1"}]}`); got.status != http.StatusConflict || got.code() != "NOT_AWAITING_APPROVAL" {
		t.Errorf("a resume of a thread that is not paused: %d %s", got.status, got.raw)
	}
}

// Stop ends a running turn from another request, keeps what had streamed,
// and frees the thread.
func TestStopEndsARunningTurnAndKeepsWhatItHad(t *testing.T) {
	script := modeltest.NewScript(
		modeltest.Reply{Text: "The first part of a long answer", Hang: true},
		modeltest.Reply{Text: "A fresh answer."},
	)
	a := newApp(t, script, nil)
	response := a.request(ada, http.MethodPost, TurnPath, turnBody)
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	until(t, reader, "text-delta")

	// While it runs: the status says so, and a second question is refused
	// in the shape the page reads.
	if got := a.call(ada, http.MethodGet, "/api/v1/threads/1/turn-status", ""); got.body["status"] != "busy" || got.raw != `{"status":"busy","active_turn_id":null,"retry_after_ms":null,"retry_after_at":null}` {
		t.Errorf("status while running = %s", got.raw)
	}
	busy := a.call(ada, http.MethodPost, TurnPath, turnBody)
	if busy.status != http.StatusConflict || busy.code() != "THREAD_BUSY" || busy.header.Get("X-Request-ID") != "req_test" {
		t.Fatalf("a second question while one runs: %d %s", busy.status, busy.raw)
	}
	if envelope, _ := busy.body["error"].(map[string]any); envelope["status"] != 409.0 || envelope["request_id"] != "req_test" {
		t.Errorf("the refusal has no envelope: %s", busy.raw)
	}

	stopped := a.call(ada, http.MethodPost, "/api/v1/threads/1/cancel-active-turn", "")
	if stopped.status != http.StatusAccepted || stopped.body["status"] != "cancelling" || stopped.body["error_code"] != "TURN_CANCELLING" || stopped.body["retry_after_ms"] != 300.0 {
		t.Fatalf("stop = %d %s", stopped.status, stopped.raw)
	}
	rest, done := readFrames(t, reader)
	// A stopped stream ends as a finished one, not as an error.
	if tail := kinds(rest[max(0, len(rest)-3):]); !done || tail != "data-turn-status finish-step finish" {
		t.Fatalf("the stopped stream ended: %s (done=%v)", kinds(rest), done)
	}
	saved := contentOf(t, a.rows.assistant(1)[0])
	if len(saved) != 1 || saved[0]["text"] != "The first part of a long answer" {
		t.Errorf("saved after a stop = %v", saved)
	}
	if len(a.rows.responding) != 0 {
		t.Errorf("still marked as answering %v", a.rows.responding)
	}

	// The thread is free at once, and stopping nothing is not an error.
	if page := readAll(t, first(a.ask(ada, turnBody))); page.failure != nil || page.parts[0]["text"] != "A fresh answer." {
		t.Fatalf("after a stop: %v %v", page.failure, page.parts)
	}
	idle := a.call(ada, http.MethodPost, "/api/v1/threads/1/cancel-active-turn", "")
	if idle.status != http.StatusOK || idle.body["status"] != "idle" || idle.body["error_code"] != "NO_ACTIVE_TURN" {
		t.Errorf("stop with nothing running = %d %s", idle.status, idle.raw)
	}
}

// A reader who closes the tab has not asked for the answer to be thrown
// away: the turn finishes and is saved, and progress was saved on the way.
func TestATurnFinishesAfterItsReaderLeaves(t *testing.T) {
	release := make(chan struct{})
	slow := tool.Tool{Name: "slow", Parameters: objectSchema, Approval: tool.Auto,
		Run: func(ctx context.Context, _ tool.Call) (string, error) {
			select {
			case <-release:
				return "done", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}}
	script := modeltest.NewScript(
		modeltest.Reply{Text: "Working on it.", Calls: call("s1", "slow", `{}`)},
		modeltest.Reply{Text: "Finished without you."},
	)
	a := newApp(t, script, []tool.Tool{slow})
	response := a.request(ada, http.MethodPost, TurnPath, turnBody)
	reader := bufio.NewReader(response.Body)
	until(t, reader, "tool-input-available")
	// The answer so far is in the row while the tool is still running.
	eventually(t, "progress to be saved", func() bool {
		rows := a.rows.assistant(1)
		return len(rows) == 1 && strings.Contains(rows[0].Content, "Working on it.")
	})
	response.Body.Close() // the tab is closed
	close(release)
	eventually(t, "the turn to finish unobserved", func() bool {
		rows := a.rows.assistant(1)
		return strings.Contains(rows[0].Content, "Finished without you.")
	})
	eventually(t, "the thread to be freed", func() bool {
		return a.call(ada, http.MethodGet, "/api/v1/threads/1/turn-status", "").body["status"] == "idle"
	})
}

// A turn that outlives its limit is stopped, says so, and frees the thread.
func TestATurnThatTakesTooLongIsStopped(t *testing.T) {
	script := modeltest.NewScript(modeltest.Reply{Text: "Thinking", Hang: true})
	a := newApp(t, script, nil, func(s *Service, _ *scripted) { s.Timing.MaxTurn = 150 * time.Millisecond })
	page := readAll(t, first(a.ask(ada, turnBody)))
	if page.failure == nil || page.failure["errorCode"] != "TURN_TIMEOUT" {
		t.Fatalf("failure = %v", page.failure)
	}
	if got := a.call(ada, http.MethodGet, "/api/v1/threads/1/turn-status", ""); got.body["status"] != "idle" {
		t.Errorf("status after a timeout = %s", got.raw)
	}
}

// A model that fails, and a database that cannot save the answer, are both
// an error frame with a sentence. The cause stays in the log.
func TestAFailedTurnTellsTheReaderASentenceAndNotTheCause(t *testing.T) {
	failing := modeltest.NewScript(modeltest.Reply{Text: "Half", Err: errors.New("upstream http://10.1.2.3:4000 said 500 for key sk-secret")})
	a := newApp(t, failing, nil)
	frames, done := a.ask(ada, turnBody)
	page := readAll(t, frames)
	if !done || page.failure == nil || page.failure["errorCode"] != "SERVER_ERROR" || page.failure["message"] != failedMessage {
		t.Fatalf("failure = %v", page.failure)
	}
	raw, _ := json.Marshal(frames)
	if strings.Contains(string(raw), "10.1.2.3") || strings.Contains(string(raw), "sk-secret") {
		t.Fatal("the cause of a failure was sent to the reader")
	}
	if text := contentOf(t, a.rows.assistant(1)[0])[0]["text"]; text != "Half" {
		t.Errorf("what had streamed was not saved: %v", text)
	}

	unsaved := newApp(t, modeltest.NewScript(modeltest.Reply{Text: "Answer."}), nil)
	unsaved.rows.failClose = true
	page = readAll(t, first(unsaved.ask(ada, turnBody)))
	if page.failure == nil || page.failure["errorCode"] != "SERVER_ERROR" {
		t.Fatalf("an answer that could not be saved was reported as fine: %v", page.failure)
	}
	if raw, _ := json.Marshal(page.failure); strings.Contains(string(raw), "5432") {
		t.Fatal("the database's address was sent to the reader")
	}
}

// A model that says nothing leaves a message that says so.
func TestAnEmptyAnswerIsSavedAsNoResponse(t *testing.T) {
	a := newApp(t, modeltest.NewScript(modeltest.Reply{}), nil)
	a.ask(ada, turnBody)
	saved := a.rows.assistant(1)[0]
	if got, _ := json.Marshal(contentOf(t, saved)); string(got) != `[{"code":"no_response","text":"No response was produced.","type":"status"}]` {
		t.Fatalf("saved %s", saved.Content)
	}
}

func TestWhoMayAskAndWhatIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		caller, body string
		status       int
		says         string
	}{
		"nobody signed in":                    {"", turnBody, 500, ""},
		"another account: no such thread":     {dee, turnBody, 404, "Thread not found"},
		"a second account, the same":          {ben, turnBody, 404, "Thread not found"},
		"a thread that does not exist":        {ada, `{"chat_id":999,"user_query":"x"}`, 404, "Thread not found"},
		"an id past every thread":             {ada, `{"chat_id":99999999999,"user_query":"x"}`, 404, "Thread not found"},
		"another account's thread":            {ada, `{"chat_id":3,"user_query":"x"}`, 404, "Thread not found"},
		"a body that names the owner":         {ben, `{"chat_id":1,"user_id":"` + ada + `","workspace_id":7,"user_query":"x"}`, 404, "Thread not found"},
		"an empty message":                    {ada, `{"chat_id":1,"user_query":"   "}`, 422, "empty"},
		"no thread named":                     {ada, `{"user_query":"x"}`, 422, "chat_id"},
		"a body that is not an object":        {ada, `[1,2]`, 422, ""},
		"a scraper that was cut":              {ada, `{"chat_id":1,"user_query":"x","preferred_tool":"reddit"}`, 422, "reddit tool is not available"},
		"an unknown scope":                    {ada, `{"chat_id":1,"user_query":"x","retrieval_scope":"everything"}`, 422, "retrieval_scope"},
		"web only, with a document mentioned": {ada, `{"chat_id":1,"user_query":"x","retrieval_scope":"web","mentioned_document_ids":[4]}`, 422, "cannot be limited to the web"},
		"an image, which is not read yet":     {ada, `{"chat_id":1,"user_query":"x","user_images":[{"media_type":"image/png","data":"AAAA"}]}`, 422, "Images"},
	} {
		a := newApp(t, modeltest.NewScript(), nil)
		got := a.call(c.caller, http.MethodPost, TurnPath, c.body)
		if got.status != c.status || !strings.Contains(got.raw, c.says) {
			t.Errorf("%s: %d %s", name, got.status, got.raw)
		}
		if len(a.rows.messages) != 0 {
			t.Errorf("%s: a refused question was saved", name)
		}
		if len(a.assembly.builds) != 0 && c.status == 404 {
			t.Errorf("%s: an agent was built for a thread the caller does not have", name)
		}
		if busy := a.call(ada, http.MethodGet, "/api/v1/threads/1/turn-status", ""); busy.body["status"] != "idle" {
			t.Errorf("%s: a refused question left the thread busy", name)
		}
	}
}

// Every route that names a thread, asked by an account the thread is not
// of: each answers what it answers for a thread that was never made, and
// does nothing. A turn of Ada's is paused for an approval while the others
// ask, so there is something to read, to answer, to stop and to replace.
func TestAnotherAccountsThreadIsNotFoundOnEveryRoute(t *testing.T) {
	var ran atomic.Int64
	script := modeltest.NewScript(
		modeltest.Reply{Text: "Noting it.", Calls: []model.ToolCall{{ID: "w1", Name: "write_note", Args: `{}`}}},
		modeltest.Reply{Text: "Noted."},
	)
	a := newApp(t, script, []tool.Tool{notes(tool.Ask, &ran)})
	if page := readAll(t, first(a.ask(ada, turnBody))); len(page.interrupts) != 1 {
		t.Fatalf("the turn did not pause: %+v", page)
	}
	rowsBefore, _ := json.Marshal(a.rows.thread(1))
	builds := len(a.assembly.builds)

	approve := `{"decisions":[{"type":"approve_always","tool_call_id":"w1"}]}`
	for _, route := range []struct{ method, path, body string }{
		{http.MethodPost, TurnPath, `{"chat_id":1,"user_query":"Let me in"}`},
		{http.MethodPost, "/api/v1/threads/1/resume", approve},
		{http.MethodPost, "/api/v1/threads/1/regenerate", `{"user_query":"Replaced"}`},
		{http.MethodPost, "/api/v1/threads/1/cancel-active-turn", ""},
		{http.MethodGet, "/api/v1/threads/1/turn-status", ""},
		{http.MethodGet, "/api/v1/threads/1/pending-interrupts", ""},
	} {
		for _, stranger := range []string{ben, dee} {
			got := a.call(stranger, route.method, route.path, route.body)
			never := a.call(stranger, route.method, strings.Replace(strings.Replace(route.path, "/threads/1/", "/threads/999/", 1), `"chat_id":1`, `"chat_id":999`, 1), strings.Replace(route.body, `"chat_id":1`, `"chat_id":999`, 1))
			if got.status != 404 || !strings.Contains(got.raw, "Thread not found") || never.status != 404 || !strings.Contains(never.raw, "Thread not found") {
				t.Errorf("%s %s by another account: %d %s; a missing thread: %d %s", route.method, route.path, got.status, got.raw, never.status, never.raw)
			}
		}
	}
	// Nothing ran, nothing was written, nothing was trusted, no agent was
	// built for them, and the turn still waits for Ada.
	if rowsAfter, _ := json.Marshal(a.rows.thread(1)); string(rowsAfter) != string(rowsBefore) {
		t.Errorf("the thread's rows changed:\n%s\nwere\n%s", rowsAfter, rowsBefore)
	}
	if ran.Load() != 0 || len(a.rows.trust) != 0 || len(a.assembly.builds) != builds {
		t.Errorf("ran %d, trust %v, %d agents built for strangers", ran.Load(), a.rows.trust, len(a.assembly.builds)-builds)
	}
	waiting := a.call(ada, http.MethodGet, "/api/v1/threads/1/pending-interrupts", "")
	if list, _ := waiting.body["pending_interrupts"].([]any); len(list) != 1 {
		t.Fatalf("what waits for Ada afterwards: %s", waiting.raw)
	}
	// And it is still Ada's to answer.
	response := a.request(ada, http.MethodPost, "/api/v1/threads/1/resume", `{"decisions":[{"type":"approve","tool_call_id":"w1"}]}`)
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("Ada's own resume: %d", response.StatusCode)
	}
	if _, done := readFrames(t, response.Body); !done || ran.Load() != 1 {
		t.Errorf("Ada's approval: done %v, ran %d", done, ran.Load())
	}
}

// The small routes are the owner's, and a turn is answered for the account
// that owns its thread.
func TestTheSmallRoutesAreTheOwners(t *testing.T) {
	a := newApp(t, modeltest.NewScript(modeltest.Reply{Text: "Hello Ada."}), nil)
	page := readAll(t, first(a.ask(ada, `{"chat_id":2,"user_query":"Hi"}`)))
	if page.failure != nil || a.rows.messages[0].Author != ada || a.assembly.builds[0].UserID != ada {
		t.Fatalf("failure %v, rows %+v, built for %+v", page.failure, a.rows.messages, a.assembly.builds)
	}
	for name, c := range map[string]struct {
		caller, method, path string
		status               int
	}{
		"the owner sees the status":           {ada, http.MethodGet, "/api/v1/threads/2/turn-status", 200},
		"the owner sees what is waiting":      {ada, http.MethodGet, "/api/v1/threads/2/pending-interrupts", 200},
		"another account sees no thread":      {dee, http.MethodGet, "/api/v1/threads/2/turn-status", 404},
		"nor what is waiting in it":           {ben, http.MethodGet, "/api/v1/threads/2/pending-interrupts", 404},
		"nor can it stop a turn":              {ben, http.MethodPost, "/api/v1/threads/2/cancel-active-turn", 404},
		"Dee's own thread is Dee's":           {dee, http.MethodGet, "/api/v1/threads/3/turn-status", 200},
		"and not Ada's":                       {ada, http.MethodGet, "/api/v1/threads/3/turn-status", 404},
		"a thread id that is not a number":    {ada, http.MethodGet, "/api/v1/threads/abc/turn-status", 422},
		"a thread id past any column":         {ada, http.MethodGet, "/api/v1/threads/99999999999999999999/turn-status", 404},
		"a thread id past the old int column": {ada, http.MethodGet, "/api/v1/threads/99999999999/turn-status", 404},
	} {
		if got := a.call(c.caller, c.method, c.path, ""); got.status != c.status {
			t.Errorf("%s: %d %s", name, got.status, got.raw)
		}
	}
	if got := a.call(ada, http.MethodGet, "/api/v1/threads/2/pending-interrupts", ""); got.raw != `{"assistant_message_id":null,"pending_interrupts":[]}` {
		t.Errorf("nothing waiting = %s", got.raw)
	}
}

// What the person picked and mentioned reaches the turn's agent, is put
// before their message for the model, and is not saved into their message.
func TestThePickAndTheMentionsReachTheTurn(t *testing.T) {
	script := modeltest.NewScript(modeltest.Reply{Text: "Ok."})
	a := newApp(t, script, nil)
	body := `{"chat_id":1,"user_query":"Find trials","preferred_tool":"pubmed","mentioned_document_ids":[4,5],"mentioned_folder_ids":[9],
		"mentioned_documents":[{"id":4,"title":"Notes","kind":"doc","document_type":"FILE"},{"id":9,"title":"Folder","kind":"folder"},{"id":5,"title":"No type","kind":"doc"}]}`
	a.ask(ada, body)
	turn := a.assembly.builds[0]
	// A pick widens a documents-only scope: picking a web source is asking
	// for the web.
	if turn.PreferredTool != "pubmed" || turn.Scope != "all" || len(turn.DocumentIDs) != 2 || turn.FolderIDs[0] != 9 || turn.UserID != ada {
		t.Fatalf("turn = %+v", turn)
	}
	sent := script.Requests()[0].Messages[1].Content
	if !strings.Contains(sent, "<tool_preference>") || !strings.Contains(sent, "PubMed") || !strings.HasSuffix(sent, "Find trials") {
		t.Errorf("the model was sent %q", sent)
	}
	// The chip that could not be drawn again (a document with no type) is
	// left out; the note about the pick is the model's, not the row's.
	want := `[{"type":"text","text":"Find trials"},{"type":"mentioned-documents","documents":[{"id":4,"title":"Notes","kind":"doc","document_type":"FILE"},{"id":9,"title":"Folder","kind":"folder"}]}]`
	if got := a.rows.messages[0].Content; got != want {
		t.Errorf("user row\n got %s\nwant %s", got, want)
	}
}

// A title that cannot be made costs the thread its name and nothing else.
func TestATurnWithoutATitleStillAnswers(t *testing.T) {
	a := newApp(t, modeltest.NewScript(modeltest.Reply{Text: "Answer."}), nil,
		func(s *Service, _ *scripted) { s.Titler = fixedTitle{err: errors.New("no")} })
	frames, _ := a.ask(ada, turnBody)
	if page := readAll(t, frames); page.failure != nil || page.title != "" || len(a.rows.titles) != 0 {
		t.Fatalf("failure %v title %q", page.failure, page.title)
	}
}

func TestTheServiceRefusesToStartUnconfigured(t *testing.T) {
	if err := (&Service{}).Check(); err == nil {
		t.Fatal("an empty service was accepted")
	}
	if err := (Timing{MaxTurn: time.Second, KeepAlive: time.Second, SaveEvery: time.Second, Title: time.Second}).check(); err == nil {
		t.Fatal("a timing with no stall limit was accepted")
	}
	if err := (Assembly{}).Check(); err == nil {
		t.Fatal("an empty assembly was accepted")
	}
}
