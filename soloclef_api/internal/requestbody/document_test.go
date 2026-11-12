package requestbody

import (
	"errors"
	"testing"
)

// The model-level refusals, each taken from the Python tier for the same body.
func TestTheBodyItselfCanBeTheFailure(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		refusal     string
	}{
		{"an empty body", "application/json", "", FieldRequired},
		{"an explicit null", "application/json", "null", FieldRequired},
		{"a number", "application/json", "7", NotAnObject},
		{"a string", "application/json", `"x"`, NotAnObject},
		{"a boolean", "application/json", "true", NotAnObject},
		{"an array", "application/json", "[]", NotAnObject},
		{"no content type", "", `{"name":"x"}`, NotAnObject},
		{"a form content type", "application/x-www-form-urlencoded", `{"name":"x"}`, NotAnObject},
		{"text/plain", "text/plain", `{"name":"x"}`, NotAnObject},
	}
	for _, test := range cases {
		object, failure, err := Decode(test.contentType, []byte(test.body))
		if err != nil {
			t.Errorf("%s: %v", test.name, err)
			continue
		}
		if object != nil || failure == nil {
			t.Errorf("%s: read an object", test.name)
			continue
		}
		if failure.Message != test.refusal {
			t.Errorf("%s: %q, want %q", test.name, failure.Message, test.refusal)
		}
		if len(failure.Location) != 1 || failure.Location[0] != BodyRoot {
			t.Errorf("%s: location %v, want [body]", test.name, failure.Location)
		}
	}
}

// A JSON content type with parameters, and any subtype ending in +json, are
// read as JSON; both were checked against the Python tier.
func TestWhichContentTypesAreReadAsJSON(t *testing.T) {
	for _, contentType := range []string{
		"application/json",
		"application/json; charset=utf-8",
		"application/vnd.x+json",
	} {
		object, failure, err := Decode(contentType, []byte(`{"name":"x"}`))
		if err != nil || failure != nil || object == nil {
			t.Errorf("%s was not read as JSON (%v, %v)", contentType, failure, err)
		}
	}
}

// A body that is not JSON is reported as such, for Middleware to refuse before
// the route runs. These are the bodies that take that path.
func TestABodyThatIsNotJSONIsUndecodable(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{"an unclosed object", "{"},
		{"a value cut short", `{"name":`},
		{"a trailing comma", `{"name":"x",}`},
		{"a bare word", "nope"},
		{"data after the value", `{"name":"x"} extra`},
		{"whitespace alone", "   "},
		{"a raw tab inside a string", "{\"name\":\"a\tb\"}"},
		// pydantic-core's parser took NaN and then refused it as a field
		// value; it is not JSON, so it is refused as such, once, here.
		{"NaN", `{"name":"x","is_default":NaN}`},
	} {
		_, _, err := Decode("application/json", []byte(test.body))
		if !errors.Is(err, ErrUndecodable) {
			t.Errorf("%s: %v, want ErrUndecodable", test.name, err)
		}
	}
}

// Bytes that are not UTF-8 are not text at all, which is a different refusal
// (a 400) from text that is not JSON (a 422).
func TestABodyThatIsNotUTF8IsNotText(t *testing.T) {
	_, _, err := Decode("application/json", []byte("{\"name\":\"\xff\"}"))
	if !errors.Is(err, ErrNotUTF8) {
		t.Errorf("got %v, want ErrNotUTF8", err)
	}
}

// Both tiers keep the last of two identical keys, which is Python dict
// assignment and this package's Object.Set.
func TestADuplicateKeyKeepsTheLastValue(t *testing.T) {
	object, _, err := Decode("application/json", []byte(`{"name":"a","name":"b"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := NewModel(object).Text("name", 1, 100); got != "b" {
		t.Errorf("read %q, want %q", got, "b")
	}
}
