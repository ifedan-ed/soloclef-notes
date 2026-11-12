package documentdeletion

import (
	"errors"
	"testing"
)

// The status column is jsonb and nothing constrains its shape, so what the
// Python route reads out of it is a Python expression with a truthiness test
// in it. Each case below is a row that exists in a database somewhere.
func TestStateOfReadsWhatPythonReadsOutOfTheStatusColumn(t *testing.T) {
	cases := map[string]struct {
		status string
		want   string
	}{
		"the state a document is in":        {`{"state": "ready"}`, "ready"},
		"a state with other keys beside it": {`{"state": "processing", "progress": 3}`, "processing"},
		"an object with no state":           {`{"progress": 3}`, ""},
		"an empty object":                   {`{}`, ""},
		"an empty array, which is falsy":    {`[]`, ""},
		"a zero, which is falsy":            {`0`, ""},
		"an empty string, which is falsy":   {`""`, ""},
		"false":                             {`false`, ""},
		"null":                              {`null`, ""},
		"a state that is not a string":      {`{"state": 3}`, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			state, err := StateOf([]byte(c.status))
			if err != nil {
				t.Fatalf("StateOf(%s): %v", c.status, err)
			}
			if state != c.want {
				t.Errorf("StateOf(%s) = %q, want %q", c.status, state, c.want)
			}
		})
	}
}

// A truthy status that is not an object is the one shape Python cannot read:
// .get is called on it and raises, and the route answers 500. Answering
// "ready" instead would delete a document on one tier and not the other.
func TestATruthyNonObjectStatusIsTheFailurePythonHas(t *testing.T) {
	for _, status := range []string{`5`, `"ready"`, `true`, `["ready"]`} {
		if _, err := StateOf([]byte(status)); !errors.Is(err, ErrStatusIsNotAnObject) {
			t.Errorf("StateOf(%s) = %v, want a refusal", status, err)
		}
	}
}
