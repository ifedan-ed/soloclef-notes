package automations

import (
	"encoding/json"
	"strings"
	"time"
)

// The JSON the web app's contracts read (contracts/types/automation.types.ts).

type summaryView struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Status      string    `json:"status"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type detailView struct {
	summaryView
	Definition json.RawMessage `json:"definition"`
	Triggers   []triggerView   `json:"triggers"`
}

type triggerView struct {
	ID           int64           `json:"id"`
	Type         string          `json:"type"`
	Params       json.RawMessage `json:"params"`
	StaticInputs json.RawMessage `json:"static_inputs"`
	Enabled      bool            `json:"enabled"`
	LastFiredAt  *time.Time      `json:"last_fired_at"`
	NextFireAt   *time.Time      `json:"next_fire_at"`
	CreatedAt    time.Time       `json:"created_at"`
}

type runSummaryView struct {
	ID           int64      `json:"id"`
	AutomationID int64      `json:"automation_id"`
	TriggerID    *int64     `json:"trigger_id"`
	Status       string     `json:"status"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

type runView struct {
	runSummaryView
	DefinitionSnapshot json.RawMessage `json:"definition_snapshot"`
	Inputs             json.RawMessage `json:"inputs"`
	StepResults        json.RawMessage `json:"step_results"`
	Output             json.RawMessage `json:"output"`
	Artifacts          json.RawMessage `json:"artifacts"`
	Error              json.RawMessage `json:"error"`
}

type page[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
}

// shaped is a jsonb column as the page's schema wants it: the stored value
// when it is of the wanted kind, else the empty value of that kind. A row
// written some other way must not fail the page that lists it.
func shaped(column []byte, opens, empty string) json.RawMessage {
	if trimmed := strings.TrimSpace(string(column)); strings.HasPrefix(trimmed, opens) {
		return json.RawMessage(trimmed)
	}
	return json.RawMessage(empty)
}

func object(column []byte) json.RawMessage { return shaped(column, "{", "{}") }
func list(column []byte) json.RawMessage   { return shaped(column, "[", "[]") }

// nullable is a jsonb column that may be NULL: an object, or JSON null.
func nullable(column []byte) json.RawMessage { return shaped(column, "{", "null") }

// moment and momentOrNil put a time in UTC. pgx reads a timestamptz into
// the process's own zone, and a page that compares or shows these must not
// depend on where the server runs.
func moment(t time.Time) time.Time { return t.UTC() }

func momentOrNil(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}

func summaryOfAutomation(id int64, name string, description *string, status string, version int64, created, updated time.Time) summaryView {
	return summaryView{ID: id, Name: name, Description: description, Status: status, Version: version,
		CreatedAt: moment(created), UpdatedAt: moment(updated)}
}

func triggerOf(row ListTriggersRow) triggerView {
	return triggerView{ID: row.ID, Type: row.Type, Params: object(row.Params), StaticInputs: object(row.StaticInputs),
		Enabled: row.Enabled, LastFiredAt: momentOrNil(row.LastFiredAt), NextFireAt: momentOrNil(row.NextFireAt), CreatedAt: moment(row.CreatedAt)}
}

func summaryOfRun(id, automationID int64, triggerID *int64, status string, started, finished *time.Time, created time.Time) runSummaryView {
	return runSummaryView{ID: id, AutomationID: automationID, TriggerID: triggerID, Status: status,
		StartedAt: momentOrNil(started), FinishedAt: momentOrNil(finished), CreatedAt: moment(created)}
}
