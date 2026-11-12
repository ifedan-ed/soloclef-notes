package definition

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const minimal = `{"name":"Morning digest","plan":[{"step_id":"digest","action":"agent_task","params":{"query":"Summarise what arrived yesterday."}}]}`

func TestTheBuildersDefaultsAreApplied(t *testing.T) {
	d, problems := Parse([]byte(minimal))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	x := d.Execution
	if d.SchemaVersion != "1.0" || x.TimeoutSeconds != 600 || x.MaxRetries != 2 || x.RetryBackoff != "exponential" || x.Concurrency != DropIfRunning {
		t.Errorf("defaults: %+v, version %q", x, d.SchemaVersion)
	}
	stored, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	// What is stored is what the detail page parses: lists are lists, never
	// null, and metadata has its tags.
	for _, piece := range []string{`"triggers":[]`, `"on_failure":[]`, `"metadata":{"tags":[]}`, `"goal":null`} {
		if !strings.Contains(string(stored), piece) {
			t.Errorf("stored definition lacks %s: %s", piece, stored)
		}
	}
	task, problems := ParseTask(d.Plan[0].Params)
	if len(problems) > 0 || task.Scope() != "documents" || task.AutoApproveAll {
		t.Errorf("task: %+v %v", task, problems)
	}
}

func TestATimeLimitOverADayIsLoweredToADay(t *testing.T) {
	d, problems := Parse([]byte(`{"name":"n","execution":{"timeout_seconds":999999},"plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}]}`))
	if len(problems) > 0 || d.Execution.TimeoutSeconds != MaxTimeoutSeconds {
		t.Errorf("%v %d", problems, d.Execution.TimeoutSeconds)
	}
}

// Whatever a run could not honour is refused by name when it is saved.
func TestWhatCannotBeRunIsRefusedWhenSaved(t *testing.T) {
	step := func(params string) string {
		return `{"name":"n","plan":[{"step_id":"a","action":"agent_task","params":` + params + `}]}`
	}
	for _, c := range []struct{ name, raw, says string }{
		{"no plan", `{"name":"n","plan":[]}`, "at least one step"},
		{"no name", `{"name":" ","plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}]}`, "name"},
		{"an unknown action", `{"name":"n","plan":[{"step_id":"a","action":"send_email","params":{}}]}`, "agent_task"},
		{"a repeated step id", `{"name":"n","plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}},{"step_id":"a","action":"agent_task","params":{"query":"q"}}]}`, "repeats"},
		{"a condition", `{"name":"n","plan":[{"step_id":"a","action":"agent_task","when":"x","params":{"query":"q"}}]}`, "when is not available"},
		{"no query", step(`{"query":"  "}`), "query is required"},
		{"a bad scope", step(`{"query":"q","retrieval_scope":"moon"}`), "retrieval_scope"},
		{"a notebook that also leaves the documents", step(`{"query":"q","notebook_id":3,"retrieval_scope":"all"}`), "retrieval_scope must be documents"},
		{"a notebook that also names a connected account", step(`{"query":"q","notebook_id":3,"mentioned_connector_ids":[1]}`), "cannot also name a connected account"},
		{"a notebook id that is not one", step(`{"query":"q","notebook_id":0}`), "notebook_id must be a notebook's id"},
		{"a note saved nowhere", step(`{"query":"q","save_to_notebook_id":-4}`), "save_to_notebook_id must be a notebook's id"},
		{"a connector id that is not one", step(`{"query":"q","mentioned_connector_ids":[2,0]}`), "mentioned_connector_ids must hold ids"},
		{"an empty model name", step(`{"query":"q","model":" "}`), "model must be a model's name"},
		{"the old model ids", `{"name":"n","models":{"chat_model_id":0,"image_gen_model_id":3,"vision_model_id":4},"plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}]}`, "chat_model_id"},
		{"models that name none", `{"name":"n","models":{},"plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}]}`, "models.chat must be a model's name"},
		{"a misspelt field", step(`{"query":"q","auto_aprove_all":true}`), "auto_aprove_all"},
		{"failure steps", `{"name":"n","execution":{"on_failure":[{"step_id":"b","action":"agent_task","params":{"query":"q"}}]},"plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}]}`, "on_failure is not available"},
		{"a bad concurrency", `{"name":"n","execution":{"concurrency":"sometimes"},"plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}]}`, "concurrency"},
		{"an unknown top-level field", `{"name":"n","plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}],"workspace_id":1}`, "workspace_id"},
	} {
		_, problems := Parse([]byte(c.raw))
		if !strings.Contains(strings.Join(problems, "; "), c.says) {
			t.Errorf("%s: %v, want a problem saying %q", c.name, problems, c.says)
		}
	}
}

// A step picks its model, else the automation's, else the account's; and
// everything a definition names that this package cannot vouch for is
// listed for the tier that can.
func TestWhatADefinitionNames(t *testing.T) {
	d, problems := Parse([]byte(`{"name":"n","models":{"chat":"claude-fable-5-2"},"execution":{"email_result":true},"plan":[
		{"step_id":"a","action":"agent_task","params":{"query":"q","notebook_id":7,"save_to_notebook_id":7}},
		{"step_id":"b","action":"agent_task","params":{"query":"q","model":"gpt-5.2-mini","save_to_notebook_id":9,"retrieval_scope":"all","mentioned_connector_ids":[3,4,3]}},
		{"step_id":"c","action":"agent_task","params":{"query":"q","model":"claude-fable-5-2"}}]}`))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	var models []string
	for _, step := range d.Plan {
		task, _ := ParseTask(step.Params)
		models = append(models, d.ChatModel(task))
	}
	if strings.Join(models, ",") != "claude-fable-5-2,gpt-5.2-mini,claude-fable-5-2" {
		t.Errorf("the steps run on %v", models)
	}
	named := d.Named()
	if !named.Email || len(named.NotebookIDs) != 2 || named.NotebookIDs[0] != 7 || named.NotebookIDs[1] != 9 ||
		len(named.ConnectorIDs) != 2 || named.ConnectorIDs[0] != 3 || named.ConnectorIDs[1] != 4 ||
		strings.Join(named.Models, ",") != "claude-fable-5-2,gpt-5.2-mini" {
		t.Errorf("Named = %+v", named)
	}
	// The stored definition keeps the choice in the shape the form reads.
	stored, _ := json.Marshal(d)
	if !strings.Contains(string(stored), `"models":{"chat":"claude-fable-5-2"}`) || !strings.Contains(string(stored), `"email_result":true`) {
		t.Errorf("stored as %s", stored)
	}

	// With no model named anywhere, the answer is "", the account's own.
	plain, problems := Parse([]byte(minimal))
	task, _ := ParseTask(plain.Plan[0].Params)
	if len(problems) > 0 || plain.ChatModel(task) != "" || len(plain.Named().Models) != 0 || plain.Named().Email {
		t.Errorf("a definition that names no model: %v, %q, %+v", problems, plain.ChatModel(task), plain.Named())
	}
	if stored, _ := json.Marshal(plain); strings.Contains(string(stored), "models") {
		t.Errorf("a definition with no models stored %s", stored)
	}
}

func TestATriggerIsCheckedByItsType(t *testing.T) {
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	daily, err := ParseTrigger(TriggerSchedule, json.RawMessage(`{"cron":"0 9 * * *","timezone":"UTC"}`))
	if err != nil {
		t.Fatal(err)
	}
	if next := daily.NextFire(true, now); next == nil || !next.Equal(now.Add(time.Hour)) {
		t.Errorf("next fire %v", next)
	}
	if next := daily.NextFire(false, now); next != nil {
		t.Errorf("a trigger switched off waits for %v", next)
	}
	arrival, err := ParseTrigger(TriggerEvent, json.RawMessage(`{"event_type":"document.entered_folder"}`))
	if err != nil || string(arrival.Params) != `{"event_type":"document.entered_folder","filter":{}}` || arrival.NextFire(true, now) != nil {
		t.Errorf("%s %v", arrival.Params, err)
	}
	for _, c := range []struct{ kind, params, says string }{
		{"schedule", `{"cron":"0 9 * *","timezone":"UTC"}`, "five fields"},
		{"schedule", `{"cron":"0 9 * * *"}`, "time zone"},
		{"schedule", `{"cron":"0 9 * * *","timezone":"UTC","catchup":true}`, "catchup"},
		{"event", `{"event_type":"document.indexed"}`, "unknown event type"},
		{"event", `{"event_type":"document.entered_folder","filter":{"folder_id":{"$near":1}}}`, "unknown operator"},
		{"manual", `{}`, "unknown trigger type"},
		{"schedule", `[]`, "must be an object"},
	} {
		if _, err := ParseTrigger(c.kind, json.RawMessage(c.params)); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s %s: %v, want %q", c.kind, c.params, err, c.says)
		}
	}
}
