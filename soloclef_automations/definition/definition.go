// Package definition is what a saved automation may say, read the same way
// by the tier that saves it and the tier that runs it.
//
// The envelope is the one the web's builder writes and its detail page
// reads (`automations.definition`): a name, a plan of steps and how to
// execute them. This tier runs one kind of step, "agent_task": one turn of
// the chat's agent, with nobody present.
//
// Everything in a definition is either honoured or refused when it is
// saved. Nothing is accepted and then ignored at run time.
//
// # What a definition names, and who checks it
//
// A definition names things by id and by name: notebooks, connected
// accounts, models. This package checks the shape of each (Parse) and
// lists what was named (Named). Whether a notebook is the owner's, a
// model is one the proxy offers, or the server can send mail is asked by
// the tier that saves the definition, which has the database and the
// proxy, and asked again by the tier that runs it, because all of those
// can change after a save.
//
// # Which model answers a step
//
// The step's own `model`, else the automation's `models.chat`, else the
// account's own chat model when the run starts. Each is the name the model
// proxy lists the model under, as the account's chat model is
// (/users/me/chat-model). No name is ever replaced by another: a model
// the proxy has withdrawn fails the step with a sentence saying which.
package definition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_automations/eventfilter"
	"git.danvics.com/danvics/soloclef/soloclef_automations/schedule"
)

// AgentTask is the one action a step may name.
const AgentTask = "agent_task"

// MaxTimeoutSeconds is the longest a run may be given: a day. A larger
// value is lowered to it, not refused, so a definition written for a longer
// limit still saves and runs.
const MaxTimeoutSeconds = 86_400

// MaxNameChars is the column's width.
const MaxNameChars = 200

// The trigger types a person may add. "manual" is in the database's enum
// and is not one of them: running by hand is a button, not a saved trigger.
const (
	TriggerSchedule = "schedule"
	TriggerEvent    = "event"
)

// DocumentEnteredFolder is the one event an event trigger may wait for: a
// ready document arriving in a folder (the API tier's eventstream).
const DocumentEnteredFolder = "document.entered_folder"

// What happens when a trigger fires while a run is waiting or running.
const (
	DropIfRunning = "drop_if_running"
	Queue         = "queue"
	Always        = "always"
)

// Definition is the envelope.
type Definition struct {
	SchemaVersion string          `json:"schema_version"`
	Name          string          `json:"name"`
	Goal          *string         `json:"goal"`
	Inputs        json.RawMessage `json:"inputs,omitempty"`
	Triggers      []TriggerSpec   `json:"triggers"`
	Plan          []Step          `json:"plan"`
	Execution     Execution       `json:"execution"`
	Metadata      json.RawMessage `json:"metadata"`
	// Models is which model the automation's steps run on, or nil for the
	// account's own.
	Models *Models `json:"models,omitempty"`
}

// Models is the automation's own choice of model.
type Models struct {
	// Chat is the proxy's name for the model that answers each step that
	// names none of its own.
	Chat string `json:"chat"`
}

// MaxModelNameChars bounds a model's name before the proxy's list is asked
// about it; proxy model names are far shorter.
const MaxModelNameChars = 255

// modelName checks a model's name as text: the proxy's list is the saving
// tier's to ask.
func modelName(name string) bool {
	return strings.TrimSpace(name) == name && name != "" && len([]rune(name)) <= MaxModelNameChars
}

// TriggerSpec is the builder's note of the triggers it made the automation
// with. The triggers that fire are rows of their own.
type TriggerSpec struct {
	Type   string          `json:"type"`
	Params json.RawMessage `json:"params"`
}

// Step is one step of the plan.
type Step struct {
	StepID string `json:"step_id"`
	Action string `json:"action"`
	// When was a condition under which the step ran. It is refused: nothing
	// here evaluates one, and a step that silently always ran would not be
	// what its author wrote.
	When   *string         `json:"when,omitempty"`
	Params json.RawMessage `json:"params"`
	// OutputAs named the step's output for a later step's template. Kept
	// for the page; steps here do not template each other.
	OutputAs *string `json:"output_as,omitempty"`
	// MaxRetries and TimeoutSeconds override Execution's for this step.
	MaxRetries     *int `json:"max_retries,omitempty"`
	TimeoutSeconds *int `json:"timeout_seconds,omitempty"`
}

// Execution is how the run is carried out.
type Execution struct {
	// TimeoutSeconds is the run's wall-clock limit from when it starts.
	TimeoutSeconds int `json:"timeout_seconds"`
	// MaxRetries is how many more times a step that failed is tried.
	MaxRetries   int    `json:"max_retries"`
	RetryBackoff string `json:"retry_backoff"`
	Concurrency  string `json:"concurrency"`
	// OnFailure is the builder's field for steps run after a failure.
	// Nothing runs them, so a definition that names any is refused.
	OnFailure []Step `json:"on_failure"`
	// EmailResult mails the run's result to the automation's owner when
	// the run ends. Whether this server sends mail at all is the saving
	// tier's to refuse.
	EmailResult bool `json:"email_result"`
}

// Task is an agent_task step's params.
type Task struct {
	// Query is what the agent is asked.
	Query string `json:"query"`
	// AutoApproveAll is the automation's own trust rule: with it, a tool
	// that asks first runs without asking. A tool that must always ask is
	// refused whatever this says; that is the agent loop's rule, and no
	// field of a definition can waive it.
	AutoApproveAll bool `json:"auto_approve_all"`
	// RetrievalScope is documents, web or all; unset means documents.
	RetrievalScope *string `json:"retrieval_scope,omitempty"`
	// The documents and folders the task names with @.
	MentionedDocumentIDs []int64 `json:"mentioned_document_ids,omitempty"`
	MentionedFolderIDs   []int64 `json:"mentioned_folder_ids,omitempty"`
	// MentionedDocuments is the chips' titles, for the page.
	MentionedDocuments json.RawMessage `json:"mentioned_documents,omitempty"`
	// NotebookID confines the step to one notebook: it reads that
	// notebook's enabled sources and nothing else, exactly as the
	// notebook's own chat does. So it cannot be combined with a scope
	// that leaves the documents or with a connected account.
	NotebookID *int64 `json:"notebook_id,omitempty"`
	// SaveToNotebookID files the step's answer as a note in that notebook
	// when the step succeeds.
	SaveToNotebookID *int64 `json:"save_to_notebook_id,omitempty"`
	// MentionedConnectorIDs are the connected accounts the task names with
	// @ (a mailbox connection, Google Drive, a connected app): the step
	// is told to use them. MentionedConnectors is the chips' names, for
	// the page.
	MentionedConnectorIDs []int64         `json:"mentioned_connector_ids,omitempty"`
	MentionedConnectors   json.RawMessage `json:"mentioned_connectors,omitempty"`
	// Model is the proxy's name for the model that answers this step, or
	// nil for the automation's.
	Model *string `json:"model,omitempty"`
}

// MaxMentions bounds how many documents, folders or connected accounts one
// task may name: a task is a sentence, not a list of a library.
const MaxMentions = 50

// ChatModel is the proxy name of the model that answers the task in this
// definition: the task's own, else the automation's, else "" for the
// account's own chat model.
func (d Definition) ChatModel(task Task) string {
	if task.Model != nil {
		return *task.Model
	}
	if d.Models != nil {
		return d.Models.Chat
	}
	return ""
}

// Named is what a definition names that only a database or the model proxy
// can vouch for: the saving tier checks each is the owner's, or offered.
type Named struct {
	NotebookIDs  []int64
	ConnectorIDs []int64
	// Models are the proxy names, each once.
	Models []string
	// Email says the definition asks for its result to be mailed.
	Email bool
}

// Named lists what a parsed definition names. It is meant for one that
// Parse accepted.
func (d Definition) Named() Named {
	named := Named{Email: d.Execution.EmailResult}
	add := func(list *[]int64, id int64) {
		for _, held := range *list {
			if held == id {
				return
			}
		}
		*list = append(*list, id)
	}
	model := func(name string) {
		for _, held := range named.Models {
			if held == name {
				return
			}
		}
		named.Models = append(named.Models, name)
	}
	if d.Models != nil {
		model(d.Models.Chat)
	}
	for _, step := range d.Plan {
		task, problems := ParseTask(step.Params)
		if len(problems) > 0 {
			continue
		}
		if task.NotebookID != nil {
			add(&named.NotebookIDs, *task.NotebookID)
		}
		if task.SaveToNotebookID != nil {
			add(&named.NotebookIDs, *task.SaveToNotebookID)
		}
		for _, id := range task.MentionedConnectorIDs {
			add(&named.ConnectorIDs, id)
		}
		if task.Model != nil {
			model(*task.Model)
		}
	}
	return named
}

// Scope is the task's retrieval scope with the default applied.
func (t Task) Scope() string {
	if t.RetrievalScope == nil {
		return "documents"
	}
	return *t.RetrievalScope
}

// strict decodes raw into out and refuses a field out does not have, so a
// misspelt key is said instead of dropped.
func strict(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.More() {
		return fmt.Errorf("more than one JSON value")
	}
	return nil
}

func isObject(raw json.RawMessage) bool {
	return strings.HasPrefix(strings.TrimSpace(string(raw)), "{")
}

// Parse reads a definition and applies the defaults the builder relies on.
// The second result lists everything wrong with it, in words for the person
// who wrote it; an empty list means the definition can be saved and run.
func Parse(raw []byte) (Definition, []string) {
	d := Definition{
		SchemaVersion: "1.0",
		Execution:     Execution{TimeoutSeconds: 600, MaxRetries: 2, RetryBackoff: "exponential", Concurrency: DropIfRunning},
	}
	if err := strict(raw, &d); err != nil {
		return Definition{}, []string{"the definition could not be read: " + err.Error()}
	}
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if name := strings.TrimSpace(d.Name); name == "" || len([]rune(d.Name)) > MaxNameChars {
		say("the definition's name must be 1 to %d characters", MaxNameChars)
	}
	if d.Triggers == nil {
		d.Triggers = []TriggerSpec{}
	}
	if len(d.Metadata) == 0 || string(d.Metadata) == "null" {
		d.Metadata = json.RawMessage(`{"tags":[]}`)
	} else if !isObject(d.Metadata) {
		say("metadata must be an object")
	}
	if string(d.Inputs) == "null" {
		d.Inputs = nil
	}
	if d.Models != nil && !modelName(d.Models.Chat) {
		say("models.chat must be a model's name of 1 to %d characters; leave models out to run on the account's own chat model", MaxModelNameChars)
	}

	x := &d.Execution
	if x.TimeoutSeconds < 1 {
		say("execution.timeout_seconds must be at least 1")
	} else if x.TimeoutSeconds > MaxTimeoutSeconds {
		x.TimeoutSeconds = MaxTimeoutSeconds
	}
	if x.MaxRetries < 0 || x.MaxRetries > 10 {
		say("execution.max_retries must be 0 to 10")
	}
	if x.RetryBackoff != "exponential" && x.RetryBackoff != "linear" && x.RetryBackoff != "none" {
		say("execution.retry_backoff must be exponential, linear or none")
	}
	if x.Concurrency != DropIfRunning && x.Concurrency != Queue && x.Concurrency != Always {
		say("execution.concurrency must be %s, %s or %s", DropIfRunning, Queue, Always)
	}
	if len(x.OnFailure) > 0 {
		say("execution.on_failure is not available: steps to run after a failure cannot be saved yet")
	}
	x.OnFailure = []Step{}

	if len(d.Plan) == 0 {
		say("the plan needs at least one step")
	}
	seen := map[string]bool{}
	for i := range d.Plan {
		step := &d.Plan[i]
		at := fmt.Sprintf("plan[%d]", i)
		switch {
		case strings.TrimSpace(step.StepID) == "":
			say("%s needs a step_id", at)
		case seen[step.StepID]:
			say("%s repeats the step_id %q", at, step.StepID)
		}
		seen[step.StepID] = true
		if step.When != nil && strings.TrimSpace(*step.When) != "" {
			say("%s.when is not available: a step cannot be made conditional yet", at)
		}
		if step.MaxRetries != nil && (*step.MaxRetries < 0 || *step.MaxRetries > 10) {
			say("%s.max_retries must be 0 to 10", at)
		}
		if step.TimeoutSeconds != nil && *step.TimeoutSeconds < 1 {
			say("%s.timeout_seconds must be at least 1", at)
		}
		if step.Action != AgentTask {
			say("%s.action is %q; the one action a step may run is %s", at, step.Action, AgentTask)
			continue
		}
		if _, taskProblems := ParseTask(step.Params); len(taskProblems) > 0 {
			for _, problem := range taskProblems {
				say("%s.params: %s", at, problem)
			}
		}
	}
	return d, problems
}

// ParseTask reads an agent_task step's params.
func ParseTask(raw json.RawMessage) (Task, []string) {
	var task Task
	if !isObject(raw) {
		return task, []string{"must be an object with a query"}
	}
	if err := strict(raw, &task); err != nil {
		return task, []string{"could not be read: " + err.Error()}
	}
	var problems []string
	if strings.TrimSpace(task.Query) == "" {
		problems = append(problems, "query is required: it is what the assistant is asked")
	}
	if scope := task.Scope(); scope != "documents" && scope != "web" && scope != "all" {
		problems = append(problems, "retrieval_scope must be documents, web or all")
	}
	positive := func(name string, ids []int64) {
		if len(ids) > MaxMentions {
			problems = append(problems, fmt.Sprintf("%s names %d; a task may name at most %d", name, len(ids), MaxMentions))
		}
		for _, id := range ids {
			if id < 1 {
				problems = append(problems, name+" must hold ids, which are whole numbers of at least 1")
				return
			}
		}
	}
	positive("mentioned_document_ids", task.MentionedDocumentIDs)
	positive("mentioned_folder_ids", task.MentionedFolderIDs)
	positive("mentioned_connector_ids", task.MentionedConnectorIDs)
	if task.NotebookID != nil {
		switch {
		case *task.NotebookID < 1:
			problems = append(problems, "notebook_id must be a notebook's id")
		case task.Scope() != "documents":
			// The notebook's own chat reads its sources and nothing
			// else; a step confined to one is held to the same line.
			problems = append(problems, "notebook_id confines the step to that notebook's sources, so retrieval_scope must be documents")
		case len(task.MentionedConnectorIDs) > 0:
			problems = append(problems, "notebook_id confines the step to that notebook's sources, so it cannot also name a connected account")
		}
	}
	if task.SaveToNotebookID != nil && *task.SaveToNotebookID < 1 {
		problems = append(problems, "save_to_notebook_id must be a notebook's id")
	}
	if task.Model != nil && !modelName(*task.Model) {
		problems = append(problems, fmt.Sprintf("model must be a model's name of 1 to %d characters; leave it out to run on the automation's model", MaxModelNameChars))
	}
	return task, problems
}

// ScheduleParams is a schedule trigger's params.
type ScheduleParams struct {
	Cron     string `json:"cron"`
	Timezone string `json:"timezone"`
}

// EventParams is an event trigger's params.
type EventParams struct {
	EventType string         `json:"event_type"`
	Filter    map[string]any `json:"filter"`
}

// Trigger is a trigger's params after they were checked: the JSON to store
// and, for a schedule, the schedule itself.
type Trigger struct {
	Type string
	// Params is the checked params, as stored.
	Params json.RawMessage
	// Schedule is set for a schedule trigger.
	Schedule *schedule.Schedule
	// Event is set for an event trigger.
	Event *EventParams
}

// NextFire is when an enabled schedule trigger next fires after `after`;
// nil for any other trigger and for one switched off, which waits for
// nothing.
func (t Trigger) NextFire(enabled bool, after time.Time) *time.Time {
	if t.Schedule == nil || !enabled {
		return nil
	}
	next := t.Schedule.Next(after)
	return &next
}

// ParseTrigger checks a trigger's type and params, or says what is wrong.
func ParseTrigger(kind string, raw json.RawMessage) (Trigger, error) {
	if !isObject(raw) {
		return Trigger{}, fmt.Errorf("a trigger's params must be an object")
	}
	switch kind {
	case TriggerSchedule:
		var params ScheduleParams
		if err := strict(raw, &params); err != nil {
			return Trigger{}, fmt.Errorf("the schedule could not be read: %w", err)
		}
		compiled, err := schedule.Parse(params.Cron, params.Timezone)
		if err != nil {
			return Trigger{}, err
		}
		stored, err := json.Marshal(params)
		if err != nil {
			return Trigger{}, err
		}
		return Trigger{Type: kind, Params: stored, Schedule: &compiled}, nil
	case TriggerEvent:
		var params EventParams
		if err := strict(raw, &params); err != nil {
			return Trigger{}, fmt.Errorf("the event trigger could not be read: %w", err)
		}
		if params.EventType != DocumentEnteredFolder {
			return Trigger{}, fmt.Errorf("unknown event type %q; the event a trigger may wait for is %s", params.EventType, DocumentEnteredFolder)
		}
		if params.Filter == nil {
			params.Filter = map[string]any{}
		}
		if err := eventfilter.Validate(params.Filter); err != nil {
			return Trigger{}, fmt.Errorf("the filter: %w", err)
		}
		stored, err := json.Marshal(params)
		if err != nil {
			return Trigger{}, err
		}
		return Trigger{Type: kind, Params: stored, Event: &params}, nil
	}
	return Trigger{}, fmt.Errorf("unknown trigger type %q; a trigger is a %s or an %s", kind, TriggerSchedule, TriggerEvent)
}
