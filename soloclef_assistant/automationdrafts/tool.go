// Package automationdrafts is the chat's create_automation tool: from what a
// person describes ("every Monday, summarise new papers on asthma and email
// me"), the model fills a short form; this package writes it as a full
// automation, checks it exactly as saving an automation is checked, and
// keeps it as a draft for a card in the chat. It never saves an automation.
// The person reads the draft on the card, changes any field there, and
// presses Create, which sends it through the ordinary create route
// (soloclef_api/internal/automations); or Discard.
//
// What it does better than the tool it replaces (the Python app's
// create_automation): the draft is checked by the create route's own code
// before it is shown, so a card never offers something Create will refuse,
// and the model is told every problem at once and may fix them a bounded
// number of times; the card is editable field by field instead of
// approve-or-reject over raw JSON; the draft is a row, so a card reloaded
// or opened on another device shows whether it was already created, and
// Create cannot run twice; and the model is told, from soloclef_automations'
// own lists, what an automation can and cannot do, and to ask when a
// request leaves something out instead of guessing.
package automationdrafts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
	"git.danvics.com/danvics/soloclef/soloclef_automations/definition"
	"git.danvics.com/danvics/soloclef/soloclef_automations/schedule"
)

// ToolName is what the model calls, and what the card is keyed to.
const ToolName = "create_automation"

// maxRefusedDrafts is how many drafts one turn may have refused before the
// tool stops taking more. Two fixes are what a model needs to get a draft
// right once it has been told every problem; past that it is guessing, and
// the person should be told what is wrong instead.
const maxRefusedDrafts = 3

// Checker is the create route's own check of a new automation
// (soloclef_api/internal/automations.Checker): the problems with request in
// words, or an error when the check could not be made.
type Checker interface {
	Check(ctx context.Context, userID string, request []byte) ([]string, error)
}

// Drafts keeps drafts and reads what a draft may name (Store).
type Drafts interface {
	Save(ctx context.Context, userID string, request []byte, replaces string) (string, error)
	Notebooks(ctx context.Context, userID string) ([]Choice, error)
	Folders(ctx context.Context, userID string) ([]Choice, error)
}

// Tools makes the tool for a turn. Every field is required.
type Tools struct {
	Drafts  Drafts
	Checker Checker
	// SendsMail is whether this server sends mail, so that the model is not
	// told an automation can email a result on a server where saving one
	// that does would be refused.
	SendsMail bool
	Logger    *slog.Logger
	Now       func() time.Time
}

// Check reports what is missing. Called once at start-up.
func (t *Tools) Check() error {
	if t == nil || t.Drafts == nil || t.Checker == nil || t.Logger == nil || t.Now == nil {
		return errors.New("automationdrafts: Drafts, Checker, Logger and Now are required")
	}
	return nil
}

// Here is what the turn knows about where the person is.
type Here struct {
	// TimeZone is the IANA zone of the person's device, as the chat request
	// said it, or "" when it did not.
	TimeZone string
}

// Tool is create_automation for one turn of userID.
//
// It is made per turn, and so is its count of refused drafts: the limit is
// on one answer's attempts, and the person's next message starts again.
func (t *Tools) Tool(userID string) tool.Tool {
	var mu sync.Mutex
	refused := 0
	return tool.Tool{
		Name:        ToolName,
		Description: description,
		Parameters:  parameters,
		// It saves nothing: the card it makes is the question, with every
		// field in front of the person and Create on it. A gate before it
		// would be a second question about nothing, which is also why the
		// chat's "ask first" list never applies to it (assembly).
		Approval: tool.Auto,
		Run: func(ctx context.Context, call tool.Call) (string, error) {
			text, problems, err := t.draft(ctx, userID, call.Args)
			if err != nil || len(problems) == 0 {
				return text, err
			}
			mu.Lock()
			refused++
			count := refused
			mu.Unlock()
			if count >= maxRefusedDrafts {
				return "", fmt.Errorf("the draft was refused again and no card was shown: %s. Stop drafting now. Tell the user plainly, in a sentence or two, what could not be set up and why, and what they could ask for instead", strings.Join(problems, "; "))
			}
			return "", fmt.Errorf("the draft was not shown to the user, because: %s. Fix these and call %s again; where a fix needs something only the user knows, ask them instead of guessing", strings.Join(problems, "; "), ToolName)
		},
	}
}

// draftIDs is a draft's id as it is handed out.
var draftIDs = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// draft makes one draft: the result for the model, or the problems that
// kept it from being shown, or an error that is not the draft's fault.
func (t *Tools) draft(ctx context.Context, userID string, raw json.RawMessage) (string, []string, error) {
	// The account's notebooks and folders are read only when the draft
	// names one, which most do not.
	var choices here
	var peek struct {
		OnlyNotebook *string `json:"answer_only_from_notebook"`
		SaveTo       *string `json:"save_results_to_notebook"`
		Folder       *string `json:"when_document_arrives_in_folder"`
	}
	_ = json.Unmarshal(raw, &peek)
	if peek.OnlyNotebook != nil || peek.SaveTo != nil {
		notebooks, err := t.Drafts.Notebooks(ctx, userID)
		if err != nil {
			return "", nil, t.failure(ctx, "the notebooks could not be read", err)
		}
		choices.notebooks, choices.cut = notebooks, choices.cut || len(notebooks) == maxChoices
	}
	if peek.Folder != nil {
		folders, err := t.Drafts.Folders(ctx, userID)
		if err != nil {
			return "", nil, t.failure(ctx, "the folders could not be read", err)
		}
		choices.folders, choices.cut = folders, choices.cut || len(folders) == maxChoices
	}
	request, args, problems := build(raw, choices)
	if len(problems) > 0 {
		return "", problems, nil
	}
	replaces := strings.TrimSpace(args.Replaces)
	if replaces != "" && !draftIDs.MatchString(replaces) {
		return "", []string{"replaces_draft_id must be the draft_id a draft of this conversation was given; leave it out for a new draft"}, nil
	}
	// The create route's own check: what Create will accept, asked now.
	problems, err := t.Checker.Check(ctx, userID, request)
	if err != nil {
		return "", nil, t.failure(ctx, "the draft could not be checked", err)
	}
	if len(problems) > 0 {
		return "", problems, nil
	}
	id, err := t.Drafts.Save(ctx, userID, request, replaces)
	switch {
	case errors.Is(err, ErrNotWaiting):
		return "", []string{"replaces_draft_id is not a draft that is still waiting (it was created, discarded or replaced already); make a new draft without it"}, nil
	case err != nil:
		return "", nil, t.failure(ctx, "the draft could not be kept", err)
	}
	return resultPrefix + id + "\n" + t.summary(request), nil, nil
}

// failure logs what went wrong and tells the model, in a sentence it can
// pass on, without the database's words.
func (t *Tools) failure(ctx context.Context, what string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	t.Logger.LogAttrs(ctx, slog.LevelError, "create_automation: "+what, slog.String("error", err.Error()))
	return errors.New(what + " just now, so no draft was made; tell the user and suggest trying again shortly")
}

// resultPrefix begins a draft's result: the card is found by it (Card), and
// the model is told the id to replace the draft by.
const resultPrefix = "automation_draft_id="

// summary is what the model is told about a draft on its card: what it
// will do and when it first runs, and that it is not saved.
func (t *Tools) summary(request []byte) string {
	var drafted struct {
		Name     string `json:"name"`
		Triggers []struct {
			Type   string          `json:"type"`
			Params json.RawMessage `json:"params"`
		} `json:"triggers"`
	}
	// The request was written by build a moment ago and passed the check.
	_ = json.Unmarshal(request, &drafted)
	when := ""
	if len(drafted.Triggers) == 1 {
		if parsed, err := definition.ParseTrigger(drafted.Triggers[0].Type, drafted.Triggers[0].Params); err == nil {
			switch {
			case parsed.Schedule != nil:
				var params definition.ScheduleParams
				_ = json.Unmarshal(parsed.Params, &params)
				when = " It would first run at " + firstRun(params, t.Now()) + "."
			case parsed.Event != nil:
				when = " It would run each time a document arrives in the folder named."
			}
		}
	}
	return "The draft " + quoteName(drafted.Name) + " is on a card in front of the user." + when +
		" It has NOT been created: only the user can create it, by pressing Create on the card, where they can change any field first. " +
		"Do not say it was created. Tell the user in a sentence that it is ready to check, mentioning the first run in their time zone, and anything you assumed. " +
		"If they then ask for a change in words, call " + ToolName + " again with the whole automation as it should be and replaces_draft_id set to this draft's id.\n"
}

// firstRun is a schedule's next fire, written in its own zone.
func firstRun(params definition.ScheduleParams, now time.Time) string {
	compiled, err := schedule.Parse(params.Cron, params.Timezone)
	if err != nil {
		return "an unknown time"
	}
	zone, err := time.LoadLocation(params.Timezone)
	if err != nil {
		return "an unknown time"
	}
	return compiled.Next(now).In(zone).Format("Monday 2 January 2006, 15:04") + " (" + params.Timezone + ")"
}

func quoteName(name string) string {
	return "“" + strings.ReplaceAll(name, "\n", " ") + "”"
}

// card is the card's payload: the draft's id and nothing else. The card
// asks the server for the draft when it is drawn, so it always shows the
// draft as it now is (created, discarded, replaced), which a copy saved in
// the message could not.
type card struct {
	Status  string `json:"status"`
	DraftID string `json:"draft_id"`
}

// Card is what the create_automation card may show of a finished call: the
// draft's id from the result's first line. ok is false for another tool, a
// failed call (the page shows it as one) or a result that does not begin as
// a draft's does.
func Card(result event.Event) (payload any, ok bool) {
	if result.Tool != ToolName || result.IsError || !strings.HasPrefix(result.Text, resultPrefix) {
		return nil, false
	}
	line, _, _ := strings.Cut(strings.TrimPrefix(result.Text, resultPrefix), "\n")
	if !draftIDs.MatchString(line) {
		return nil, false
	}
	return card{Status: "drafted", DraftID: line}, true
}
