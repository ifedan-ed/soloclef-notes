package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/uistream"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/parts"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/threads"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/turns"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/cite"
)

// Timing is how a turn is paced. Every field is required: each is a
// trade-off a deployment makes (how long a hung model may hold a thread,
// how often an unfinished answer is written to the database), and none has
// a value that is right everywhere.
type Timing struct {
	// MaxTurn is the longest one turn may run, from the request to its
	// end. Time spent waiting for a person's approval is not part of it:
	// a paused turn has ended, and its resumption is a new stretch.
	MaxTurn time.Duration
	// KeepAlive is how long the stream may be silent before a comment line
	// is sent, so that no proxy between here and the reader hangs up while
	// a model thinks.
	KeepAlive time.Duration
	// SaveEvery is how often the answer so far is saved while a turn runs,
	// which is what a page that reloads mid-answer shows.
	SaveEvery time.Duration
	// Title is how long naming a new thread may take.
	Title time.Duration
	// Stall is how long one frame may wait for a reader that has stopped
	// reading before the connection is given up on. The turn goes on.
	Stall time.Duration
}

func (t Timing) check() error {
	if t.MaxTurn <= 0 || t.KeepAlive <= 0 || t.SaveEvery <= 0 || t.Title <= 0 || t.Stall <= 0 {
		return fmt.Errorf("chat: every Timing value must be positive, got %+v", t)
	}
	return nil
}

// Titler names a new thread from its first question, with the model that
// is answering it.
type Titler interface {
	Title(ctx context.Context, chat model.Model, question string) (string, error)
}

// Service is what the routes share: everything a turn needs.
type Service struct {
	Rows Rows
	// Notebooks and Queue are what a notebook chat's turn owes its notebook
	// before it runs: a mentioned document becomes a source, and a
	// notebook that names itself is queued for naming (mentions.go).
	Notebooks NotebookRows
	Queue     Enqueuer
	Turns     *turns.Registry
	Assembler Assembler
	// Store is the loop's checkpoint store, the one the Assembler's agents
	// save to; a turn's choices are kept in it across a pause.
	Store checkpoint.Store
	// Models is the model menu: what an account chose and what it may
	// choose (models.go).
	Models *Models
	// Choices is where the model menu writes an account's choice: the
	// place Models reads it from.
	Choices ModelChoices
	Titler  Titler
	Timing  Timing
	// Threads serves the thread routes (package threads). It is built with
	// this Service because the two share the turn registry and the
	// checkpoint store; cmd/api registers threads.Routes over it. Nil in a
	// Service built for a test of the turn routes alone.
	Threads *threads.Service
	// Parts are the pieces NewService built for the chat that routes outside
	// it also run on (parts.go). The zero value in a Service built by a test.
	Parts Parts
	// Summary says when a long thread is summarised (summary.go).
	Summary SummaryLimits
	Logger  *slog.Logger
	Now     func() time.Time

	// Lifetime is cancelled when the server shuts down. A turn runs under
	// it and not under its request's context: a reader who closes the tab
	// has not asked for the answer to be thrown away, and finds it finished
	// when they come back. Stop is the way to stop a turn.
	Lifetime context.Context

	// running counts turns still finishing, for Wait.
	running sync.WaitGroup
}

// Check reports what is missing. Called once at start-up.
func (s *Service) Check() error {
	switch {
	case s.Rows == nil:
		return errors.New("chat: Rows is not set")
	case s.Notebooks == nil:
		return errors.New("chat: Notebooks is not set")
	case s.Queue == nil:
		return errors.New("chat: Queue is not set")
	case s.Turns == nil:
		return errors.New("chat: Turns is not set")
	case s.Assembler == nil:
		return errors.New("chat: Assembler is not set")
	case s.Store == nil:
		return errors.New("chat: Store is not set")
	case s.Choices == nil:
		return errors.New("chat: the model choices are not set")
	case s.Models == nil:
		return errors.New("chat: Models is not set")
	case s.Titler == nil:
		return errors.New("chat: Titler is not set")
	case s.Logger == nil:
		return errors.New("chat: Logger is not set")
	case s.Now == nil:
		return errors.New("chat: Now is not set")
	case s.Lifetime == nil:
		return errors.New("chat: Lifetime is not set")
	}
	if err := s.Models.Check(); err != nil {
		return err
	}
	if err := s.Summary.check(); err != nil {
		return err
	}
	return s.Timing.check()
}

// Wait blocks until every turn this process is running has finished saving.
// The server calls it after cancelling Lifetime and before closing the
// database pool, so a turn cut short by a deploy still saves what it had.
func (s *Service) Wait() { s.running.Wait() }

// randomHex returns n random bytes as hex, for ids nobody has to guess.
func randomHex(n int) string {
	b := make([]byte, n)
	// crypto/rand.Read does not fail on any platform Go supports.
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// relay is the stream's connection to its reader, which can be let go of.
//
// The turn outlives the request when the reader leaves. From that moment
// nothing may be written to the response (the HTTP server has taken it
// back), so the request handler detaches the relay before it returns, and
// every later write fails harmlessly inside the stream writer, which stops
// writing at its first error.
type relay struct {
	mu    sync.Mutex
	out   io.Writer
	flush func() error
}

var errDetached = errors.New("chat: the reader has gone")

func (r *relay) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.out == nil {
		return 0, errDetached
	}
	return r.out.Write(p)
}

func (r *relay) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.out == nil {
		return errDetached
	}
	return r.flush()
}

func (r *relay) detach() {
	r.mu.Lock()
	r.out, r.flush = nil, nil
	r.mu.Unlock()
}

// job is one turn about to run: everything decided before the first frame.
type job struct {
	turn  Turn
	built Built
	// lease is the thread's hold; release frees the turn's own context.
	// Both are let go when the turn is over, not when its request is.
	lease   *turns.Turn
	release context.CancelFunc
	turnID  string
	opened  OpenedTurn
	// pausedMessage is, for a resumed turn, the message of the stretch that
	// paused: where the cards of the calls being answered are saved.
	// pausedContent is that message as it was read before the resumed
	// stretch began, which is where its step display is carried on from.
	pausedMessage int64
	pausedContent []byte
	// first is true for the first answer of a thread, which names it.
	first bool
	// question is the person's message, for the title; "" on a resume.
	question string
	// run performs the loop's part: Start for a new turn, Resume for a
	// resumed one.
	run func(ctx context.Context, sink event.Sink) (agent.Result, error)
}

// The order of a turn's stream. The apps depend on it:
//
//	start, start-step                         (uistream.Begin)
//	data-turn-info      {chat_turn_id}
//	data-turn-status    {status: busy}
//	data-user-message-id      (a new turn only)
//	data-assistant-message-id
//	data-activity-timing {status: running}
//	… the run's own frames: reasoning, text, tool cards, interrupt requests,
//	  and a data-activity after each tool frame that changed a step …
//	data-activity             (steps the turn's end left unfinished)
//	data-activity-timing {status: completed | paused}
//	data-thread-title-update  (a thread's first answer, if it was named)
//	data-token-usage          (if a model was called)
//	data-turn-status    {status: idle}
//	finish-step, finish, [DONE]   or   error
//
// The ids come before anything the model says because the page renames its
// placeholder messages on them, and everything after is filed under the
// real ids. The status goes idle before the stream closes, and the rows are
// final before either: a page that refetches the thread on "finish" must
// find the finished answer.

// stream runs j's turn, writing its frames to out until the reader leaves
// (gone is closed) and finishing the turn regardless. It returns when the
// turn is over or the reader has gone, whichever is first.
func (s *Service) stream(ctx context.Context, j job, out io.Writer, flush func() error, gone <-chan struct{}) {
	link := &relay{out: out, flush: flush}
	steps := newJournal(s.Now, j.built.Activities)
	if j.pausedContent != nil {
		steps.seed(j.pausedContent)
	}
	writer, err := uistream.New(uistream.Config{
		Out: link, Flush: link.Flush, Now: s.Now, NewID: uistream.RandomID,
		Card: card, Approval: approvalCard, Metadata: steps.metadata,
	})
	if err != nil {
		// Unreachable: every field above is set. Release the thread.
		j.lease.End()
		j.release()
		j.built.Close()
		s.Logger.ErrorContext(ctx, "the stream writer could not be built", "error", err.Error())
		return
	}
	builder := parts.NewBuilder(card, steps.metadata, s.Now, uistream.RandomID)
	builder.LeaveOutArguments(unsavedArguments...)
	usage := &meter{model: j.built.Model}

	writer.Begin("msg_" + randomHex(16))
	writer.Data("turn-info", map[string]string{"chat_turn_id": j.turnID})
	writer.Data("turn-status", map[string]string{"status": "busy"})
	type messageID struct {
		MessageID int64  `json:"message_id"`
		TurnID    string `json:"turn_id"`
	}
	if j.opened.UserMessageID != 0 {
		writer.Data("user-message-id", messageID{j.opened.UserMessageID, j.turnID})
	}
	writer.Data("assistant-message-id", messageID{j.opened.AssistantMessageID, j.turnID})
	writer.Data("activity-timing", steps.timing())

	done := make(chan struct{})
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer close(done)
		s.finish(ctx, j, writer, builder, steps, usage)
	}()

	select {
	case <-done:
	case <-gone:
		// The reader left. The turn goes on; only the writing stops.
		link.detach()
	}
}

// finish runs the loop and everything after it. ctx is the turn's context:
// cancelled by Stop, by the turn's time limit, or by shutdown.
func (s *Service) finish(ctx context.Context, j job, writer *uistream.Writer, builder *parts.Builder, steps *journal, usage *meter) {
	defer j.release()
	defer j.lease.End()
	// What the turn opened (connections to connected apps) is given back
	// before the thread is: the next turn opens its own.
	defer j.built.Close()

	// Three things run beside the loop and end with it.
	beside, stopBeside := context.WithCancel(ctx)
	var helpers sync.WaitGroup
	helpers.Add(2)
	go func() {
		defer helpers.Done()
		_ = writer.KeepAlive(beside, s.Timing.KeepAlive)
	}()
	go func() {
		defer helpers.Done()
		s.saveProgress(beside, j.opened.AssistantMessageID, builder, steps)
	}()
	// The title needs only the question, so it is asked for now and is
	// usually ready long before the answer is.
	titled := make(chan string, 1)
	if j.first && j.question != "" {
		go func() {
			titleCtx, cancel := context.WithTimeout(ctx, s.Timing.Title)
			defer cancel()
			title, err := s.Titler.Title(titleCtx, j.built.Chat, j.question)
			if err != nil {
				// A thread without a generated title keeps its default
				// one. That is a cosmetic loss; say so in the log only.
				s.Logger.WarnContext(ctx, "a thread could not be named", "thread_id", j.turn.ThreadID, "error", err.Error())
				title = ""
			}
			titled <- title
		}()
	} else {
		titled <- ""
	}

	sink := func(ev event.Event) {
		usage.observe(ev)
		builder.Event(ev)
		writer.Event(ev)
		// After the tool frame it describes, so the page has the card
		// before the line that points at it.
		if snapshot, changed := steps.observe(ev); changed {
			writer.Data("activity", snapshot)
		}
	}
	// The run carries its turn's id, which the knowledge base's action log
	// files each change under ("undo this turn"): the agent was assembled
	// before the id existed, and a resumed stretch has an id of its own.
	result, runErr := j.run(assembly.WithTurnID(ctx, j.turnID), sink)

	stopBeside()
	helpers.Wait()

	// The step display is closed before anything is saved: a step still
	// "running" in a saved message would run for ever on a reloaded page.
	stopped := errors.Is(context.Cause(ctx), turns.ErrStopped)
	unfinished, clock := steps.close(stopped, runErr == nil && result.Outcome == agent.Paused)
	for _, snapshot := range unfinished {
		writer.Data("activity", snapshot)
	}
	writer.Data("activity-timing", clock)

	// From here on nothing may be lost to a cancelled context: the turn's
	// own is cancelled when it was stopped or timed out, and those are
	// exactly the turns whose partial answer must still be saved.
	closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()

	waiting := map[string]bool{}
	var registry cite.Lookup = cite.FromSources(result.Citations)
	if runErr != nil {
		// A failed run returns no result; the sources numbered so far are
		// in the thread's saved state.
		if state, _, err := j.built.Agent.Load(closing, agentThread(j.turn.ThreadID)); err == nil {
			registry = cite.FromSources(state.Citations)
		}
	} else if result.Outcome == agent.Paused {
		pending, err := j.built.Agent.Pending(closing, agentThread(j.turn.ThreadID))
		if err != nil {
			s.Logger.ErrorContext(closing, "a paused turn's waiting calls could not be read", "thread_id", j.turn.ThreadID, "error", err.Error())
		}
		for _, p := range pending {
			// A call waiting inside a specialist keeps the specialist's own
			// call waiting too: "d1/w1" is waiting, and so is "d1".
			for id := p.CallID; id != ""; {
				waiting[id] = true
				cut := strings.LastIndexByte(id, '/')
				if cut < 0 {
					break
				}
				id = id[:cut]
			}
		}
	}
	s.fillPausedCards(closing, j, builder, steps)

	frame, metered := usage.frame()
	closed := ClosedTurn{
		ThreadID: j.turn.ThreadID, MessageID: j.opened.AssistantMessageID, UserID: j.turn.UserID,
	}
	content, err := answerContent(builder.Finish(waiting, func(text string) string { return cite.Rewrite(text, registry) }), steps)
	if err == nil && metered {
		closed.Usage, err = frame.row()
	}
	if err == nil {
		closed.Content = content
		err = s.Rows.CloseTurn(closing, closed)
	}
	if err != nil {
		// The answer streamed but is not saved: the worst outcome there
		// is, because a reload loses it. It is the stream's error too.
		s.Logger.ErrorContext(closing, "a turn's answer could not be saved", "thread_id", j.turn.ThreadID, "message_id", j.opened.AssistantMessageID, "error", err.Error())
		if runErr == nil {
			runErr = fmt.Errorf("saving the answer: %w", err)
		}
	}

	// The title is written whether or not the answer succeeded: the thread
	// exists and has a first question either way. A paused first turn is
	// named when it is paused, since the resumed turn is not "first".
	if title := <-titled; title != "" {
		if err := s.Rows.SetTitle(closing, j.turn.UserID, j.turn.ThreadID, title); err != nil {
			s.Logger.WarnContext(closing, "a thread's title could not be saved", "thread_id", j.turn.ThreadID, "error", err.Error())
		} else {
			writer.Data("thread-title-update", struct {
				ThreadID int64  `json:"threadId"`
				Title    string `json:"title"`
			}{j.turn.ThreadID, title})
		}
	}
	if metered {
		writer.Data("token-usage", frame)
	}
	writer.Data("turn-status", map[string]string{"status": "idle"})

	cause := context.Cause(ctx)
	var refused *model.Error
	switch {
	case runErr == nil:
		writer.End()
	case stopped:
		// The person pressed Stop. That is not an error to show them: the
		// stream ends as a finished one, with what there was.
		writer.End()
	case errors.Is(runErr, agent.ErrStepLimit):
		writer.Fail(stepLimitMessage, codeStepLimit)
	case errors.Is(cause, context.DeadlineExceeded) || errors.Is(runErr, context.DeadlineExceeded):
		writer.Fail(timeoutMessage, codeTimeout)
	case errors.As(runErr, &refused) && refused.Status == http.StatusNotFound:
		// The proxy does not know the model: it was removed after this
		// process last read the list (Catalogue). The person is told which
		// model, as they would have been before the stream opened.
		s.Logger.WarnContext(closing, "a chat turn's model is not on the proxy", "thread_id", j.turn.ThreadID, "model", j.built.Model, "error", runErr.Error())
		writer.Fail(modelRemoved(j.built.Model), codeModelNotFound)
	default:
		// The cause can name hosts, tables and keys; it goes to the log
		// and the reader gets a sentence.
		s.Logger.ErrorContext(closing, "a chat turn failed", "thread_id", j.turn.ThreadID, "turn_id", j.turnID, "error", runErr.Error())
		writer.Fail(failedMessage, codeServerError)
	}
}

// fillPausedCards writes the results of calls made before a pause onto the
// cards saved with the message that paused. A failure here costs a card its
// result on reload and nothing else, so it is logged and the turn goes on.
func (s *Service) fillPausedCards(ctx context.Context, j job, builder *parts.Builder, steps *journal) {
	if j.pausedMessage == 0 {
		return
	}
	late := builder.Late()
	content, err := s.Rows.MessageContent(ctx, j.turn.ThreadID, j.pausedMessage)
	if err == nil && content != nil {
		filled, cards := content, false
		if len(late) > 0 {
			filled, cards, err = parts.Fill(content, late)
		}
		// The step display beside the cards: a call that was "waiting for
		// approval" in the saved message has now run or been refused.
		lines := false
		if err == nil {
			filled, lines, err = steps.fillActivities(filled)
		}
		if err == nil && (cards || lines) {
			err = s.Rows.StoreContent(ctx, j.pausedMessage, filled)
		}
	}
	if err != nil {
		s.Logger.WarnContext(ctx, "an answered call's result could not be saved on its card", "thread_id", j.turn.ThreadID, "message_id", j.pausedMessage, "error", err.Error())
	}
}

// answerContent is an answer's saved content: the step display, then the
// parts. The page reads the step display from the head of the list.
func answerContent(answer []parts.Part, steps *journal) ([]byte, error) {
	list := make([]any, 0, len(answer)+1)
	list = append(list, steps.part())
	for _, part := range answer {
		list = append(list, part)
	}
	return parts.Encode(list)
}

// closeTimeout bounds the saving a turn does after its run has ended.
const closeTimeout = 30 * time.Second

// saveProgress writes the answer so far every SaveEvery while it changes,
// so a page that reloads in the middle of an answer shows it up to a few
// seconds ago instead of an empty bubble.
func (s *Service) saveProgress(ctx context.Context, messageID int64, builder *parts.Builder, steps *journal) {
	ticker := time.NewTicker(s.Timing.SaveEvery)
	defer ticker.Stop()
	var saved uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		version := builder.Version() + steps.changes()
		if version == saved {
			continue
		}
		answer := builder.Snapshot()
		if len(answer) == 0 {
			// Nothing has streamed: the row's empty text part stays.
			continue
		}
		content, err := answerContent(answer, steps)
		if err != nil {
			continue
		}
		if err := s.Rows.StoreContent(ctx, messageID, content); err != nil {
			if ctx.Err() == nil {
				s.Logger.WarnContext(ctx, "an answer's progress could not be saved", "message_id", messageID, "error", err.Error())
			}
			continue
		}
		saved = version
	}
}

// emptyAnswer is an answer's content before anything has streamed.
var emptyAnswer = []byte(`[{"type": "text", "text": ""}]`)

// newTurnID is the id a turn's rows and frames share: the thread and the
// millisecond, the form the apps and the saved rows have always had.
func (s *Service) newTurnID(threadID int64) string {
	return itoa(threadID) + ":" + itoa(s.Now().UnixMilli())
}

// ModelTitler names a thread by asking the turn's own model for a few
// words. It holds no model of its own: a title written by some other model
// than the account chose would be a second model nobody picked.
type ModelTitler struct{}

const titlePrompt = `Write a title of at most six words for a conversation that begins with the message below. Answer with the title only: no quotes, no full stop, in the language of the message.

Message:
`

// maxTitleChars is the longest title kept; the column holds 500, the
// sidebar shows about forty.
const maxTitleChars = 100

func (ModelTitler) Title(ctx context.Context, chat model.Model, question string) (string, error) {
	// The start of a long message says what it is about; the rest only
	// costs tokens.
	if runes := []rune(question); len(runes) > 500 {
		question = string(runes[:500])
	}
	response, err := chat.Generate(ctx, model.Request{Messages: []model.Message{model.User(titlePrompt + question)}}, func(model.Chunk) {})
	if err != nil {
		return "", err
	}
	title := strings.Join(strings.Fields(response.Message.Content), " ")
	title = strings.Trim(title, "\"'“”‘’`.")
	if title == "" || len([]rune(title)) > maxTitleChars {
		// A model that answered a paragraph did not answer a title.
		return "", errors.New("the model did not answer a usable title")
	}
	return title, nil
}
