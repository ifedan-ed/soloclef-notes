package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/uistream"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/turns"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/rollingdeadline"
)

// Routes are the chat's routes over one Service: the five of a turn, "ask
// again" (regenerate.go) and the model menu's two (models.go).
//
// The four that read a body (the turn, the resume, the regenerate and the
// model choice) are buffered by the tier's body reader before the route
// runs, so cmd/api lists those pairs in `withBodies`.
type Routes struct {
	Service *Service
	Errors  errorenvelope.Writer
}

// Register mounts the routes.
func (r Routes) Register(api huma.API) {
	for _, route := range []struct {
		id, method, path, summary string
		serve                     func(huma.Context)
	}{
		{"chat-turn", http.MethodPost, TurnPath, "Ask a question and stream the answer", r.turn},
		{"chat-resume", http.MethodPost, ResumePath, "Answer a paused turn's approvals and stream the rest", r.resume},
		{"chat-regenerate", http.MethodPost, RegeneratePath, "Ask a question again, or edited, and stream the new answer", r.regenerate},
		{"chat-stop", http.MethodPost, StopPath, "Stop the turn running on a thread", r.stop},
		{"chat-turn-status", http.MethodGet, StatusPath, "Whether a turn is running on a thread", r.status},
		{"chat-pending", http.MethodGet, PendingPath, "The actions of a paused turn that wait for a decision", r.pending},
	} {
		serve := route.serve
		huma.Register(api, huma.Operation{
			OperationID: route.id, Method: route.method, Path: route.path, Summary: route.summary, Tags: []string{"chat"},
		}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: serve}, nil
		})
	}
	r.registerModelRoutes(api)
}

// begin is what both streaming routes do once the request is understood:
// claim the thread and check the loop's state of it. On success the thread
// is held; the caller must end the lease on every path that does not hand
// it to stream.
func (r Routes) begin(threadID int64) (context.Context, *turns.Turn, context.CancelFunc, error) {
	s := r.Service
	// The turn's own context: the server's lifetime and the turn's limit.
	// Not the request's; see Service.Lifetime.
	limited, release := context.WithTimeout(s.Lifetime, s.Timing.MaxTurn)
	ctx, lease, err := s.Turns.Begin(limited, itoa(threadID))
	if errors.Is(err, turns.ErrBusy) {
		release()
		return nil, nil, nil, &refusal{status: http.StatusConflict, code: codeThreadBusy, message: busyMessage}
	}
	if err != nil {
		release()
		return nil, nil, nil, err
	}
	return ctx, lease, release, nil
}

// paused reports whether the loop holds the thread paused for approval, and
// whether it holds the thread at all.
func paused(ctx context.Context, built Built, threadID int64) (isPaused, known bool, err error) {
	_, status, err := built.Agent.Load(ctx, agentThread(threadID))
	if errors.Is(err, checkpoint.ErrNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return status == checkpoint.Paused, true, nil
}

// openStream writes the response's status and headers and returns where the
// frames go.
//
// The connection's write deadline is moved forward with every frame
// (internal/rollingdeadline): the server's own write timeout bounds a whole
// response, and an answer takes longer than that. If the deadline cannot be
// moved the route fails loudly rather than stream under the fixed timeout
// and be cut mid-answer.
func (r Routes) openStream(response http.ResponseWriter) (*rollingdeadline.Writer, func() error, error) {
	steady, err := rollingdeadline.Wrap(response, r.Service.Timing.Stall)
	if err != nil {
		return nil, nil, err
	}
	header := response.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	// Tells nginx-style proxies not to buffer the stream.
	header.Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	return steady, http.NewResponseController(steady).Flush, nil
}

func (r Routes) turn(ctx huma.Context) {
	s := r.Service
	response := humawriter.New(ctx)
	answer := newAnswerer(ctx, response, r.Errors, s.Logger, "a chat turn could not be started")
	caller, signedIn := answer.signedIn()
	if !signedIn {
		return
	}
	request, failures := readTurn(requestbody.Buffered(ctx.Context()))
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, answer.requestID, failures)
		return
	}
	thread, err := s.authorise(ctx.Context(), caller, request.ChatID)
	if err != nil {
		answer.refuse(err, "could not authorise the turn")
		return
	}
	// The turn runs for the thread's owner, who is the caller: the account
	// every later read and write of this turn is confined to.
	turn := Turn{
		ThreadID: thread.ID, UserID: thread.UserID,
		Scope: request.Scope, PreferredTool: request.PreferredTool,
		DocumentIDs: request.DocumentIDs, FolderIDs: request.FolderIDs,
		DisabledTools: request.DisabledTools,
	}
	// Before the agent is built: its search is confined to the notebook's
	// sources as this leaves them.
	if err := s.prepareNotebook(ctx.Context(), turn, request.Query); err != nil {
		answer.fail("could not add the mentioned sources to the notebook", err)
		return
	}
	built, err := s.Assembler.Build(ctx.Context(), turn)
	if err != nil {
		// A refusal is the account's model being gone or unreachable,
		// said in its own words; anything else is this server's fault.
		answer.refuse(err, "could not build the turn's agent")
		return
	}
	// Until the stream takes the turn over, every return gives back what
	// the turn opened and, once it is held, the thread.
	handed := false
	defer func() {
		if !handed {
			built.Close()
		}
	}()

	turnCtx, lease, release, err := r.begin(thread.ID)
	if err != nil {
		answer.refuse(err, "could not claim the thread")
		return
	}
	defer func() {
		if !handed {
			lease.End()
			release()
		}
	}()

	isPaused, known, err := paused(turnCtx, built, thread.ID)
	if err != nil {
		answer.fail("could not read the thread's state", err)
		return
	}
	if isPaused {
		answer.refuse(&refusal{status: http.StatusConflict, code: codeAwaitingApproval, message: awaitingMessage}, "")
		return
	}
	// Read before the new rows are written, so neither is in it.
	answers, err := s.Rows.AssistantMessages(turnCtx, thread.ID)
	if err != nil {
		answer.fail("could not count the thread's answers", err)
		return
	}
	turnID := s.newTurnID(thread.ID)
	// The message that opens the turn is marked with the turn, so that the
	// turn can be found in the loop's conversation again (regenerate.go).
	question := model.Message{Role: model.RoleUser, Content: built.Preface + request.Query, Mark: markOf(turnID, turn.PreferredTool)}
	input := []model.Message{question}
	if !known {
		// The loop has no transcript of this thread: it predates this
		// engine or was copied. Start it from the messages as saved.
		history, err := s.Rows.History(turnCtx, thread.ID)
		if err != nil {
			answer.fail("could not read the thread's history", err)
			return
		}
		input = withQuestion(transcriptFromRows(history), question)
	}
	if err := (savedTurns{s.Store}).keep(turnCtx, turn); err != nil {
		answer.fail("could not keep the turn's choices", err)
		return
	}
	content, err := userContent(request.Query, request.Mentions)
	if err != nil {
		answer.fail("could not encode the question", err)
		return
	}
	opened, err := s.Rows.OpenTurn(turnCtx, NewTurn{
		ThreadID: thread.ID, UserID: caller.UserID, TurnID: turnID, UserContent: content, AssistantContent: emptyAnswer,
	})
	if err != nil {
		answer.fail("could not write the turn's messages", err)
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
		first: answers == 0, question: request.Query,
		run: s.starting(built, thread.ID, input),
	}, out, flush, ctx.Context().Done())
}

// starting is a new turn's run: the thread is summarised first if it has
// grown long, then the loop starts on it.
//
// The summary is written inside the run and not before the stream opens,
// because it is a model call: the reader is already being sent keep-alives
// and can press Stop, and its tokens are counted with the turn's. If it
// fails, the turn is answered from the conversation as it is.
func (s *Service) starting(built Built, threadID int64, input []model.Message) func(context.Context, event.Sink) (agent.Result, error) {
	return func(ctx context.Context, sink event.Sink) (agent.Result, error) {
		thread := agentThread(threadID)
		if _, err := s.condense(ctx, built, thread, sink); err != nil && ctx.Err() == nil {
			s.Logger.WarnContext(ctx, "a long thread could not be summarised; answering from the whole conversation", "thread_id", threadID, "error", err.Error())
		}
		return built.Agent.Start(ctx, thread, input, sink)
	}
}

func (r Routes) resume(ctx huma.Context) {
	s := r.Service
	response := humawriter.New(ctx)
	answer := newAnswerer(ctx, response, r.Errors, s.Logger, "a chat turn could not be resumed")
	caller, signedIn := answer.signedIn()
	if !signedIn {
		return
	}
	threadID, readable := answer.threadID()
	if !readable {
		return
	}
	decisions, always, failures := readResume(requestbody.Buffered(ctx.Context()))
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, answer.requestID, failures)
		return
	}
	thread, err := s.authorise(ctx.Context(), caller, threadID)
	if err != nil {
		answer.refuse(err, "could not authorise the resume")
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

	// The paused turn's own choices, so the agent that resumes it has the
	// tools the one that paused had. The account is set from the thread
	// the gate just answered, never from what was saved: the saved choices
	// say what was picked, not who may act.
	turn, kept, err := (savedTurns{s.Store}).load(turnCtx, thread.ID)
	if err != nil {
		answer.fail("could not read the paused turn's choices", err)
		return
	}
	if !kept {
		answer.refuse(&refusal{status: http.StatusConflict, code: codeNotAwaiting, message: notAwaiting}, "")
		return
	}
	turn.UserID = thread.UserID
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
	waiting, err := built.Agent.Pending(turnCtx, agentThread(thread.ID))
	if err != nil {
		answer.fail("could not read what is waiting", err)
		return
	}
	if len(waiting) == 0 {
		answer.refuse(&refusal{status: http.StatusConflict, code: codeNotAwaiting, message: notAwaiting}, "")
		return
	}
	if problem := checkAgainstWaiting(waiting, decisions); problem != nil {
		r.Errors.WriteValidation(response, answer.requestID, []errorenvelope.Failure{*problem})
		return
	}
	// "Always allow" is remembered before the turn resumes, and only for a
	// tool that may be: checkAgainstWaiting refused the others. The loop
	// would also refuse them; this is the same rule held in two places on
	// purpose, because one of them writes a row.
	byID := make(map[string]agent.Approval, len(waiting))
	for _, w := range waiting {
		byID[w.CallID] = w
	}
	for _, callID := range always {
		// Through the turn's agent, which knows where each tool's trust is
		// kept: a connected app's on its connection, the rest in the rules.
		if err := built.Trust(turnCtx, byID[callID].Tool); err != nil {
			answer.fail("could not remember an always-allow", err)
			return
		}
	}
	// The message that paused, read before the resumed stretch's own row
	// exists: it is the thread's newest, and holds the cards being answered.
	pausedMessage, err := s.Rows.LatestAssistantMessage(turnCtx, thread.ID)
	if err != nil {
		answer.fail("could not find the paused turn's message", err)
		return
	}
	// Its content as it stands, for the step display to carry on from.
	var pausedContent []byte
	if pausedMessage != 0 {
		if pausedContent, err = s.Rows.MessageContent(turnCtx, thread.ID, pausedMessage); err != nil {
			answer.fail("could not read the paused turn's message", err)
			return
		}
	}
	turnID := s.newTurnID(thread.ID)
	opened, err := s.Rows.OpenTurn(turnCtx, NewTurn{ThreadID: thread.ID, UserID: caller.UserID, TurnID: turnID, AssistantContent: emptyAnswer})
	if err != nil {
		answer.fail("could not write the turn's message", err)
		return
	}
	out, flush, err := r.openStream(response)
	if err != nil {
		answer.fail("could not pace the stream", err)
		return
	}
	handed = true
	s.stream(turnCtx, job{
		turn: turn, built: built, lease: lease, release: release, turnID: turnID, opened: opened, pausedMessage: pausedMessage, pausedContent: pausedContent,
		run: func(ctx context.Context, sink event.Sink) (agent.Result, error) {
			return built.Agent.Resume(ctx, agentThread(thread.ID), decisions, sink)
		},
	}, out, flush, ctx.Context().Done())
}

// checkAgainstWaiting holds a set of decisions to the calls that are
// waiting, before anything is written or run: each waiting call answered
// once, nothing else answered, a question answered as a question, and
// "always allow" only where it would be honoured. The loop checks the same
// when it resumes; checking here turns a wrong request into a 422 that says
// what is wrong instead of a stream that opens and fails.
func checkAgainstWaiting(waiting []agent.Approval, decisions []agent.Decision) *errorenvelope.Failure {
	byID := make(map[string]agent.Approval, len(waiting))
	for _, w := range waiting {
		byID[w.CallID] = w
	}
	seen := make(map[string]bool, len(decisions))
	for _, d := range decisions {
		w, isWaiting := byID[d.CallID]
		problem := ""
		switch {
		case !isWaiting:
			problem = "A decision answers an action that is not waiting"
		case seen[d.CallID]:
			problem = "An action was given two decisions"
		case (w.Question != nil) != (d.Verdict == agent.Answer):
			problem = "A decision is of the wrong kind for the action it answers"
		case d.Verdict == agent.ApproveAlways && !w.AllowAlways:
			problem = "This action asks every time and cannot be always allowed"
		}
		if problem != "" {
			f := failure("decisions", problem)
			return &f
		}
		seen[d.CallID] = true
	}
	if len(decisions) != len(waiting) {
		f := failure("decisions", "Every waiting action needs a decision")
		return &f
	}
	return nil
}

func (r Routes) stop(ctx huma.Context) {
	s := r.Service
	response := humawriter.New(ctx)
	answer := newAnswerer(ctx, response, r.Errors, s.Logger, "a chat turn could not be stopped")
	thread, ok := r.threadFor(answer)
	if !ok {
		return
	}
	running, err := s.Turns.Stop(ctx.Context(), itoa(thread.ID))
	if err != nil {
		answer.fail("could not ask the turn to stop", err)
		return
	}
	if !running {
		writeJSON(response, http.StatusOK, turnState{Status: "idle", ErrorCode: codeNoActiveTurn, Message: idleMessage})
		return
	}
	// 202: the turn has been told and is saving what it had. The page asks
	// for the status shortly after; retry_after_ms says how shortly.
	retry := stopRetry.Milliseconds()
	at := s.Now().Add(stopRetry).UnixMilli()
	writeJSON(response, http.StatusAccepted, turnState{Status: "cancelling", ErrorCode: codeTurnCancelling, Message: cancellingMessage, RetryAfterMS: &retry, RetryAfterAt: &at})
}

func (r Routes) status(ctx huma.Context) {
	s := r.Service
	response := humawriter.New(ctx)
	answer := newAnswerer(ctx, response, r.Errors, s.Logger, "a thread's turn status could not be read")
	thread, ok := r.threadFor(answer)
	if !ok {
		return
	}
	running, err := s.Turns.Running(ctx.Context(), itoa(thread.ID))
	if err != nil {
		answer.fail("could not read whether a turn is running", err)
		return
	}
	state := turnStatus{Status: "idle"}
	if running {
		state.Status = "busy"
	}
	writeJSON(response, http.StatusOK, state)
}

func (r Routes) pending(ctx huma.Context) {
	s := r.Service
	response := humawriter.New(ctx)
	answer := newAnswerer(ctx, response, r.Errors, s.Logger, "a thread's waiting actions could not be read")
	thread, ok := r.threadFor(answer)
	if !ok {
		return
	}
	nothing := pendingAnswer{Pending: []json.RawMessage{}}
	turn, kept, err := (savedTurns{s.Store}).load(ctx.Context(), thread.ID)
	if err != nil {
		answer.fail("could not read the paused turn's choices", err)
		return
	}
	if !kept {
		writeJSON(response, http.StatusOK, nothing)
		return
	}
	turn.UserID = thread.UserID
	built, err := s.Assembler.Build(ctx.Context(), turn)
	if err != nil {
		answer.refuse(err, "could not build the turn's agent")
		return
	}
	defer built.Close()
	waiting, err := built.Agent.Pending(ctx.Context(), agentThread(thread.ID))
	if err != nil {
		answer.fail("could not read what is waiting", err)
		return
	}
	if len(waiting) == 0 {
		writeJSON(response, http.StatusOK, nothing)
		return
	}
	// Each waiting call as the object its interrupt frame carried, built by
	// the function that built the frame, so the card a reloaded page draws
	// is the card the stream drew.
	out := pendingAnswer{Pending: make([]json.RawMessage, 0, len(waiting))}
	for _, w := range waiting {
		data, err := uistream.InterruptData(event.Event{
			Kind: event.ApprovalNeeded, CallID: w.CallID, Tool: w.Tool, Args: w.Args, AllowAlways: w.AllowAlways, Payload: w.Question,
		}, approvalCard)
		if err != nil {
			answer.fail("could not describe a waiting action", err)
			return
		}
		out.Pending = append(out.Pending, data)
	}
	// The cards belong on the paused turn's answer, which is the thread's
	// newest: nothing can have been asked since.
	latest, err := s.Rows.LatestAssistantMessage(ctx.Context(), thread.ID)
	if err != nil {
		answer.fail("could not find the paused turn's message", err)
		return
	}
	if latest != 0 {
		out.AssistantMessageID = &latest
	}
	writeJSON(response, http.StatusOK, out)
}

// threadFor is what the three small routes do first: the caller, the path's
// thread, and whether the caller may do this to it.
func (r Routes) threadFor(answer answerer) (*Thread, bool) {
	caller, signedIn := answer.signedIn()
	if !signedIn {
		return nil, false
	}
	threadID, readable := answer.threadID()
	if !readable {
		return nil, false
	}
	thread, err := r.Service.authorise(answer.ctx.Context(), caller, threadID)
	if err != nil {
		answer.refuse(err, "could not authorise the request")
		return nil, false
	}
	return thread, true
}
