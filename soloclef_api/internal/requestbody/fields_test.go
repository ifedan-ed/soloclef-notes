package requestbody

import (
	"strings"
	"testing"
)

// The field-level refusals, and the messages the Python tier words them with.
// The plural is pydantic's own: an empty name is refused with "1 character".
func TestTheFieldRefusalsAreThePythonTiersWords(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		refusals []string
		location []string
	}{
		{"a missing name", `{}`, []string{FieldRequired}, []string{"body", "name"}},
		{"a null name", `{"name":null}`, []string{NotAString}, []string{"body", "name"}},
		{"a numeric name", `{"name":7}`, []string{NotAString}, []string{"body", "name"}},
		{"an empty name", `{"name":""}`,
			[]string{"String should have at least 1 character"}, []string{"body", "name"}},
		{"a name too long", `{"name":"` + strings.Repeat("n", 101) + `"}`,
			[]string{"String should have at most 100 characters"}, []string{"body", "name"}},
	}
	for _, test := range cases {
		object, _, err := Decode("application/json", []byte(test.body))
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		model := NewModel(object)
		model.Text("name", 1, 100)
		got := model.Failures()
		if len(got) != len(test.refusals) || got[0].Message != test.refusals[0] {
			t.Errorf("%s: %v, want %v", test.name, got, test.refusals)
			continue
		}
		if strings.Join(got[0].Location, "/") != strings.Join(test.location, "/") {
			t.Errorf("%s: location %v, want %v", test.name, got[0].Location, test.location)
		}
	}
}

// A name of exactly the limit passes, and the count is of characters rather
// than bytes: Python's len() of a str counts code points.
func TestLengthIsCountedInCharacters(t *testing.T) {
	object, _, _ := Decode("application/json", []byte(`{"name":"`+strings.Repeat("é", 100)+`"}`))
	model := NewModel(object)
	model.Text("name", 1, 100)
	if !model.Valid() {
		t.Errorf("a hundred two-byte characters were refused: %v", model.Failures())
	}
}

// description is str | None with a default of None: absent and null are both
// None, and neither is a failure.
func TestAnOptionalStringTakesNullForAbsent(t *testing.T) {
	for _, body := range []string{`{}`, `{"description":null}`} {
		object, _, _ := Decode("application/json", []byte(body))
		model := NewModel(object)
		if got := model.OptionalText("description", 500); got != nil {
			t.Errorf("%s: read %q, want nothing", body, *got)
		}
		if !model.Valid() {
			t.Errorf("%s: refused with %v", body, model.Failures())
		}
	}
}

// A list's bad item is refused under its own index, which pydantic reports as a
// further part of the location — and as text, because app.py renders every part
// of a location with str().
func TestABadListItemIsRefusedUnderItsIndex(t *testing.T) {
	object, _, _ := Decode("application/json", []byte(`{"permissions":["roles:read",1,2]}`))
	model := NewModel(object)
	model.TextList("permissions")
	failures := model.Failures()
	if len(failures) != 2 {
		t.Fatalf("%d refusals, want 2: %v", len(failures), failures)
	}
	for index, want := range []string{"1", "2"} {
		location := failures[index].Location
		if len(location) != 3 || location[2] != want {
			t.Errorf("refusal %d: location %v, want [body permissions %s]", index, location, want)
		}
		if failures[index].Message != NotAString {
			t.Errorf("refusal %d: %q", index, failures[index].Message)
		}
	}
}

// A list field that is not a list is refused once, and null is one of those:
// the default is an empty list, not None.
func TestAListFieldThatIsNotAList(t *testing.T) {
	for _, body := range []string{`{"permissions":"documents:read"}`, `{"permissions":null}`} {
		object, _, _ := Decode("application/json", []byte(body))
		model := NewModel(object)
		model.TextList("permissions")
		failures := model.Failures()
		if len(failures) != 1 || failures[0].Message != NotAList {
			t.Errorf("%s: %v, want one %q", body, failures, NotAList)
		}
	}
}
