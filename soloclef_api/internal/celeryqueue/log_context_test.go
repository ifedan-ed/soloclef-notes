package celeryqueue

import (
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// The workspace id comes from the path and from nowhere else, because that is
// where the Python middleware reads it: a document delete knows the document's
// workspace and still publishes without the field.
func TestTheWorkspaceIdComesFromThePathAsItDoesThere(t *testing.T) {
	cases := map[string]string{
		"/api/v1/documents/7":          "",
		"/api/v1/workspaces/3":         "3",
		"/api/v1/workspaces/3/roles":   "3",
		"/api/v1/workspaces/3/roles/4": "3",
		"/api/v1/workspaces/abc":       "",
		"/api/v1/workspaces":           "",
		"/api/v1/pats/2":               "",
	}
	for path, want := range cases {
		got := LogContextFor(path, "req_1ea615454491", "11111111-1111-4111-8111-111111111111")
		if got.WorkspaceID != want {
			t.Errorf("%s: workspace_id = %q, want %q", path, got.WorkspaceID, want)
		}
		if got.RequestID != "req_1ea615454491" || got.UserID != "11111111-1111-4111-8111-111111111111" {
			t.Errorf("%s: %+v", path, got)
		}
	}
}

// The fields the Python tier never binds for a request stay unset: a task
// published with an empty thread id would carry a field Celery's envelope did
// not have.
func TestNothingElseIsBound(t *testing.T) {
	got := LogContextFor("/api/v1/workspaces/3", "req_1", "user-1")
	if got.ThreadID != "" || got.JobID != "" {
		t.Errorf("%+v", got)
	}
	if got == (produce.LogContext{}) {
		t.Error("nothing was bound at all")
	}
}
