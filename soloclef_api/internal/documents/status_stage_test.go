package documents

import (
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// The status poll says what a processing document is waiting on, and says
// nothing new about any other document.
func TestTheStatusPollCarriesTheStageOfAProcessingDocument(t *testing.T) {
	for _, test := range []struct {
		name, stored, want string
	}{
		{"being parsed", `{"state": "processing", "stage": "parsing", "attempt": 1}`,
			`{"state":"processing","reason":null,"stage":"parsing"}`},
		{"waiting to be tried again", `{"state": "processing", "stage": "retrying", "attempt": 2}`,
			`{"state":"processing","reason":null,"stage":"retrying"}`},
		{"processing with no stage, as the Python pipeline writes it", `{"state": "processing"}`,
			`{"state":"processing","reason":null}`},
		// The worker removes the stage when a document ends; a stage left
		// on a finished document by anything else is not reported.
		{"ready with a stale stage", `{"state": "ready", "stage": "indexing"}`,
			`{"state":"ready","reason":null}`},
		{"failed, with its reason", `{"state": "failed", "reason": "The parser did not answer."}`,
			`{"state":"failed","reason":"The parser did not answer."}`},
		{"a stage that is not text", `{"state": "processing", "stage": 3}`,
			`{"state":"processing","reason":null}`},
		// The attempt count is the worker's own and is never answered.
		{"pending", `{"state": "pending"}`, `{"state":"pending","reason":null}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, err := buildStatus([]byte(test.stored))
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := pythonjson.Marshal(status)
			if err != nil {
				t.Fatal(err)
			}
			if string(rendered) != test.want {
				t.Errorf("answered %s, want %s", rendered, test.want)
			}
		})
	}
}
