package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// The retrieval scopes the composer offers: the user's documents, the web,
// or both.
const (
	scopeDocuments = assembly.ScopeDocuments
	scopeWeb       = assembly.ScopeWeb
	scopeAll       = assembly.ScopeAll
)

// maxQueryChars bounds one message. It is far above anything typed and
// below what would fill a model's context by itself.
const maxQueryChars = 100_000

// Mention is a source chip on the user's message, stored with the message so
// the chip is drawn again when the thread is reloaded.
type Mention struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Kind          string `json:"kind"`
	DocumentType  string `json:"document_type,omitempty"`
	ConnectorType string `json:"connector_type,omitempty"`
	AccountName   string `json:"account_name,omitempty"`
}

// turnRequest is the body of POST /new_chat, as far as this tier reads it.
//
// The apps send more (the page's own copy of the history, the desktop
// app's file-system mode, and ids an older app still sends); those fields
// are accepted and not used. Whose the turn is comes from the
// request's principal and the thread it owns, never from the body. The
// history in particular is never read from the request: what the model is
// shown is what the server stored, not what a client says was said.
type turnRequest struct {
	ChatID        int64     `json:"chat_id"`
	Query         string    `json:"user_query"`
	Scope         string    `json:"retrieval_scope"`
	PreferredTool string    `json:"preferred_tool"`
	DocumentIDs   []int64   `json:"mentioned_document_ids"`
	FolderIDs     []int64   `json:"mentioned_folder_ids"`
	Mentions      []Mention `json:"mentioned_documents"`
	// DisabledTools are the tools the person switched off in the composer's
	// tool list (GET /agent/tools names them).
	DisabledTools []string `json:"disabled_tools"`
	// Images is read only to refuse it: see readTurn.
	Images []json.RawMessage `json:"user_images"`
}

func failure(field, message string) errorenvelope.Failure {
	return errorenvelope.Failure{Location: []string{"body", field}, Message: message}
}

// decode reads a JSON object body into target. Unknown fields are allowed:
// the apps send fields this tier does not use, and a newer app must not be
// refused by an older server for one more.
func decode(raw []byte, target any) []errorenvelope.Failure {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(target); err != nil {
		return []errorenvelope.Failure{{Location: []string{"body"}, Message: "The body is not the JSON object this route reads: " + err.Error()}}
	}
	return nil
}

// readTurn checks a turn request and returns it normalised.
func readTurn(raw []byte) (turnRequest, []errorenvelope.Failure) {
	var request turnRequest
	if failures := decode(raw, &request); failures != nil {
		return request, failures
	}
	var failures []errorenvelope.Failure
	if request.ChatID < 1 {
		failures = append(failures, failure("chat_id", "chat_id is required"))
	}
	request.Query = strings.TrimSpace(request.Query)
	switch {
	case request.Query == "":
		failures = append(failures, failure("user_query", "The message is empty"))
	case len([]rune(request.Query)) > maxQueryChars:
		failures = append(failures, failure("user_query", fmt.Sprintf("The message is longer than %d characters", maxQueryChars)))
	}
	// Refused, not dropped: answering a question about a picture without
	// the picture would be an answer to a different question.
	if len(request.Images) > 0 {
		failures = append(failures, failure("user_images", "Images cannot be sent in chat in this version yet"))
	}
	switch request.Scope {
	case "":
		request.Scope = scopeDocuments
	case scopeDocuments, scopeWeb, scopeAll:
	default:
		failures = append(failures, failure("retrieval_scope", "retrieval_scope must be documents, web or all"))
	}
	if request.PreferredTool != "" {
		if !assembly.IsPick(request.PreferredTool) {
			failures = append(failures, failure("preferred_tool", "The "+request.PreferredTool+" tool is not available in chat"))
		} else if request.Scope == scopeDocuments {
			// Picking a web tool is asking for the web: the scope follows
			// the pick instead of silently hiding the picked tool.
			request.Scope = scopeAll
		}
	}
	if len(request.DisabledTools) > maxDisabledTools {
		failures = append(failures, failure("disabled_tools", "Too many tools are listed as switched off"))
	}
	if request.Scope == scopeWeb && (len(request.DocumentIDs) > 0 || len(request.FolderIDs) > 0) {
		failures = append(failures, failure("retrieval_scope", "A message that mentions documents cannot be limited to the web"))
	}
	for _, id := range append(append([]int64(nil), request.DocumentIDs...), request.FolderIDs...) {
		if id < 1 {
			failures = append(failures, failure("mentioned_document_ids", "A mentioned source's id is not valid"))
			break
		}
	}
	return request, failures
}

// userContent is the user message's saved content: the text, then the
// mention chips if there are any. A chip that could not be drawn again (no
// title, a document chip with no type) is left out rather than saved broken.
func userContent(query string, mentions []Mention) ([]byte, error) {
	type textPart struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type mentionsPart struct {
		Type      string    `json:"type"`
		Documents []Mention `json:"documents"`
	}
	content := []any{textPart{"text", query}}
	var kept []Mention
	for _, m := range mentions {
		switch m.Kind {
		case "":
			m.Kind = "doc"
		case "doc", "folder", "connector", "thread":
		default:
			m.Kind = "doc"
		}
		if m.ID < 1 || strings.TrimSpace(m.Title) == "" || (m.Kind == "doc" && m.DocumentType == "") || (m.Kind == "connector" && m.ConnectorType == "") {
			continue
		}
		if m.Kind == "connector" && m.AccountName == "" {
			m.AccountName = m.Title
		}
		kept = append(kept, m)
	}
	if len(kept) > 0 {
		content = append(content, mentionsPart{"mentioned-documents", kept})
	}
	// Not HTML-escaped: the content is stored in a jsonb column and read
	// by JSON.parse, and "<" in a question is "<".
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(content); err != nil {
		return nil, err
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}

// resumeRequest is the body of POST /threads/{id}/resume.
type resumeRequest struct {
	Decisions []json.RawMessage `json:"decisions"`
}

// decision is one item of "decisions" as the page sends it: an approval
// card's answer, or a question card's.
type decision struct {
	Type       string `json:"type"`
	ToolCallID string `json:"tool_call_id"`
	// Message is the person's reason for a rejection.
	Message string `json:"message"`
	// EditedAction carries the arguments the person changed.
	EditedAction *struct {
		Args json.RawMessage `json:"args"`
	} `json:"edited_action"`
}

// readResume checks a resume request and turns its decisions into the
// loop's. Every decision must say which call it answers: the id the
// interrupt frame carried. They are never matched by their place in the
// list, which is how an approval meant for one call once reached another.
// Whether the set answers exactly the waiting calls is the loop's check.
func readResume(raw []byte) ([]agent.Decision, []string, []errorenvelope.Failure) {
	var request resumeRequest
	if failures := decode(raw, &request); failures != nil {
		return nil, nil, failures
	}
	var failures []errorenvelope.Failure
	if len(request.Decisions) == 0 {
		failures = append(failures, failure("decisions", "At least one decision is required"))
	}
	decisions := make([]agent.Decision, 0, len(request.Decisions))
	// always are the calls answered "always allow", whose tools are to be
	// remembered once the route has checked that they may be.
	var always []string
	for i, item := range request.Decisions {
		where := "decisions." + strconv.Itoa(i)
		var d decision
		if err := json.Unmarshal(item, &d); err != nil {
			failures = append(failures, failure(where, "The decision is not readable"))
			continue
		}
		if d.ToolCallID == "" {
			failures = append(failures, failure(where+".tool_call_id", "The decision does not say which action it answers"))
			continue
		}
		out := agent.Decision{CallID: d.ToolCallID}
		switch d.Type {
		case "approve":
			out.Verdict = agent.Approve
		case "edit":
			out.Verdict = agent.Approve
			if d.EditedAction == nil || len(d.EditedAction.Args) == 0 {
				failures = append(failures, failure(where+".edited_action", "An edit must carry the edited arguments"))
				continue
			}
			out.Args = d.EditedAction.Args
		case "approve_always":
			out.Verdict = agent.ApproveAlways
			always = append(always, d.ToolCallID)
		case "reject":
			out.Verdict = agent.Reject
			out.Reason = strings.TrimSpace(d.Message)
		case "respond", "cancel":
			// A question card's answer. The whole item is the answer: its
			// shape is between the card and the tool that asked.
			out.Verdict = agent.Answer
			out.Answer = item
		default:
			failures = append(failures, failure(where+".type", "The decision's type is not one this route knows"))
			continue
		}
		decisions = append(decisions, out)
	}
	return decisions, always, failures
}

// maxDisabledTools bounds the list of switched-off tools a request may
// carry. The chat has about a dozen switchable tools of its own; the bound
// is only so that a request cannot make every turn carry a long list in
// its saved choices.
const maxDisabledTools = 100
