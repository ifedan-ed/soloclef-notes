package errorenvelope

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestWriteValidationMatchesTheCapturedPythonBody(t *testing.T) {
	// Captured from the running Python tier:
	// GET /api/v1/logs/workspaces/abc/summary?hours=xyz
	// The report URL is this deployment's own setting, so the capture's value
	// is stood in for here; nothing else was changed.
	writer := Writer{
		ReportURL: "https://notes.example.com/contact",
		Now: func() time.Time {
			return time.Date(2026, 11, 7, 20, 45, 32, 269159000, time.UTC)
		},
	}
	recorder := httptest.NewRecorder()

	writer.WriteValidation(recorder, "req_c5ea0343d3fc", []Failure{
		{Location: []string{"path", "workspace_id"}, Message: IntegerExpected},
		{Location: []string{"query", "hours"}, Message: IntegerExpected},
	})

	const want = `{"error":{"code":"VALIDATION_ERROR","message":"Validation failed: path -> workspace_id: ` +
		`Input should be a valid integer, unable to parse string as an integer; query -> hours: ` +
		`Input should be a valid integer, unable to parse string as an integer","status":422,` +
		`"request_id":"req_c5ea0343d3fc","timestamp":"2026-11-07T20:45:32.269159+00:00",` +
		`"report_url":"https://notes.example.com/contact","fields":[` +
		`{"loc":["path","workspace_id"],"msg":"Input should be a valid integer, unable to parse string as an integer"},` +
		`{"loc":["query","hours"],"msg":"Input should be a valid integer, unable to parse string as an integer"}]},` +
		`"detail":"Validation failed: path -> workspace_id: Input should be a valid integer, unable to parse string as an integer; ` +
		`query -> hours: Input should be a valid integer, unable to parse string as an integer"}`
	if got := recorder.Body.String(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if recorder.Code != 422 {
		t.Errorf("status: got %d", recorder.Code)
	}
}
