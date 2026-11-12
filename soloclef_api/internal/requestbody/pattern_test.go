package requestbody

import (
	"regexp"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// mode is app/schemas/prompts.py's own constraint, which is the only pattern
// this app declares.
var mode = regexp.MustCompile("^(transform|explore)$")

func readPattern(t *testing.T, body string) (string, []string) {
	t.Helper()
	decoded, failure, err := Decode("application/json", []byte(body))
	if err != nil || failure != nil {
		t.Fatalf("the body did not decode: %v %v", err, failure)
	}
	model := NewModel(decoded)
	value := model.TextMatching("mode", 0, 0, mode)
	messages := make([]string, 0, len(model.Failures()))
	for _, failed := range model.Failures() {
		messages = append(messages, strings.Join(failed.Location, ".")+": "+failed.Message)
	}
	return value, messages
}

// Every row was asked of pydantic directly, with the model
// app/schemas/prompts.py declares.
func TestAPatternIsAppliedAsPydanticAppliesIt(t *testing.T) {
	for _, test := range []struct {
		body  string
		fails string
	}{
		{`{"mode":"transform"}`, ""},
		{`{"mode":"explore"}`, ""},
		// The pattern is anchored at both ends, and neither engine lets `$`
		// match before a trailing newline the way Python's `re` would.
		{`{"mode":"transform\n"}`, "body.mode: " + PatternMismatch(mode)},
		{`{"mode":"transform\r"}`, "body.mode: " + PatternMismatch(mode)},
		{`{"mode":"\ntransform"}`, "body.mode: " + PatternMismatch(mode)},
		{`{"mode":"Transform"}`, "body.mode: " + PatternMismatch(mode)},
		{`{"mode":"transformexplore"}`, "body.mode: " + PatternMismatch(mode)},
		{`{"mode":" transform"}`, "body.mode: " + PatternMismatch(mode)},
		{`{"mode":"transform "}`, "body.mode: " + PatternMismatch(mode)},
		{`{"mode":""}`, "body.mode: " + PatternMismatch(mode)},
		{`{"mode":"transform\u0000"}`, "body.mode: " + PatternMismatch(mode)},
		// The type is read before the pattern, so neither of these names it.
		{`{"mode":null}`, "body.mode: " + NotAString},
		{`{"mode":7}`, "body.mode: " + NotAString},
		{`{}`, "body.mode: " + FieldRequired},
	} {
		_, failures := readPattern(t, test.body)
		got := strings.Join(failures, " | ")
		if got != test.fails {
			t.Errorf("%s\n got %q\nwant %q", test.body, got, test.fails)
		}
	}
}

// pydantic checks min_length and max_length before the pattern, both of them:
// a value that is too short and also does not match names its length and not
// the pattern. Read off pydantic with a field carrying all three.
func TestALengthIsCheckedBeforeAPattern(t *testing.T) {
	lower := regexp.MustCompile("^[a-z]+$")
	for _, test := range []struct {
		value string
		fails string
	}{
		{"abcd", ""},
		{"ab", "String should have at least 4 characters"},
		{"AB", "String should have at least 4 characters"},
		{"abcdefgh", "String should have at most 6 characters"},
		{"ABCDEFGH", "String should have at most 6 characters"},
		{"ABCD", PatternMismatch(lower)},
	} {
		body := pythonjson.NewObject()
		body.Set("code", test.value)
		model := NewModel(body)
		model.TextMatching("code", 4, 6, lower)
		got := ""
		if !model.Valid() {
			got = model.Failures()[0].Message
		}
		if got != test.fails {
			t.Errorf("%q: got %q, want %q", test.value, got, test.fails)
		}
	}
}

// A partial-update model's pattern field: an omission is nothing, an explicit
// null is the None branch and skips the pattern, and a bad string still names
// it.
func TestAPartialUpdateReadsThePatternOnlyWhenTheFieldCarriesAString(t *testing.T) {
	for _, test := range []struct {
		body    string
		present bool
		value   string
		// null is "the field carries no value", which an omission and an
		// explicit null are both: Present is what tells them apart.
		null  bool
		fails string
	}{
		{`{}`, false, "", true, ""},
		{`{"mode":null}`, true, "", true, ""},
		{`{"mode":"explore"}`, true, "explore", false, ""},
		{`{"mode":"nope"}`, true, "nope", false, "body.mode: " + PatternMismatch(mode)},
		{`{"mode":7}`, true, "", true, "body.mode: " + NotAString},
	} {
		decoded, failure, err := Decode("application/json", []byte(test.body))
		if err != nil || failure != nil {
			t.Fatalf("the body did not decode: %v %v", err, failure)
		}
		model := NewModel(decoded)
		read := model.MaybeTextMatching("mode", 0, 0, mode)
		if read.Present != test.present {
			t.Errorf("%s: present %v, want %v", test.body, read.Present, test.present)
		}
		if (read.Value == nil) != test.null {
			t.Errorf("%s: null %v, want %v", test.body, read.Value == nil, test.null)
		}
		if read.Value != nil && *read.Value != test.value {
			t.Errorf("%s: value %q, want %q", test.body, *read.Value, test.value)
		}
		got := ""
		if !model.Valid() {
			got = strings.Join(model.Failures()[0].Location, ".") + ": " + model.Failures()[0].Message
		}
		if got != test.fails {
			t.Errorf("%s: got %q, want %q", test.body, got, test.fails)
		}
	}
}
