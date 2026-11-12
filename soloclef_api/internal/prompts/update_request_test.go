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

// What a body *mentioned* is three decisions, not one: whether a column is
// written, whether the version is bumped, and whether publishing is refused.
// A field mentioned with the value it already holds still counts, because the
// Python route tests the keys of model_dump(exclude_unset=True) and not the
// values.
func TestWhichMentionsBumpTheVersion(t *testing.T) {
	for _, test := range []struct {
		body     string
		mentions bool
		content  bool
	}{
		{`{}`, false, false},
		{`{"nobody_declared":1}`, false, false},
		{`{"name":"n"}`, true, true},
		{`{"prompt":"p"}`, true, true},
		{`{"mode":"explore"}`, true, true},
		{`{"is_public":false}`, true, false},
		{`{"is_public":true}`, true, false},
		// An explicit null is a mention: it is written, and every one of these
		// four columns is NOT NULL, so it is a 500 on both tiers.
		{`{"name":null}`, true, true},
		{`{"is_public":null}`, true, false},
		{`{"name":"n","is_public":false}`, true, true},
	} {
		changes, failures := changesFrom(t, test.body)
		if len(failures) > 0 {
			t.Fatalf("%s: %v", test.body, failures)
		}
		if changes.Mentions() != test.mentions {
			t.Errorf("%s: Mentions() = %v, want %v", test.body, changes.Mentions(), test.mentions)
		}
		if changes.MentionsContent() != test.content {
			t.Errorf("%s: MentionsContent() = %v, want %v",
				test.body, changes.MentionsContent(), test.content)
		}
	}
}

// _refuse_public_unless_admin's first argument is the Python expression
// `updates.get("is_public") and not prompt.is_public`, which is falsy in three
// of its four branches — including for a prompt that is *already* published,
// which is why its owner may keep editing it without being an administrator
// (tests/integration/prompts/test_community_publishing.py asserts exactly
// that).
func TestOnlyTurningPublishingOnAsksToPublish(t *testing.T) {
	for _, test := range []struct {
		body          string
		alreadyPublic bool
		asks          bool
	}{
		{`{}`, false, false},
		{`{"name":"n"}`, false, false},
		{`{"is_public":null}`, false, false},
		{`{"is_public":false}`, false, false},
		{`{"is_public":false}`, true, false},
		{`{"is_public":true}`, false, true},
		{`{"is_public":true}`, true, false},
		{`{"is_public":"yes"}`, false, true},
		{`{"is_public":"off"}`, false, false},
		{`{"is_public":1}`, false, true},
		{`{"is_public":0}`, false, false},
	} {
		changes, failures := changesFrom(t, test.body)
		if len(failures) > 0 {
			t.Fatalf("%s: %v", test.body, failures)
		}
		if got := changes.AsksToPublish(test.alreadyPublic); got != test.asks {
			t.Errorf("%s (already public %v): got %v, want %v",
				test.body, test.alreadyPublic, got, test.asks)
		}
	}
}

// The four fields are read in PromptUpdate's declaration order — name,
// prompt, mode, is_public — because that is the order FastAPI reports their
// failures in whatever order the request wrote them.
func TestTheFailuresAreInTheModelsFieldOrder(t *testing.T) {
	_, failures := changesFrom(t, `{"is_public":0.5,"mode":"nope","prompt":"","name":7}`)
	want := []string{
		"body.name: " + requestbody.NotAString,
		"body.prompt: String should have at least 1 character",
		"body.mode: " + requestbody.PatternMismatch(ModePattern),
		"body.is_public: " + requestbody.NotABoolean,
	}
	if strings.Join(failures, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(failures, "\n"), strings.Join(want, "\n"))
	}
}

// The create model's own order, and its own refusals: is_public there is a
// plain `bool = False`, so a null is not the None branch of a union.
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
		"body.is_public: " + requestbody.NotABoolean,
	}
	if strings.Join(messages, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(messages, "\n"), strings.Join(want, "\n"))
	}
}
