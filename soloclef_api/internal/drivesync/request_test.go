package drivesync

import (
	"strings"
	"testing"
)

func TestTheImportBodyIsReadStrictly(t *testing.T) {
	read, wrong := readRequest(strings.NewReader(`{"files": [
		{"id": "1AbCdEfGhIjKlMnOp", "name": "Report.pdf", "mime_type": "application/pdf"},
		{"id": "2AbCdEfGhIjKlMnOp", "name": "Plan"},
		{"id": "1AbCdEfGhIjKlMnOp", "name": "Report again.pdf", "mime_type": "application/pdf"}
	], "notebook_id": 7}`))
	if wrong != "" {
		t.Fatalf("a good body was refused: %s", wrong)
	}
	// The same file picked twice is one pick, the first.
	if len(read.Picks) != 2 || read.Picks[0] != (Pick{"1AbCdEfGhIjKlMnOp", "Report.pdf", "application/pdf"}) ||
		read.Picks[1] != (Pick{ID: "2AbCdEfGhIjKlMnOp", Name: "Plan"}) || read.NotebookID == nil || *read.NotebookID != 7 {
		t.Errorf("read %+v", read)
	}

	file := `{"id": "1AbCdEfGhIjKlMnOp", "name": "a.pdf"}`
	tooMany := `{"files": [` + strings.Repeat(file+",", MaxPickedFiles) + file + `]}`
	for name, body := range map[string]string{
		"not JSON":                `files=1`,
		"no files":                `{"files": [], "notebook_id": null}`,
		"too many files":          tooMany,
		"a file with no id":       `{"files": [{"name": "a.pdf"}]}`,
		"an id that is a path":    `{"files": [{"id": "../../../about?fields=*", "name": "a.pdf"}]}`,
		"a file with no name":     `{"files": [{"id": "1AbCdEfGhIjKlMnOp", "name": "  "}]}`,
		"a name with a NUL":       `{"files": [{"id": "1AbCdEfGhIjKlMnOp", "name": "a\u0000.pdf"}]}`,
		"a name too long":         `{"files": [{"id": "1AbCdEfGhIjKlMnOp", "name": "` + strings.Repeat("n", 513) + `"}]}`,
		"a notebook that is zero": `{"files": [` + file + `], "notebook_id": 0}`,
		// A key naming an owner is unknown like any other: the documents
		// are the caller's, and a client that believes otherwise is told.
		"an owner":       `{"files": [` + file + `], "user_id": "22222222-2222-4222-8222-222222222222"}`,
		"a workspace":    `{"files": [` + file + `], "workspace_id": 3}`,
		"two objects":    `{"files": [` + file + `]} {"files": []}`,
		"a body too big": `{"files": [` + file + `], "notebook_id": null` + strings.Repeat(" ", bodyLimit) + `}`,
	} {
		if _, wrong := readRequest(strings.NewReader(body)); wrong == "" {
			t.Errorf("%s was accepted", name)
		}
	}
}
