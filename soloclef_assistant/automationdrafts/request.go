package automationdrafts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_automations/definition"
)

// arguments are what the model passes. They are the automation in the
// person's terms (a notebook by its title, a folder by its name, a reach),
// not the stored definition: the model fills a short form and this package
// writes the envelope, so a model never has to get schema_version, step ids
// or the trigger's wire shape right, and a definition is always written the
// one way the card can edit (one step, one trigger).
type arguments struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Task        string `json:"task"`
	Schedule    *struct {
		Cron     string `json:"cron"`
		Timezone string `json:"timezone"`
	} `json:"schedule"`
	Folder       *string `json:"when_document_arrives_in_folder"`
	Reach        string  `json:"reach"`
	OnlyNotebook *string `json:"answer_only_from_notebook"`
	SaveTo       *string `json:"save_results_to_notebook"`
	EmailMe      bool    `json:"email_me"`
	Model        string  `json:"model"`
	Replaces     string  `json:"replaces_draft_id"`
}

// parameters is the arguments' schema, as the model sees it.
var parameters = json.RawMessage(`{"type":"object","properties":{` +
	`"name":{"type":"string","description":"A short name for the automation, at most 200 characters."},` +
	`"description":{"type":"string","description":"One line saying what it does and when, for the Automations list."},` +
	`"task":{"type":"string","description":"What the assistant is asked at every run, written as a complete instruction to it (it runs with nobody present to ask). Include what to look at, what to produce and how long. Do not ask it to email, save or file the result: email_me and save_results_to_notebook do that."},` +
	`"schedule":{"type":"object","properties":{"cron":{"type":"string","description":"Five fields: minute hour day-of-month month day-of-week, numbers only (0 or 7 is Sunday). Every Monday at 08:00 is \"0 8 * * 1\"."},"timezone":{"type":"string","description":"The IANA time zone the times are in, e.g. Africa/Lagos."}},"required":["cron","timezone"],"description":"Run on a schedule. Give this or when_document_arrives_in_folder, not both."},` +
	`"when_document_arrives_in_folder":{"type":"string","description":"Run each time a document finishes processing in this folder, by its name. Give this or schedule, not both."},` +
	`"reach":{"type":"string","enum":["documents","web","all"],"description":"What the task may read: the user's documents (the default), the web, or both."},` +
	`"answer_only_from_notebook":{"type":"string","description":"Confine the task to this notebook's sources, by the notebook's title. Only with reach documents."},` +
	`"save_results_to_notebook":{"type":"string","description":"Keep each run's result as a note in this notebook, by its title."},` +
	`"email_me":{"type":"boolean","description":"Email each run's result to the user."},` +
	`"model":{"type":"string","description":"Only when the user named a model: its name as the model menu lists it. Leave out to run on the user's own chat model."},` +
	`"replaces_draft_id":{"type":"string","description":"When the user asked in words to change a draft this conversation made: that draft's id. The old card then says it was replaced."}` +
	`},"required":["name","task"]}`)

// here is what a turn knows that a draft's names are read against.
type here struct {
	// notebooks and folders are the account's, for names; cut says either
	// list was bounded (maxChoices).
	notebooks, folders []Choice
	cut                bool
}

// draftedTrigger is a trigger as the create route takes it.
type draftedTrigger struct {
	Type         string          `json:"type"`
	Params       any             `json:"params"`
	StaticInputs json.RawMessage `json:"static_inputs"`
	Enabled      bool            `json:"enabled"`
}

// draftedRequest is a create request (POST /api/v1/automations's body).
type draftedRequest struct {
	Name        string            `json:"name"`
	Description *string           `json:"description"`
	Definition  draftedDefinition `json:"definition"`
	Triggers    []draftedTrigger  `json:"triggers"`
}

type draftedDefinition struct {
	SchemaVersion string             `json:"schema_version"`
	Name          string             `json:"name"`
	Plan          []draftedStep      `json:"plan"`
	Execution     draftedExecution   `json:"execution"`
	Models        *definition.Models `json:"models,omitempty"`
}

type draftedStep struct {
	StepID string          `json:"step_id"`
	Action string          `json:"action"`
	Params definition.Task `json:"params"`
}

// draftedExecution says only what the person chose; the definition's own
// defaults (timeout, retries, concurrency) are applied when it is read
// (definition.Parse), so a draft never carries a second copy of them.
type draftedExecution struct {
	EmailResult bool `json:"email_result"`
}

// build reads the model's arguments into a create request. problems are
// for the model, in words, and name the argument at fault.
func build(raw []byte, choices here) ([]byte, arguments, []string) {
	var args arguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	// A misspelt argument is said, not dropped: a dropped "email_me" would
	// be a draft that quietly does not email.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return nil, args, []string{"the arguments could not be read: " + err.Error()}
	}
	var problems []string
	say := func(format string, values ...any) { problems = append(problems, fmt.Sprintf(format, values...)) }
	name, task := strings.TrimSpace(args.Name), strings.TrimSpace(args.Task)
	if name == "" {
		say("name is required")
	}
	if task == "" {
		say("task is required: it is what the assistant is asked at every run")
	}

	params := definition.Task{Query: task, AutoApproveAll: true}
	// The run trusts the tools the person marked "ask first", as the
	// builder's default does: nobody is present to be asked, and without it
	// such a tool fails the step. The card shows the switch.
	switch args.Reach {
	case "", "documents":
	case "web", "all":
		reach := args.Reach
		params.RetrievalScope = &reach
	default:
		say("reach must be documents, web or all")
	}
	if args.OnlyNotebook != nil {
		if id, problem := resolve("answer_only_from_notebook", "notebook", *args.OnlyNotebook, choices.notebooks, choices.cut); problem != "" {
			say("%s", problem)
		} else {
			params.NotebookID = &id
		}
	}
	if args.SaveTo != nil {
		if id, problem := resolve("save_results_to_notebook", "notebook", *args.SaveTo, choices.notebooks, choices.cut); problem != "" {
			say("%s", problem)
		} else {
			params.SaveToNotebookID = &id
		}
	}

	var trigger draftedTrigger
	switch {
	case args.Schedule != nil && args.Folder != nil:
		say("give schedule or when_document_arrives_in_folder, not both: an automation drafted here has one trigger")
	case args.Schedule != nil:
		trigger = draftedTrigger{Type: definition.TriggerSchedule, Params: definition.ScheduleParams{
			Cron: strings.Join(strings.Fields(args.Schedule.Cron), " "), Timezone: strings.TrimSpace(args.Schedule.Timezone)}}
	case args.Folder != nil:
		if id, problem := resolve("when_document_arrives_in_folder", "folder", *args.Folder, choices.folders, choices.cut); problem != "" {
			say("%s", problem)
		} else {
			trigger = draftedTrigger{Type: definition.TriggerEvent, Params: definition.EventParams{
				EventType: definition.DocumentEnteredFolder, Filter: map[string]any{"folder_id": id}}}
		}
	default:
		say("say when it runs: give schedule (cron and timezone) or when_document_arrives_in_folder; if the user has not said, ask them")
	}
	trigger.StaticInputs, trigger.Enabled = json.RawMessage(`{}`), true

	model := strings.TrimSpace(args.Model)
	var models *definition.Models
	if model != "" {
		models = &definition.Models{Chat: model}
	}
	if len(problems) > 0 {
		return nil, args, problems
	}
	var description *string
	if text := strings.TrimSpace(args.Description); text != "" {
		description = &text
	}
	request, err := json.Marshal(draftedRequest{
		Name: name, Description: description,
		Definition: draftedDefinition{
			SchemaVersion: "1.0", Name: name, Models: models,
			Plan:      []draftedStep{{StepID: "task", Action: definition.AgentTask, Params: params}},
			Execution: draftedExecution{EmailResult: args.EmailMe},
		},
		Triggers: []draftedTrigger{trigger},
	})
	if err != nil {
		return nil, args, []string{"the draft could not be written: " + err.Error()}
	}
	return request, args, nil
}

// resolve finds the notebook or folder a name means: the one whose title is
// the name, ignoring case and surrounding spaces. A name that matches none,
// or more than one, is a problem listing the choices, so the model can ask
// the user which they meant instead of picking one. A number names nothing:
// ids are not something a person says, and a title may be a number.
func resolve(argument, kind, name string, choices []Choice, cut bool) (int64, string) {
	wanted := strings.ToLower(strings.TrimSpace(name))
	var found []Choice
	for _, choice := range choices {
		if strings.ToLower(strings.TrimSpace(choice.Title)) == wanted {
			found = append(found, choice)
		}
	}
	if len(found) == 1 {
		return found[0].ID, ""
	}
	listing := listed(choices, cut)
	if len(found) > 1 {
		return 0, fmt.Sprintf("%s: the user has %d %ss called %q; ask them which one they mean, or to rename one", argument, len(found), kind, name)
	}
	if len(choices) == 0 {
		return 0, fmt.Sprintf("%s: the user has no %ss", argument, kind)
	}
	return 0, fmt.Sprintf("%s: no %s is called %q. The user's %ss: %s. If none is meant, ask the user", argument, kind, name, kind, listing)
}

// listed is the choices' titles for a problem, at most forty of them.
func listed(choices []Choice, cut bool) string {
	const shown = 40
	titles := make([]string, 0, shown)
	for i, choice := range choices {
		if i == shown {
			break
		}
		titles = append(titles, strconv.Quote(choice.Title))
	}
	text := strings.Join(titles, ", ")
	if len(choices) > shown || cut {
		text += " (and more)"
	}
	return text
}
