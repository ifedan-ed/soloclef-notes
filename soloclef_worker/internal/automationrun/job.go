// Package automationrun runs one automation run: `automations.execute_run`.
//
// A run is one headless turn of the chat's agent per step of its plan. The
// agent is built by the assembly the chat itself is built by
// (soloclef_assistant/assembly), for the automation's owner: the same model
// choice, the same prompt, the same document search, web and academic
// sources, page reader, connected apps, mail and Drive tools, and the
// artifact specialist. One thing a chat turn has is deliberately not given
// to a run: it cannot rewrite the owner's memory.
//
// # What a step may ask for
//
// A step's own options, each honoured here or refused when the automation
// was saved (soloclef_automations/definition):
//
//   - a model: the step's, else the automation's, else the account's own
//     chat model. A name the proxy no longer offers fails the step with a
//     sentence saying which; no other model answers instead.
//   - a notebook to read (notebook_id): the step is confined to that
//     notebook's enabled sources, as the notebook's own chat is.
//   - a notebook to save to (save_to_notebook_id): the step's answer is
//     filed there as a note when the step ends with one.
//   - connected accounts named with @: the step is told to use them.
//
// A notebook or a connected account that was the owner's when the
// automation was saved and is gone when it runs is said in the step's
// result, never quietly replaced by "search everything".
//
// # A thread of its own
//
// What the specialist makes is made under a thread: its sandbox and an
// artifact's attribution are keyed by one. So a run is given a hidden
// thread when it is claimed (Store.Claim), and everything every step of
// the run makes belongs to it.
//
// # Mailing the result
//
// A run whose automation asks for its result to be emailed ends with
// output.email.status "pending". This tier sends no mail: the API tier
// holds the SMTP client, finds the pending run and sends it
// (soloclef_api/internal/automationmail).
//
// # Nobody is present
//
// A chat turn that reaches a tool needing approval stops and shows a card.
// A run has nobody to show it to, so:
//
//   - A tool that asks first runs only if the step says so itself
//     (auto_approve_all, the automation's own trust rule). The account's
//     standing "always allow" answers from chat are not consulted.
//   - A tool that must always ask (the page reader) is never run. The agent
//     loop does not consult trust for one, and this package refuses it.
//   - Each refusal is told to the model, so it finishes with what it has,
//     and is written into the run with the tool's name. A run that was
//     refused something ends as needs_attention, not succeeded.
//
// # What the row holds
//
// The run's row is its whole record and its only outcome: each step's
// transcript (what the model said, called and was answered), the final
// message, the tokens used and the model that answered. A run's status is
// written only over a run that is still running, so a run cancelled or
// timed out from elsewhere keeps that ending.
//
// # At most one worker runs a run
//
// A message names a run and nothing else. The run is claimed in one
// transaction under its automation's row lock (Store.Claim): pending to
// running, once. A duplicate or redelivered message finds it claimed and is
// acknowledged with nothing done. A worker that dies mid-run leaves a
// running row no message will finish; the schedule tick ends such a run as
// timed out once its own time limit has passed (automationtriggers).
package automationrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/cite"
	"git.danvics.com/danvics/soloclef/soloclef_automations/definition"
	"git.danvics.com/danvics/soloclef/soloclef_automations/runqueue"
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// Task is the message this job reads.
const Task = runqueue.ExecuteTask

// The statuses a run ends with (the automation_run_status enum).
const (
	Succeeded      = "succeeded"
	Failed         = "failed"
	NeedsAttention = "needs_attention"
	TimedOut       = "timed_out"
	Cancelled      = "cancelled"
)

// Claim is what happened when a worker asked for a run.
type Claim uint8

const (
	// Claimed: the run is this worker's and is now running.
	Claimed Claim = iota + 1
	// Gone: the run or its automation no longer exists.
	Gone
	// Taken: the run is not pending, so another message ran or is running
	// it, or it was cancelled.
	Taken
	// Waiting: the automation runs one at a time and it is not this run's
	// turn. It stays pending; the run ahead of it hands over when it ends.
	Waiting
	// Stopped: the automation was paused before the run began; the run was
	// cancelled by the claim.
	Stopped
)

// Run is a claimed run.
type Run struct {
	ID           int64
	AutomationID int64
	// UserID is the automation's owner: the account the run acts as.
	UserID string
	Name   string
	// Snapshot is the definition as it was when the run was created.
	Snapshot []byte
	// Inputs is what the trigger knew about this fire, a JSON object.
	Inputs []byte
	// ThreadID is the run's own hidden thread, made when it was claimed:
	// what the run makes is made under it.
	ThreadID int64
}

// Connection is one of the account's connected accounts, as a task names
// it with @.
type Connection struct {
	ID   int64
	Name string
	// Kind is the connection's type, in the database's spelling.
	Kind    string
	Enabled bool
}

// Note is a step's answer to be filed as a note.
type Note struct {
	UserID       string
	AutomationID int64
	NotebookID   int64
	Title        string
	Markdown     string
}

// Rows is the run's row (Store).
type Rows interface {
	Claim(ctx context.Context, runID int64) (Run, Claim, error)
	// Status is the run's status now, or "" when its row is gone.
	Status(ctx context.Context, runID int64) (string, error)
	// Finish writes the ending over a run that is still running and reports
	// whether it was: false means the run was ended from elsewhere first.
	Finish(ctx context.Context, runID int64, ending Ending) (bool, error)
	// NextWaiting is the oldest pending run of the automation, if any.
	NextWaiting(ctx context.Context, automationID int64) (int64, bool, error)
	// Notebook is the title of one of the account's notebooks; found is
	// false when it is not the account's or is not there.
	Notebook(ctx context.Context, userID string, notebookID int64) (title string, found bool, err error)
	// Connections is which of the ids are the account's connected
	// accounts.
	Connections(ctx context.Context, userID string, ids []int64) ([]Connection, error)
	// SaveNote files a note in one of the account's notebooks and answers
	// its document's id.
	SaveNote(ctx context.Context, note Note) (int64, error)
}

// Indexer queues a document for indexing (indexrequest.Publisher): a note
// a step saved is searchable once it has been indexed.
type Indexer interface {
	Index(ctx context.Context, documentID int64) error
}

// Ending is what a finished run's row is given.
type Ending struct {
	Status string
	// Steps is step_results; Output and Error are JSON objects or nil.
	Steps  []StepResult
	Output map[string]any
	Error  map[string]any
}

// Sender publishes the next waiting run (produce.Producer).
type Sender interface {
	Send(ctx context.Context, message produce.Message) (string, error)
}

// Timing is how the job paces itself: decisions about the job, made in
// cmd/worker with their reasons.
type Timing struct {
	// StatusInterval is how often a running run asks whether it was
	// cancelled.
	StatusInterval time.Duration
	// RetryStep is the unit a failed step's retry waits by: once for
	// linear back-off, doubled each time for exponential.
	RetryStep time.Duration
	// MaxPauses bounds how many times one step may stop for approvals
	// before the run gives up on a model that keeps asking.
	MaxPauses int
	// ResultChars bounds one tool result in the saved transcript.
	ResultChars int
}

// Job runs the task.
type Job struct {
	Rows Rows
	// Assembler builds a step's agent: the chat's own assembly, given this
	// tier's rows.
	Assembler assembly.Assembler
	// Store is where the loop keeps a step's conversation while it runs.
	// It is the store the Assembler's agents save to.
	Store     checkpoint.Store
	Publisher Sender
	Indexer   Indexer
	Timing    Timing
	Log       *slog.Logger
	Now       func() time.Time
}

// Check reports what is missing. Called once at start-up.
func (j *Job) Check() error {
	switch {
	case j.Rows == nil || j.Assembler == nil || j.Store == nil || j.Publisher == nil || j.Indexer == nil || j.Log == nil || j.Now == nil:
		return errors.New("automationrun: a dependency is missing")
	case j.Timing.StatusInterval <= 0 || j.Timing.RetryStep <= 0 || j.Timing.MaxPauses < 1 || j.Timing.ResultChars < 1:
		return errors.New("automationrun: the timing is incomplete")
	}
	return nil
}

// closingWrite bounds the writes that end a run, which use a context of
// their own: the run's is often the thing that just ended.
const closingWrite = 30 * time.Second

// Run is the jobs.Handler.
func (j *Job) Run(ctx context.Context, msg *celery.Message) error {
	runID, err := runIDOf(msg)
	if err != nil {
		return err
	}
	run, claim, err := j.Rows.Claim(ctx, runID)
	if err != nil {
		return fmt.Errorf("automationrun: claiming run %d: %w", runID, err)
	}
	if claim != Claimed {
		// Nothing to do, and nothing wrong: see Claim.
		j.Log.Info("an automation run was not started", "run", runID, "reason", claim.String())
		return nil
	}
	ending := j.execute(ctx, run)

	closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), closingWrite)
	defer cancel()
	written, err := j.Rows.Finish(closing, run.ID, ending)
	if err != nil {
		return fmt.Errorf("automationrun: writing run %d's ending: %w", run.ID, err)
	}
	j.Log.Info("an automation run ended", "run", run.ID, "automation", run.AutomationID, "status", ending.Status, "kept", written)
	// The run behind this one, for an automation that queues: started now
	// rather than at the next tick.
	if next, waiting, err := j.Rows.NextWaiting(closing, run.AutomationID); err != nil {
		j.Log.Warn("the next waiting run was not looked for", "automation", run.AutomationID, "error", err.Error())
	} else if waiting {
		if _, err := j.Publisher.Send(closing, runqueue.Execute(next, 0)); err != nil {
			j.Log.Warn("the next waiting run was not published; the tick will publish it", "run", next, "error", err.Error())
		}
	}
	return nil
}

func (c Claim) String() string {
	switch c {
	case Claimed:
		return "claimed"
	case Gone:
		return "the run is gone"
	case Taken:
		return "the run is not pending"
	case Waiting:
		return "another run of the automation is ahead of it"
	case Stopped:
		return "the automation is not active"
	}
	return "unknown"
}

// runIDOf reads the message's one argument.
func runIDOf(msg *celery.Message) (int64, error) {
	raw, found := msg.Kwargs[runqueue.RunKey]
	number, isNumber := raw.(float64)
	if !found || len(msg.Args) != 0 || len(msg.Kwargs) != 1 || !isNumber || number < 1 || number != math.Trunc(number) || number > math.MaxInt64/2 {
		return 0, fmt.Errorf("automationrun: %s takes one keyword argument, %s; got %v and %v", Task, runqueue.RunKey, msg.Args, msg.Kwargs)
	}
	return int64(number), nil
}

// execute runs the plan and says how the run ended. It never returns an
// error: every way a run can go wrong is an ending the person reads.
func (j *Job) execute(ctx context.Context, run Run) Ending {
	parsed, problems := definition.Parse(run.Snapshot)
	if len(problems) > 0 {
		// Saved before a rule existed, or written some other way. Said, not
		// run in part.
		return Ending{Status: Failed, Steps: []StepResult{}, Error: failure("invalid_definition",
			"This automation's definition cannot be run: "+strings.Join(problems, "; ")+". Edit and save it again.")}
	}
	limit := time.Duration(parsed.Execution.TimeoutSeconds) * time.Second
	working, stop := context.WithTimeout(ctx, limit)
	defer stop()
	// Cancelled from the page, paused, or deleted: the row says so, and the
	// run stops within one interval of it.
	var cancelled bool
	var watching sync.WaitGroup
	watching.Add(1)
	go func() {
		defer watching.Done()
		ticker := time.NewTicker(j.Timing.StatusInterval)
		defer ticker.Stop()
		for {
			select {
			case <-working.Done():
				return
			case <-ticker.C:
				status, err := j.Rows.Status(working, run.ID)
				if err == nil && status != "running" {
					cancelled = true
					stop()
					return
				}
				// A missed look is not a reason to stop the run; the next
				// one asks again.
			}
		}
	}()

	ending := Ending{Status: Succeeded, Steps: make([]StepResult, 0, len(parsed.Plan))}
	usage, final, modelName := model.Usage{}, "", ""
	for index, step := range parsed.Plan {
		result := j.step(working, run, parsed, index, step)
		ending.Steps = append(ending.Steps, result)
		usage.PromptTokens += result.usage.PromptTokens
		usage.CompletionTokens += result.usage.CompletionTokens
		usage.TotalTokens += result.usage.TotalTokens
		if result.model != "" {
			modelName = result.model
		}
		if result.Status == Succeeded || result.Status == NeedsAttention {
			final = result.Result.FinalMessage
		}
		if result.Status == NeedsAttention && ending.Status == Succeeded {
			ending.Status = NeedsAttention
			// Why it needs attention is the step's own sentence; its kind
			// says whether an approval was refused or something else was
			// left undone (an answer that could not be filed).
			kind := "needs_attention"
			if len(result.Result.Refused) > 0 {
				kind = "approval_refused"
			}
			ending.Error = failure(kind, *result.Result.Outcome.Reason)
		}
		if result.Status == Failed {
			ending.Status, ending.Error = Failed, map[string]any{"type": result.Error.Type, "message": result.Error.Message, "step_id": step.StepID}
			break
		}
	}
	stop()
	watching.Wait()
	switch {
	case cancelled:
		// The row already says how it ended; Finish will not overwrite it.
		ending.Status, ending.Error = Cancelled, failure("cancelled", "The run was cancelled.")
	case working.Err() == context.DeadlineExceeded:
		ending.Status, ending.Error = TimedOut, failure("timed_out", "The run reached its time limit of "+limit.String()+" and was stopped.")
	case ctx.Err() != nil:
		// The worker is stopping. The run did not fail by itself, and
		// saying so lets the person run it again knowingly.
		ending.Status, ending.Error = Failed, failure("worker_stopped", "The worker running this was stopped. Run the automation again.")
	}
	ending.Output = map[string]any{
		"final_message": final, "model": modelName,
		"usage": map[string]int{"prompt_tokens": usage.PromptTokens, "completion_tokens": usage.CompletionTokens, "total_tokens": usage.TotalTokens},
	}
	// The result is mailed for every ending the owner did not choose: a
	// run they cancelled is not news to them. "pending" is the whole of
	// this tier's part; the API tier sends it and writes what became of it.
	if parsed.Execution.EmailResult && !cancelled {
		ending.Output["email"] = map[string]any{"status": EmailPending}
	}
	return ending
}

// EmailPending is output.email.status for a result waiting to be sent. It
// is the word the API tier's sender looks for
// (soloclef_api/internal/automationmail) and the schema's partial index
// is built on.
const EmailPending = "pending"

func failure(kind, message string) map[string]any {
	return map[string]any{"type": kind, "message": message}
}

// StepResult is one entry of step_results, in the shape the run page reads
// (the web's RunStepResult).
type StepResult struct {
	StepID     string      `json:"step_id"`
	Action     string      `json:"action"`
	Status     string      `json:"status"`
	StartedAt  time.Time   `json:"started_at"`
	FinishedAt time.Time   `json:"finished_at"`
	Attempts   int         `json:"attempts"`
	Result     *StepOutput `json:"result,omitempty"`
	Error      *StepError  `json:"error,omitempty"`

	usage model.Usage
	model string
}

// StepOutput is what a step produced.
type StepOutput struct {
	FinalMessage   string      `json:"final_message"`
	RetrievalScope string      `json:"retrieval_scope"`
	Outcome        StepOutcome `json:"outcome"`
	// Refused are the tool calls nobody was present to approve.
	Refused []Refusal `json:"refused"`
	// Transcript is the step's conversation after the task: what the model
	// said, the tools it called and what each answered.
	Transcript []Entry `json:"transcript"`
	// SavedNote is the note the answer was filed as, when the step asked
	// for that and it was done.
	SavedNote *SavedNote `json:"saved_note,omitempty"`
	Model     string     `json:"model"`
	Usage     struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// SavedNote names the note a step's answer was filed as.
type SavedNote struct {
	DocumentID int64  `json:"document_id"`
	NotebookID int64  `json:"notebook_id"`
	Title      string `json:"title"`
}

// StepOutcome is the step's verdict, with the reason when it is not plain
// success.
type StepOutcome struct {
	Status string  `json:"status"`
	Reason *string `json:"reason"`
}

// Refusal is one tool call that was not run.
type Refusal struct {
	Tool   string `json:"tool"`
	Reason string `json:"reason"`
}

// StepError is why a step failed.
type StepError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Entry is one message of a transcript.
type Entry struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
	// Calls are the tools an assistant message asked for.
	Calls []EntryCall `json:"tool_calls,omitempty"`
	// Tool names the tool a tool message answers for.
	Tool string `json:"tool,omitempty"`
}

// EntryCall is one tool call in a transcript.
type EntryCall struct {
	Name string `json:"name"`
	Args string `json:"args"`
}

// step runs one step, trying again as the definition allows.
func (j *Job) step(ctx context.Context, run Run, parsed definition.Definition, index int, step definition.Step) StepResult {
	result := StepResult{StepID: step.StepID, Action: step.Action, StartedAt: j.Now().UTC()}
	task, problems := definition.ParseTask(step.Params)
	if len(problems) > 0 {
		result.Status, result.Error = Failed, &StepError{Type: "invalid_step", Message: strings.Join(problems, "; ")}
		result.FinishedAt = j.Now().UTC()
		return result
	}
	retries := parsed.Execution.MaxRetries
	if step.MaxRetries != nil {
		retries = *step.MaxRetries
	}
	working := ctx
	if step.TimeoutSeconds != nil {
		var stop context.CancelFunc
		working, stop = context.WithTimeout(ctx, time.Duration(*step.TimeoutSeconds)*time.Second)
		defer stop()
	}
	for attempt := 1; ; attempt++ {
		result.Attempts = attempt
		output, err := j.turn(working, run, parsed, index, attempt, task)
		if err == nil {
			result.Result, result.Status, result.Error = output, output.Outcome.Status, nil
			result.usage = model.Usage{PromptTokens: output.Usage.PromptTokens, CompletionTokens: output.Usage.CompletionTokens, TotalTokens: output.Usage.TotalTokens}
			result.model = output.Model
			break
		}
		result.Status, result.Error = Failed, describe(err)
		// A run that was stopped, or a failure that is the account's to fix
		// (its model is gone, a notebook the step names is gone), is not
		// helped by trying again.
		var unanswerable *assembly.ModelError
		var missing *missingError
		if working.Err() != nil || attempt > retries || errors.As(err, &unanswerable) || errors.As(err, &missing) {
			break
		}
		wait := backoff(parsed.Execution.RetryBackoff, attempt, j.Timing.RetryStep)
		select {
		case <-working.Done():
		case <-time.After(wait):
		}
	}
	result.FinishedAt = j.Now().UTC()
	return result
}

// backoff is the wait before the attempt after `attempt`.
func backoff(kind string, attempt int, step time.Duration) time.Duration {
	switch kind {
	case "none":
		return 0
	case "linear":
		return time.Duration(attempt) * step
	}
	// Exponential, bounded so a long retry budget does not wait for hours.
	return step << min(attempt-1, 6)
}

// describe is a step's failure in words for the run page. The cause is the
// account's own run of its own model on its own documents, so it is shown.
func describe(err error) *StepError {
	var unanswerable *assembly.ModelError
	var missing *missingError
	switch {
	case errors.As(err, &unanswerable):
		return &StepError{Type: "model_unavailable", Message: unanswerable.Message}
	case errors.As(err, &missing):
		return &StepError{Type: "invalid_step", Message: missing.message}
	case errors.Is(err, agent.ErrStepLimit):
		return &StepError{Type: "step_limit", Message: "The task needed more steps than one run allows, so it was stopped."}
	case errors.Is(err, context.DeadlineExceeded):
		return &StepError{Type: "timed_out", Message: "The step reached its time limit and was stopped."}
	case errors.Is(err, context.Canceled):
		return &StepError{Type: "stopped", Message: "The step was stopped before it finished."}
	case errors.Is(err, errQuestion):
		return &StepError{Type: "question", Message: err.Error()}
	}
	return &StepError{Type: "error", Message: err.Error()}
}

// missingError is a step that names something that was the owner's when
// the automation was saved and no longer is. It is the owner's to fix by
// editing the automation, so it is said and not retried.
type missingError struct{ message string }

func (e *missingError) Error() string { return e.message }

// errQuestion ends a step whose tool asked the person something.
var errQuestion = errors.New("a tool asked a question, and an automation runs with nobody present to answer it")

// preface tells the model what kind of turn this is. It goes before the
// task in the user message and not in the system prompt, which is the
// chat's own and is built by the assembly for both.
const preface = `<automation>
This is a scheduled automation running with nobody present. Nobody will read your work as you go, answer a question or approve an action, so do not ask: do the task with what you have and finish with the result itself. If a tool was refused because it needs approval, say in your answer what you could not do and why.
</automation>

`

// inert makes a name safe inside one of the message's blocks.
func inert(text string) string {
	return strings.NewReplacer("<", "‹", ">", "›").Replace(strings.Join(strings.Fields(text), " "))
}

// named is the block that tells the model which connected accounts the
// task names with @. Each is named as its owner named it. One that is
// switched off or gone is said, so the answer says so too instead of
// being made from somewhere else.
func named(wanted []int64, found []Connection) string {
	if len(wanted) == 0 {
		return ""
	}
	byID := map[int64]Connection{}
	for _, connection := range found {
		byID[connection.ID] = connection
	}
	var b strings.Builder
	b.WriteString("<connected_accounts>\nThe task names these connected accounts of the user. Use their tools for it:\n")
	for _, id := range wanted {
		connection, known := byID[id]
		switch {
		case !known:
			b.WriteString("- an account that is no longer connected: say in your answer that it could not be used\n")
		case !connection.Enabled:
			b.WriteString("- " + inert(connection.Name) + ", which the user has switched off: say in your answer that it could not be used\n")
		default:
			b.WriteString("- " + inert(connection.Name) + "\n")
		}
	}
	b.WriteString("</connected_accounts>\n\n")
	return b.String()
}

// message is the user message a step's turn starts with.
func message(run Run, task definition.Task, extra string) string {
	var b strings.Builder
	b.WriteString(preface)
	b.WriteString(extra)
	b.WriteString(task.Query)
	// What the trigger knew (which document arrived, when the schedule
	// fired) is given as data after the task. The task's own text is never
	// rewritten with it, so nothing in a document's title can change what
	// the task says.
	if inputs := strings.TrimSpace(string(run.Inputs)); inputs != "" && inputs != "{}" {
		b.WriteString("\n\n<trigger>\nWhat started this run, as data:\n" + strings.NewReplacer("<", "‹", ">", "›").Replace(inputs) + "\n</trigger>")
	}
	return b.String()
}

// turn runs one attempt of a step: build the agent, run it, refuse what
// nobody can approve, and read back what happened.
func (j *Job) turn(ctx context.Context, run Run, parsed definition.Definition, index, attempt int, task definition.Task) (*StepOutput, error) {
	// What the step names is looked up again now: it was the owner's when
	// the automation was saved, and may not be any more.
	unattended := &assembly.Unattended{TrustAskFirst: task.AutoApproveAll, Model: parsed.ChatModel(task)}
	for _, notebook := range []struct {
		id   *int64
		what string
	}{{task.NotebookID, "reads from"}, {task.SaveToNotebookID, "saves its answer to"}} {
		if notebook.id == nil {
			continue
		}
		if _, found, err := j.Rows.Notebook(ctx, run.UserID, *notebook.id); err != nil {
			return nil, fmt.Errorf("reading the step's notebook: %w", err)
		} else if !found {
			return nil, &missingError{"The notebook this step " + notebook.what + " no longer exists. Edit the automation and choose another."}
		}
	}
	if task.NotebookID != nil {
		unattended.NotebookID = *task.NotebookID
	}
	accounts := ""
	if len(task.MentionedConnectorIDs) > 0 {
		found, err := j.Rows.Connections(ctx, run.UserID, task.MentionedConnectorIDs)
		if err != nil {
			return nil, fmt.Errorf("reading the step's connected accounts: %w", err)
		}
		accounts = named(task.MentionedConnectorIDs, found)
	}
	built, err := j.Assembler.Build(ctx, assembly.Turn{
		// The run's own thread: what the specialist makes is made under it.
		ThreadID: run.ThreadID,
		UserID:   run.UserID, Scope: task.Scope(),
		DocumentIDs: task.MentionedDocumentIDs, FolderIDs: task.MentionedFolderIDs,
		Unattended: unattended,
	})
	if err != nil {
		return nil, err
	}
	defer built.Close()
	// The conversation is this attempt's own and is removed when it ends:
	// nothing resumes a run, and the transcript is kept in the run's row.
	thread := "automation-run-" + strconv.FormatInt(run.ID, 10) + "-" + strconv.Itoa(index) + "-" + strconv.Itoa(attempt)
	defer func() {
		closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), closingWrite)
		defer cancel()
		if err := j.Store.DeleteTree(closing, thread); err != nil {
			j.Log.Warn("an automation run's conversation was not removed", "run", run.ID, "error", err.Error())
		}
	}()

	var mu sync.Mutex
	var usage model.Usage
	sink := func(ev event.Event) {
		// Every model call reports what it used; a step's cost is their sum.
		mu.Lock()
		usage.PromptTokens += ev.Usage.PromptTokens
		usage.CompletionTokens += ev.Usage.CompletionTokens
		usage.TotalTokens += ev.Usage.TotalTokens
		mu.Unlock()
	}
	output := &StepOutput{RetrievalScope: task.Scope(), Model: built.Model, Refused: []Refusal{}, Transcript: []Entry{}}
	result, err := built.Agent.Start(ctx, thread, []model.Message{model.User(message(run, task, built.Preface+accounts))}, sink)
	for pauses := 0; err == nil && result.Outcome == agent.Paused; pauses++ {
		if pauses == j.Timing.MaxPauses {
			err = errors.New("the run kept asking for approvals nobody is present to give")
			break
		}
		var waiting []agent.Approval
		if waiting, err = built.Agent.Pending(ctx, thread); err != nil {
			break
		}
		decisions := make([]agent.Decision, 0, len(waiting))
		for _, call := range waiting {
			if call.Question != nil {
				err = errQuestion
				break
			}
			reason := refusalReason(call, task.AutoApproveAll)
			output.Refused = append(output.Refused, Refusal{Tool: call.Tool, Reason: reason})
			decisions = append(decisions, agent.Decision{CallID: call.CallID, Verdict: agent.Reject, Reason: reason})
		}
		if err == nil {
			result, err = built.Agent.Resume(ctx, thread, decisions, sink)
		}
	}
	if err != nil {
		return nil, err
	}
	// The labels the model wrote, "[3]", become the markers the pages draw
	// as source chips, from the sources this step's tools numbered. A
	// label that names no source was invented and is removed.
	output.FinalMessage = cite.Rewrite(result.Answer, cite.FromSources(result.Citations))
	output.Usage.PromptTokens, output.Usage.CompletionTokens, output.Usage.TotalTokens = usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens
	output.Outcome = StepOutcome{Status: Succeeded}
	if len(output.Refused) > 0 {
		names := make([]string, 0, len(output.Refused))
		for _, refused := range output.Refused {
			names = append(names, refused.Tool)
		}
		reason := "Nobody was present to approve: " + strings.Join(names, ", ") + ". The run finished without " + plural(len(names)) + "."
		output.Outcome = StepOutcome{Status: NeedsAttention, Reason: &reason}
	}
	state, _, err := built.Agent.Load(ctx, thread)
	if err != nil {
		return nil, fmt.Errorf("reading the step's conversation: %w", err)
	}
	output.Transcript = transcript(state.Messages, j.Timing.ResultChars)
	if task.SaveToNotebookID != nil {
		j.file(ctx, run, *task.SaveToNotebookID, output)
	}
	return output, nil
}

// maxNoteTitleChars bounds a saved note's title.
const maxNoteTitleChars = 200

// file saves a step's answer as a note in the notebook the step named,
// and queues it for indexing. A step with no answer saves nothing. An
// answer that could not be saved does not fail the step, whose work is
// done and would only be done again: the step ends as needing attention,
// with the reason, and its answer is still in the run.
func (j *Job) file(ctx context.Context, run Run, notebookID int64, output *StepOutput) {
	if strings.TrimSpace(output.FinalMessage) == "" {
		return
	}
	title := clipped(run.Name+", "+j.Now().UTC().Format("2 January 2006"), maxNoteTitleChars)
	// The write is not the run's to cancel half-way: a run stopped at this
	// moment still keeps the answer it has.
	saving, cancel := context.WithTimeout(context.WithoutCancel(ctx), closingWrite)
	defer cancel()
	id, err := j.Rows.SaveNote(saving, Note{UserID: run.UserID, AutomationID: run.AutomationID, NotebookID: notebookID, Title: title, Markdown: output.FinalMessage})
	if err != nil {
		j.Log.Warn("a step's answer was not saved as a note", "run", run.ID, "notebook", notebookID, "error", err.Error())
		reason := "The answer could not be saved as a note in the notebook the step names."
		if output.Outcome.Reason != nil {
			reason = *output.Outcome.Reason + " " + reason
		}
		output.Outcome = StepOutcome{Status: NeedsAttention, Reason: &reason}
		return
	}
	output.SavedNote = &SavedNote{DocumentID: id, NotebookID: notebookID, Title: title}
	// After the commit: the indexer reads the row the moment it takes the
	// message. A note whose job was not queued is saved and readable; it
	// is found by search once something else queues it, so it is logged.
	if err := j.Indexer.Index(saving, id); err != nil {
		j.Log.Warn("a saved note was not queued for indexing", "run", run.ID, "document", id, "error", err.Error())
	}
}

func plural(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// refusalReason is what the model, and then the person, is told about a
// call that was not run.
func refusalReason(call agent.Approval, trusted bool) string {
	if !call.AllowAlways {
		// The tool's own rule, which no setting of an automation waives.
		return "This is an automation's unattended run, and " + call.Tool + " asks a person every time it is used. It was not run."
	}
	if trusted {
		// Trusted tools do not wait; reaching here means the trust could
		// not be applied, which is still a refusal and is said plainly.
		return "This is an automation's unattended run and " + call.Tool + " could not be approved. It was not run."
	}
	return "This is an automation's unattended run, and " + call.Tool + " asks before it acts. The automation is not set to let its tools act without asking, so it was not run."
}

// transcript is the conversation after the task, bounded: the task itself
// is in the definition, and a tool result can be a whole document.
func transcript(messages []model.Message, resultChars int) []Entry {
	entries := make([]Entry, 0, len(messages))
	calls := map[string]string{}
	for i, m := range messages {
		if i == 0 && m.Role == model.RoleUser {
			continue
		}
		entry := Entry{Role: string(m.Role), Content: m.Content}
		for _, call := range m.ToolCalls {
			calls[call.ID] = call.Name
			entry.Calls = append(entry.Calls, EntryCall{Name: call.Name, Args: clipped(call.Args, resultChars)})
		}
		if m.Role == model.RoleTool {
			entry.Tool = calls[m.ToolCallID]
			entry.Content = clipped(m.Content, resultChars)
		}
		entries = append(entries, entry)
	}
	return entries
}

// clipped is text cut to max characters, saying so when it was.
func clipped(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "\n… (" + strconv.Itoa(len(runes)-max) + " more characters not kept)"
}

// encode is an object as the JSON a jsonb column takes, or nil (SQL NULL)
// for none: "no error" is a NULL column, never a JSON null in one.
func encode(object map[string]any) ([]byte, error) {
	if object == nil {
		return nil, nil
	}
	return json.Marshal(object)
}
