package automationdrafts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
	"git.danvics.com/danvics/soloclef/soloclef_automations/definition"
)

// drafts keeps drafts in memory, as Store keeps them in Postgres.
type drafts struct {
	saved     [][]byte
	replaced  []string
	notebooks []Choice
	folders   []Choice
	reads     int
	failSave  bool
}

func (d *drafts) Save(_ context.Context, _ string, request []byte, replaces string) (string, error) {
	if d.failSave {
		return "", errors.New("connection refused")
	}
	if replaces == "99999999-9999-4999-8999-999999999999" {
		return "", ErrNotWaiting
	}
	d.saved = append(d.saved, request)
	d.replaced = append(d.replaced, replaces)
	return "0b5c2f7e-1d2a-4c3b-9e8f-7a6b5c4d3e2f", nil
}

func (d *drafts) Notebooks(context.Context, string) ([]Choice, error) {
	d.reads++
	return d.notebooks, nil
}

func (d *drafts) Folders(context.Context, string) ([]Choice, error) {
	d.reads++
	return d.folders, nil
}

// checker answers what the create route's check would: the problems it was
// told to find, and the requests it was asked about.
type checker struct {
	problems []string
	asked    [][]byte
	down     bool
}

func (c *checker) Check(_ context.Context, _ string, request []byte) ([]string, error) {
	c.asked = append(c.asked, request)
	if c.down {
		return nil, errors.New("the model service cannot be reached")
	}
	return c.problems, nil
}

func tools(d *drafts, c *checker) *Tools {
	return &Tools{Drafts: d, Checker: c, SendsMail: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC) }}
}

func run(t *testing.T, made tool.Tool, args string) (string, error) {
	t.Helper()
	return made.Run(context.Background(), tool.Call{ID: "c1", Name: ToolName, Args: json.RawMessage(args)})
}

const asthma = `{"name":"Asthma papers","description":"Monday digest of new asthma papers","task":"Summarise new papers on asthma.",
 "schedule":{"cron":"0  8 * * 1","timezone":"Africa/Lagos"},"reach":"web","save_results_to_notebook":"asthma","email_me":true}`

// A draft is written in the stored envelope, one step and one trigger, with
// names read as ids and nothing the person did not choose.
func TestADraftIsWrittenAsTheCreateRouteTakesIt(t *testing.T) {
	d := &drafts{notebooks: []Choice{{ID: 7, Title: "Asthma"}, {ID: 9, Title: "Grants"}}}
	c := &checker{}
	text, err := run(t, tools(d, c).Tool("ada"), asthma)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.saved) != 1 || len(c.asked) != 1 || string(c.asked[0]) != string(d.saved[0]) {
		t.Fatalf("saved %d, checked %d: the saved draft must be the checked one", len(d.saved), len(c.asked))
	}
	var got map[string]any
	if err := json.Unmarshal(d.saved[0], &got); err != nil {
		t.Fatal(err)
	}
	want := `{"definition":{"execution":{"email_result":true},"name":"Asthma papers","plan":[{"action":"agent_task","params":{"auto_approve_all":true,"query":"Summarise new papers on asthma.","retrieval_scope":"web","save_to_notebook_id":7},"step_id":"task"}],"schema_version":"1.0"},"description":"Monday digest of new asthma papers","name":"Asthma papers","triggers":[{"enabled":true,"params":{"cron":"0 8 * * 1","timezone":"Africa/Lagos"},"static_inputs":{},"type":"schedule"}]}`
	if again, _ := json.Marshal(got); string(again) != want {
		t.Errorf("request =\n%s\nwant\n%s", again, want)
	}
	// Its definition is one the worker reads.
	var envelope struct {
		Definition json.RawMessage `json:"definition"`
	}
	_ = json.Unmarshal(d.saved[0], &envelope)
	if _, problems := definition.Parse(envelope.Definition); len(problems) > 0 {
		t.Errorf("the definition does not parse: %v", problems)
	}
	for _, says := range []string{resultPrefix + "0b5c2f7e-1d2a-4c3b-9e8f-7a6b5c4d3e2f\n", "Monday 16 November 2026, 08:00 (Africa/Lagos)", "NOT been created", "replaces_draft_id"} {
		if !strings.Contains(text, says) {
			t.Errorf("the result lacks %q:\n%s", says, text)
		}
	}
}

// What the card may show of a result is the draft's id, nothing else, and
// only for a draft's result.
func TestTheCardCarriesTheDraftsIDOnly(t *testing.T) {
	ok := event.Event{Tool: ToolName, Text: resultPrefix + "0b5c2f7e-1d2a-4c3b-9e8f-7a6b5c4d3e2f\nThe draft ..."}
	payload, mine := Card(ok)
	encoded, _ := json.Marshal(payload)
	if !mine || string(encoded) != `{"status":"drafted","draft_id":"0b5c2f7e-1d2a-4c3b-9e8f-7a6b5c4d3e2f"}` {
		t.Fatalf("card = %s, %v", encoded, mine)
	}
	for name, result := range map[string]event.Event{
		"another tool":  {Tool: "draft_email", Text: ok.Text},
		"a failed call": {Tool: ToolName, Text: ok.Text, IsError: true},
		"not an id":     {Tool: ToolName, Text: resultPrefix + "'; drop table\n"},
		"no prefix":     {Tool: ToolName, Text: "the draft was not shown"},
	} {
		if _, mine := Card(result); mine {
			t.Errorf("%s made a card", name)
		}
	}
}

// Every problem with a draft reaches the model at once, nothing is saved,
// and after a bounded number of refusals the model is told to stop and say
// what cannot be done.
func TestARefusedDraftIsSaidAndTheRetriesAreBounded(t *testing.T) {
	d := &drafts{}
	c := &checker{problems: []string{"the model \"gone\" is not offered; choose one from the list", "notebook 4 is not one of yours"}}
	made := tools(d, c).Tool("ada")
	for attempt := 1; attempt <= maxRefusedDrafts; attempt++ {
		_, err := run(t, made, `{"name":"N","task":"T","schedule":{"cron":"0 8 * * 1","timezone":"UTC"},"model":"gone"}`)
		if err == nil {
			t.Fatalf("attempt %d was accepted", attempt)
		}
		for _, problem := range c.problems {
			if !strings.Contains(err.Error(), problem) {
				t.Errorf("attempt %d does not say %q: %v", attempt, problem, err)
			}
		}
		last := attempt == maxRefusedDrafts
		if strings.Contains(err.Error(), "Stop drafting") != last {
			t.Errorf("attempt %d: %v", attempt, err)
		}
	}
	if len(d.saved) != 0 {
		t.Fatal("a refused draft was saved")
	}
	// A new turn's tool counts from nothing.
	if _, err := run(t, tools(d, c).Tool("ada"), `{"name":"N","task":"T","schedule":{"cron":"0 8 * * 1","timezone":"UTC"}}`); err == nil || strings.Contains(err.Error(), "Stop drafting") {
		t.Errorf("a new turn: %v", err)
	}
}

// What the tool can see wrong is said without asking the create route, and
// a name that is not one of the person's lists what is.
func TestTheToolsOwnProblems(t *testing.T) {
	d := &drafts{notebooks: []Choice{{ID: 7, Title: "Asthma"}, {ID: 8, Title: "Twins"}, {ID: 9, Title: "twins"}}, folders: []Choice{{ID: 3, Title: "Papers"}}}
	for name, c := range map[string]struct{ args, says string }{
		"no trigger":         {`{"name":"N","task":"T"}`, "if the user has not said, ask them"},
		"two triggers":       {`{"name":"N","task":"T","schedule":{"cron":"0 8 * * *","timezone":"UTC"},"when_document_arrives_in_folder":"Papers"}`, "not both"},
		"an unknown key":     {`{"name":"N","task":"T","email":true,"schedule":{"cron":"0 8 * * *","timezone":"UTC"}}`, `unknown field "email"`},
		"no task":            {`{"name":"N","task":"  ","schedule":{"cron":"0 8 * * *","timezone":"UTC"}}`, "task is required"},
		"a missing notebook": {`{"name":"N","task":"T","schedule":{"cron":"0 8 * * *","timezone":"UTC"},"save_results_to_notebook":"Sepsis"}`, `no notebook is called "Sepsis". The user's notebooks: "Asthma", "Twins", "twins"`},
		"two of a name":      {`{"name":"N","task":"T","schedule":{"cron":"0 8 * * *","timezone":"UTC"},"answer_only_from_notebook":"TWINS"}`, "2 notebooks called"},
		"a bad reach":        {`{"name":"N","task":"T","schedule":{"cron":"0 8 * * *","timezone":"UTC"},"reach":"internet"}`, "reach must be"},
		"a bad replace":      {`{"name":"N","task":"T","schedule":{"cron":"0 8 * * *","timezone":"UTC"},"replaces_draft_id":"draft 1"}`, "replaces_draft_id must be"},
		"a used replace":     {`{"name":"N","task":"T","schedule":{"cron":"0 8 * * *","timezone":"UTC"},"replaces_draft_id":"99999999-9999-4999-8999-999999999999"}`, "not a draft that is still waiting"},
	} {
		c2 := &checker{}
		_, err := run(t, tools(d, c2).Tool("ada"), c.args)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want it to say %q", name, err, c.says)
		}
	}
	// A folder by its name becomes the event trigger the worker waits on.
	c := &checker{}
	if _, err := run(t, tools(d, c).Tool("ada"), `{"name":"N","task":"T","when_document_arrives_in_folder":" papers "}`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(c.asked[0]), `"triggers":[{"type":"event","params":{"event_type":"document.entered_folder","filter":{"folder_id":3}}`) {
		t.Errorf("event trigger: %s", c.asked[0])
	}
}

// A check or a save that fails is not the draft's fault: the model is told
// to try later, in a sentence without the database's words.
func TestAFailureIsNotADraftProblem(t *testing.T) {
	ok := `{"name":"N","task":"T","schedule":{"cron":"0 8 * * *","timezone":"UTC"}}`
	if _, err := run(t, tools(&drafts{}, &checker{down: true}).Tool("ada"), ok); err == nil || !strings.Contains(err.Error(), "could not be checked just now") {
		t.Errorf("check down: %v", err)
	}
	if _, err := run(t, tools(&drafts{failSave: true}, &checker{}).Tool("ada"), ok); err == nil || strings.Contains(err.Error(), "connection refused") {
		t.Errorf("save failed: %v", err)
	}
}

// The model is told what an automation can do from soloclef_automations'
// own lists, so what the chat promises is what saving accepts: every
// trigger, event, action and scope the definition accepts is described,
// and nothing it does not accept.
func TestWhatAnAutomationCanDoIsTheDefinitionsOwnList(t *testing.T) {
	for name, pair := range map[string]struct {
		accepted []string
		words    map[string]string
	}{
		"triggers": {definition.TriggerTypes, triggerWords},
		"events":   {definition.EventTypes, eventWords},
		"actions":  {definition.Actions, actionWords},
		"reach":    {definition.RetrievalScopes, reachWords},
	} {
		var described []string
		for key := range pair.words {
			described = append(described, key)
		}
		accepted := slices.Clone(pair.accepted)
		sort.Strings(described)
		sort.Strings(accepted)
		if !slices.Equal(described, accepted) {
			t.Errorf("%s: described %v, the definition accepts %v", name, described, accepted)
		}
	}
	// Each reach the tool's schema offers is one the definition accepts.
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if offered := schema.Properties["reach"].Enum; !slices.Equal(offered, definition.RetrievalScopes) {
		t.Errorf("the schema offers reach %v, the definition accepts %v", offered, definition.RetrievalScopes)
	}
}

// The tool and the prompt tell the model to ask when a request leaves
// something out, and to say what an automation cannot do; the prompt says
// the person's zone, or that it is unknown and must be asked.
func TestTheModelIsToldToAskAndToSayWhatCannotBeDone(t *testing.T) {
	made := tools(&drafts{}, &checker{}).Tool("ada")
	for _, says := range []string{"ask one short question", "Never guess", "cannot do", "say plainly", "does NOT create it"} {
		if !strings.Contains(made.Description, says) {
			t.Errorf("the description lacks %q", says)
		}
	}
	if made.Approval != tool.Auto {
		t.Error("the tool must not be gated: its card is the question")
	}
	with := tools(&drafts{}, &checker{}).Prompt(Here{TimeZone: "Africa/Lagos"})
	for _, says := range []string{"Ask a short clarifying question", "beyond what an automation can do", "time zone Africa/Lagos",
		"once a minute", "Send email to anyone", "Read a web page in full", "a document finishes processing"} {
		if !strings.Contains(with, says) {
			t.Errorf("the prompt lacks %q:\n%s", says, with)
		}
	}
	if without := tools(&drafts{}, &checker{}).Prompt(Here{}); !strings.Contains(without, "ask which time zone") {
		t.Errorf("without a zone:\n%s", without)
	}
	noMail := tools(&drafts{}, &checker{})
	noMail.SendsMail = false
	if prompt := noMail.Prompt(Here{}); !strings.Contains(prompt, "email_me is not available") {
		t.Errorf("a server that sends no mail:\n%s", prompt)
	}
}

func TestValidTimeZone(t *testing.T) {
	for zone, valid := range map[string]bool{"Africa/Lagos": true, "UTC": true, "Local": false, "": false, "Mars/Base": false, " UTC": false} {
		if ValidTimeZone(zone) != valid {
			t.Errorf("%q: %v", zone, !valid)
		}
	}
}
