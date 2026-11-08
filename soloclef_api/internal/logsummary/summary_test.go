package logsummary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// The two fixtures are one request to the running Python tier:
// testdata/rows.json is what its query read out of the logs table, and
// testdata/summary.json is the response body it returned, byte for byte.
// Free text (messages, sources, metadata values) was replaced in both together
// after the capture, because this repository has a public copy; nothing that
// decides the bytes — the row order, the key order, the statuses, the levels,
// the timestamps, the metadata structure — was touched. See
// custom/docs/go-api-tier.md for how the untouched capture was checked.
const capturedWindowHours = 2000

type storedRow struct {
	ID        int64           `json:"id"`
	Status    string          `json:"status"`
	Level     string          `json:"level"`
	Message   string          `json:"message"`
	Source    *string         `json:"source"`
	CreatedAt string          `json:"created_at"`
	Metadata  json.RawMessage `json:"log_metadata"`
}

func loadRows(t *testing.T) []Row {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "rows.json"))
	if err != nil {
		t.Fatalf("reading the captured rows: %v", err)
	}
	var stored []storedRow
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("parsing the captured rows: %v", err)
	}

	rows := make([]Row, 0, len(stored))
	for _, row := range stored {
		createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
		if err != nil {
			t.Fatalf("log %d has an unparsable created_at: %v", row.ID, err)
		}
		rows = append(rows, Row{
			ID:        row.ID,
			Status:    row.Status,
			Level:     row.Level,
			Message:   row.Message,
			Source:    row.Source,
			CreatedAt: createdAt,
			Metadata:  row.Metadata,
		})
	}
	return rows
}

func TestBuildReproducesThePythonResponseByteForByte(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "summary.json"))
	if err != nil {
		t.Fatalf("reading the captured response: %v", err)
	}

	summary, err := Build(loadRows(t), capturedWindowHours)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := pythonjson.Marshal(summary)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if string(got) != string(want) {
		t.Errorf("the response differs from the Python tier's.\ngot:  %s\nwant: %s",
			firstDifference(string(got), string(want)), firstDifference(string(want), string(got)))
	}
}

// firstDifference quotes the 120 bytes around the first byte that differs,
// because the whole body is thousands of bytes long.
func firstDifference(text, other string) string {
	at := 0
	for at < len(text) && at < len(other) && text[at] == other[at] {
		at++
	}
	start := max(at-60, 0)
	end := min(at+60, len(text))
	return text[start:end]
}

func TestBuildCountsInTheOrderTheRowsArrive(t *testing.T) {
	// The counter keys are in first-seen order, which is newest row first.
	summary, err := Build([]Row{
		{ID: 3, Status: "FAILED", Level: "ERROR", Message: "last"},
		{ID: 2, Status: "SUCCESS", Level: "INFO", Message: "middle"},
		{ID: 1, Status: "SUCCESS", Level: "INFO", Message: "first"},
	}, 24)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := pythonjson.Marshal(summary)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `{"total_logs":3,"time_window_hours":24,"by_status":{"FAILED":1,"SUCCESS":2},` +
		`"by_level":{"ERROR":1,"INFO":2},"by_source":{},"active_tasks":[],` +
		`"recent_failures":[{"id":3,"task_name":"Unknown","message":"last",` +
		`"failed_at":"0001-01-01T00:00:00+00:00","source":null,"error_details":null}]}`
	if string(got) != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestBuildReportsAtMostTenFailures(t *testing.T) {
	rows := make([]Row, 0, 12)
	for id := 12; id > 0; id-- {
		rows = append(rows, Row{ID: int64(id), Status: "FAILED", Level: "ERROR", Message: "failed"})
	}

	summary, err := Build(rows, 24)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	failures, _ := summary.Get("recent_failures")
	reported, _ := failures.([]any)
	if len(reported) != 10 {
		t.Errorf("got %d failures, want the Python route's cap of 10", len(reported))
	}
	// The count is still every row, as the Python route counts them.
	if total, _ := summary.Get("by_status"); total.(*pythonjson.Object).Len() != 1 {
		t.Error("the status counter should hold one key")
	}
}

func TestBuildTreatsEmptyMetadataAsNone(t *testing.T) {
	// `if log.log_metadata` is false for None and for {} alike.
	for name, metadata := range map[string][]byte{
		"no column value": nil,
		"json null":       []byte("null"),
		"an empty object": []byte("{}"),
	} {
		summary, err := Build([]Row{
			{ID: 1, Status: "IN_PROGRESS", Level: "INFO", Message: "working", Metadata: metadata},
		}, 24)
		if err != nil {
			t.Fatalf("%s: Build: %v", name, err)
		}
		tasks, _ := summary.Get("active_tasks")
		task := tasks.([]any)[0].(*pythonjson.Object)
		if taskName, _ := task.Get("task_name"); taskName != unknownTaskName {
			t.Errorf("%s: task_name is %#v", name, taskName)
		}
		if documentID, _ := task.Get("document_id"); documentID != nil {
			t.Errorf("%s: document_id is %#v", name, documentID)
		}
	}
}

func TestBuildRefusesMetadataThePythonRouteWouldHaveFailedOn(t *testing.T) {
	_, err := Build([]Row{
		{ID: 1, Status: "IN_PROGRESS", Level: "INFO", Metadata: []byte(`["not", "an", "object"]`)},
	}, 24)
	if err == nil {
		t.Error("want an error, got none")
	}
}
