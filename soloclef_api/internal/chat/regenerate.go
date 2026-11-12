package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// Asking a question again (Regenerate), or asking it reworded (Edit).
//
// Both replace a turn from its question on: the question, its answer and
// every message after them. The page names a message; an answer stands for
// its turn's question; with no message named it is the latest question.
//
// # One engine per thread
//
// The new answer is produced from this engine's own transcript of the
// thread, never from another's. The loop's conversation is cut at the
// message that opened the replaced turn (it is marked with the turn's id,
// see markOf) and the question is asked from there, so the model sees
// exactly what it saw the first time, tool results included, and nothing of
// the answer being replaced.
//
// A turn the transcript does not hold (it predates this engine, or a
// summary has replaced it) cannot be cut to. Then the transcript is dropped
// and rebuilt from the saved messages before the question, as it is for any
// thread this engine meets for the first time. That is a stated rule and
// not a quiet substitute: the saved messages are the truth of what was
// said, and the transcript is a richer copy of them.
//
// # The order, and what a failure leaves
//
//  1. The thread is held, so no turn can start beside this one.
//  2. The transcript is cut (or dropped). If that fails nothing has changed.
//  3. The replaced rows are deleted and the new ones written, in one
//     transaction. If that fails after step 2, the transcript is dropped so
//     the next turn rebuilds it from the rows, which still hold the old
//     turn: the page and the model agree again.
//
// The chat this replaces deleted the old rows after the stream, in a
// clean-up that could be cut short, and re-ran from a checkpoint id it had
// to search the history for.
//
// # What is not here
//
// "revert_actions" is accepted and does nothing: it undoes reversible file
// actions of the replaced turns, and no tool of this engine makes one.

// regenerateRequest is the body, as far as this tier reads it.
type regenerateRequest struct {
	// Query is the reworded question; absent or null asks the saved one.
	Query         *string   `json:"user_query"`
	Scope         string    `json:"retrieval_scope"`
	DocumentIDs   []int64   `json:"mentioned_document_ids"`
	FolderIDs     []int64   `json:"mentioned_folder_ids"`
	Mentions      []Mention `json:"mentioned_documents"`
	DisabledTools []string  `json:"disabled_tools"`
	FromMessageID *int64    `json:"from_message_id"`
	// Images is read only to refuse it, as on a new turn.
	Images []json.RawMessage `json:"user_images"`
}

const (
	noQuestion        = "There is no question in this chat to ask again."
	messageNotInChat  = "That message is not in this chat."
	imageOnlyQuestion = "This question has no text to ask again. Edit it and add some."
)

func readRegenerate(raw []byte) (regenerateRequest, []errorenvelope.Failure) {
	var request regenerateRequest
	if failures := decode(raw, &request); failures != nil {
		return request, failures
	}
	var failures []errorenvelope.Failure
	if request.Query != nil {
		trimmed := strings.TrimSpace(*request.Query)
		request.Query = &trimmed
		switch {
		case trimmed == "":
			failures = append(failures, failure("user_query", "The message is empty"))
		case len([]rune(trimmed)) > maxQueryChars:
			failures = append(failures, failure("user_query", fmt.Sprintf("The message is longer than %d characters", maxQueryChars)))
		}
	}
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
	if len(request.DisabledTools) > maxDisabledTools {
		failures = append(failures, failure("disabled_tools", "Too many tools are listed as switched off"))
	}
	if request.FromMessageID != nil && *request.FromMessageID < 1 {
		failures = append(failures, failure("from_message_id", "from_message_id is not a message's id"))
	}
	for _, id := range append(append([]int64(nil), request.DocumentIDs...), request.FolderIDs...) {
		if id < 1 {
			failures = append(failures, failure("mentioned_document_ids", "A mentioned source's id is not valid"))
			break
		}
	}
	return request, failures
}

// loadState reads the loop's saved conversation of a thread without an
// agent, which the regenerate route needs before it can build one: the pick
// the question was asked with is in the conversation.
func loadState(ctx context.Context, store checkpoint.Store, thread string) (state agent.State, status checkpoint.Status, found bool, err error) {
	saved, err := store.Load(ctx, thread)
	if errors.Is(err, checkpoint.ErrNotFound) {
		return agent.State{}, "", false, nil
	}
	if err != nil {
		return agent.State{}, "", false, err
	}
	if err := json.Unmarshal(saved.State, &state); err != nil {
		return agent.State{}, "", false, fmt.Errorf("chat: the thread's saved conversation is not readable: %w", err)
	}
	return state, saved.Status, true, nil
}

func (r Routes) regenerate(ctx huma.Context) {
	s := r.Service
	response := humawriter.New(ctx)
	answer := newAnswerer(ctx, response, r.Errors, s.Logger, "a chat turn could not be asked again")
	caller, signedIn := answer.signedIn()
	if !signedIn {
		return
	}
	threadID, readable := answer.threadID()
	if !readable {
		return
	}
	request, failures := readRegenerate(requestbody.Buffered(ctx.Context()))
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, answer.requestID, failures)
		return
	}
	thread, err := s.authorise(ctx.Context(), caller, threadID)
	if err != nil {
		answer.refuse(err, "could not authorise the regenerate")
		return
	}

	turnCtx, lease, release, err := r.begin(thread.ID)
	if err != nil {
		answer.refuse(err, "could not claim the thread")
		return
	}
	handed := false
	defer func() {
		if !handed {
			lease.End()
			release()
		}
	}()

	var from int64
	if request.FromMessageID != nil {
		from = *request.FromMessageID
	}
	question, err := s.Rows.Question(turnCtx, thread.ID, from)
	switch {
	case errors.Is(err, errNotInThread):
		answer.refuse(&refusal{status: http.StatusNotFound, message: messageNotInChat}, "")
		return
	case err != nil:
		answer.fail("could not find the question to ask again", err)
		return
	case question == nil:
		answer.refuse(&refusal{status: http.StatusBadRequest, message: noQuestion}, "")
		return
	}

	name := agentThread(thread.ID)
	state, status, known, err := loadState(turnCtx, s.Store, name)
	if err != nil {
		answer.fail("could not read the thread's state", err)
		return
	}
	if known && status == checkpoint.Paused {
		answer.refuse(&refusal{status: http.StatusConflict, code: codeAwaitingApproval, message: awaitingMessage}, "")
		return
	}
	at, mark, held := markedTurn(state.Messages, question.TurnID)

	// What is asked: the reworded question, else the saved one.
	edit := request.Query != nil
	query := textOf(question.Content)
	content := question.Content
	if edit {
		query = *request.Query
		if content, err = userContent(query, request.Mentions); err != nil {
			answer.fail("could not encode the question", err)
			return
		}
	}
	if strings.TrimSpace(query) == "" {
		answer.refuse(&refusal{status: http.StatusBadRequest, message: imageOnlyQuestion}, "")
		return
	}

	// The question is asked with the tool it was asked with. The page
	// forgot the pick when the question was sent; the mark kept it.
	turn := Turn{
		ThreadID: thread.ID, UserID: thread.UserID,
		Scope: request.Scope, PreferredTool: mark.Tool,
		DocumentIDs: request.DocumentIDs, FolderIDs: request.FolderIDs,
		DisabledTools: request.DisabledTools,
	}
	if assembly.IsPick(turn.PreferredTool) && turn.Scope == scopeDocuments {
		turn.Scope = scopeAll
	}
	// As for a new message: a document mentioned with the question becomes
	// a source of its notebook before the agent's search is confined.
	if err := s.prepareNotebook(turnCtx, turn, query); err != nil {
		answer.fail("could not add the mentioned sources to the notebook", err)
		return
	}
	built, err := s.Assembler.Build(turnCtx, turn)
	if err != nil {
		answer.refuse(err, "could not build the turn's agent")
		return
	}
	defer func() {
		if !handed {
			built.Close()
		}
	}()

	turnID := s.newTurnID(thread.ID)
	asked := model.Message{Role: model.RoleUser, Content: built.Preface + query, Mark: markOf(turnID, turn.PreferredTool)}
	input := []model.Message{asked}
	switch {
	case held:
		// Cut the conversation where the replaced turn began. The index is
		// found again inside the edit, in the state as it is read there.
		err = built.Agent.Edit(turnCtx, name, func(st *agent.State) error {
			index, _, still := markedTurn(st.Messages, question.TurnID)
			if !still || index != at {
				return errors.New("chat: the thread's conversation changed while it was held")
			}
			st.Messages = st.Messages[:index]
			st.Batch, st.Steps, st.TurnTrust = nil, 0, nil
			return nil
		})
		if err != nil {
			answer.fail("could not rewind the thread's conversation", err)
			return
		}
	default:
		// The transcript does not hold this turn. It is rebuilt from the
		// saved messages before the question.
		if known {
			if err := s.Store.Delete(turnCtx, name); err != nil {
				answer.fail("could not drop the thread's transcript", err)
				return
			}
		}
		history, err := s.Rows.HistoryBefore(turnCtx, thread.ID, question)
		if err != nil {
			answer.fail("could not read the thread's history", err)
			return
		}
		input = withQuestion(transcriptFromRows(history), asked)
	}

	if err := (savedTurns{s.Store}).keep(turnCtx, turn); err != nil {
		answer.fail("could not keep the turn's choices", err)
		return
	}
	opened, err := s.Rows.ReplaceTurn(turnCtx, question, NewTurn{
		ThreadID: thread.ID, UserID: thread.UserID, TurnID: turnID, UserContent: content, AssistantContent: emptyAnswer,
	})
	if err != nil {
		// The transcript has moved and the rows have not. Dropping the
		// transcript makes the rows the only account of the thread again.
		if dropErr := s.Store.Delete(context.WithoutCancel(turnCtx), name); dropErr != nil {
			s.Logger.ErrorContext(turnCtx, "a thread's transcript could not be dropped after its rows failed to be replaced; its model may not see the last turn the page shows", "thread_id", thread.ID, "error", dropErr.Error())
		}
		answer.fail("could not replace the turn's messages", err)
		return
	}
	out, flush, err := r.openStream(response)
	if err != nil {
		answer.fail("could not pace the stream", err)
		return
	}
	handed = true
	s.stream(turnCtx, job{
		turn: turn, built: built, lease: lease, release: release, turnID: turnID, opened: opened,
		run: s.starting(built, thread.ID, input),
	}, out, flush, ctx.Context().Done())
}
