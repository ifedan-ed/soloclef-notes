package workspacelogs

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// captured is one logs row and the body the Python tier answered
// GET /api/v1/logs/{log_id} with for it.
//
// testdata/entries.json holds eight of them, taken against the seeded database
// described in custom/docs/go-api-tier.md: a log with metadata, one with none,
// one whose metadata is SQL NULL, one whose source is ” rather than NULL, one
// with nested metadata and a non-string task name, one of another workspace,
// one carrying floats at both ends of pydantic's decimal/scientific boundary,
// and one carrying U+2028, U+2029 and an integer wider than int64. Nothing in
// it is an account's own text — every row was written for the comparison.
type captured struct {
	ID          int64   `json:"id"`
	Level       string  `json:"level"`
	Status      string  `json:"status"`
	Message     string  `json:"message"`
	Source      *string `json:"source"`
	Metadata    *string `json:"log_metadata"`
	WorkspaceID int64   `json:"workspace_id"`
	CreatedAt   string  `json:"created_at"`
	Body        string  `json:"body"`
}

func TestBuildRendersWhatThePythonTierAnswered(t *testing.T) {
	raw, err := os.ReadFile("testdata/entries.json")
	if err != nil {
		t.Fatalf("read the capture: %v", err)
	}
	var entries []captured
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode the capture: %v", err)
	}
	if len(entries) < 8 {
		t.Fatalf("the capture holds %d entries, want the whole seeded table", len(entries))
	}

	for _, want := range entries {
		createdAt, err := time.Parse("2006-01-02T15:04:05.000000Z", want.CreatedAt)
		if err != nil {
			t.Fatalf("entry %d: created_at %q: %v", want.ID, want.CreatedAt, err)
		}
		var metadata []byte
		if want.Metadata != nil {
			metadata = []byte(*want.Metadata)
		}

		rendered, err := Build(Entry{
			CreatedAt:   createdAt,
			ID:          want.ID,
			Level:       want.Level,
			Status:      want.Status,
			Message:     want.Message,
			Source:      want.Source,
			Metadata:    metadata,
			WorkspaceID: want.WorkspaceID,
		})
		if err != nil {
			t.Errorf("entry %d: %v", want.ID, err)
			continue
		}
		body, err := marshal(rendered)
		if err != nil {
			t.Errorf("entry %d: %v", want.ID, err)
			continue
		}
		if body != want.Body {
			t.Errorf("entry %d:\n got %s\nwant %s", want.ID, body, want.Body)
		}
	}
}

// A log_metadata column holding something other than an object fails the
// response model there, so it must not be answered here either.
func TestBuildRefusesMetadataThePythonTierWouldHaveFailedOn(t *testing.T) {
	for _, stored := range []string{`["not","an","object"]`, `7`, `"text"`, `true`} {
		if _, err := Build(Entry{Metadata: []byte(stored)}); err == nil {
			t.Errorf("metadata %s was rendered; the Python tier answers 500", stored)
		}
	}
	// A column that is SQL NULL, and one holding the JSON scalar null, are both
	// None there and both render as null here.
	for _, stored := range [][]byte{nil, []byte("null")} {
		rendered, err := Build(Entry{Metadata: stored})
		if err != nil {
			t.Fatalf("metadata %q: %v", stored, err)
		}
		if value, present := rendered.Get("log_metadata"); !present || value != nil {
			t.Errorf("metadata %q rendered as %v, want null", stored, value)
		}
	}
}
