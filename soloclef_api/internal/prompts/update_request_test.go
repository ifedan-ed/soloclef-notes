package prompts

import (
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

func changesFrom(t *testing.T, body string) (Changes, []string) {
	t.Helper()
	changes, failures, err := ReadChanges("application/json", []byte(body))
	if err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	messages := make([]string, 0, len(failures))
	for _, failed := range failures {
		messages = append(messages, strings.Join(failed.Location, ".")+": "+failed.Message)
	}
	return changes, messages
}

// What a body *mentioned* decides whether a column is written and whether
// the version is bumped. A field mentioned with the value it already holds
// still counts, because the test is on the keys the body sent and not on the
// values; a field no prompt has (an old client's is_public) is not a mention.
func TestWhichMentionsBumpTheVersion(t *testing.T) {
	for _, test := range []struct {
		body     string
		mentions bool
	}{
		{`{}`, false},
		{`{"nobody_declared":1}`, false},
		{`{"is_public":true}`, false},
		{`{"name":"n"}`, true},
		{`{"prompt":"p"}`, true},
		{`{"mode":"explore"}`, true},
		// An explicit null is a mention: it is written, and every one of these
		// columns is NOT NULL, so it is a 500.
		{`{"name":null}`, true},
		{`{"name":"n","is_public":false}`, true},
	} {
		changes, failures := changesFrom(t, test.body)
		if len(failures) > 0 {
			t.Fatalf("%s: %v", test.body, failures)
		}
		if changes.Mentions() != test.mentions {
			t.Errorf("%s: Mentions() = %v, want %v", test.body, changes.Mentions(), test.mentions)
		}
	}
}

// The three fields are read in PromptUpdate's declaration order — name,
// prompt, mode — because that is the order their failures are reported in
// whatever order the request wrote them. An is_public that is not even a
// boolean is no failure: it is not a field of a prompt.
func TestTheFailuresAreInTheModelsFieldOrder(t *testing.T) {
	_, failures := changesFrom(t, `{"is_public":0.5,"mode":"nope","prompt":"","name":7}`)
	want := []string{
		"body.name: " + requestbody.NotAString,
		"body.prompt: String should have at least 1 character",
		"body.mode: " + requestbody.PatternMismatch(ModePattern),
	}
	if strings.Join(failures, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(failures, "\n"), strings.Join(want, "\n"))
	}
}

// The create model's own order, and its own refusals.
func TestTheCreateModelsFailuresAreInItsOwnOrder(t *testing.T) {
	_, failures, err := ReadRequest("application/json",
		[]byte(`{"is_public":null,"mode":7,"name":""}`))
	if err != nil {
		t.Fatal(err)
	}
	messages := make([]string, 0, len(failures))
	for _, failed := range failures {
		messages = append(messages, strings.Join(failed.Location, ".")+": "+failed.Message)
	}
	want := []string{
		"body.name: String should have at least 1 character",
		"body.prompt: " + requestbody.FieldRequired,
		"body.mode: " + requestbody.NotAString,
	}
	if strings.Join(messages, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(messages, "\n"), strings.Join(want, "\n"))
	}
}
