package celery

import "testing"

func TestPyJSONSpellsValuesAsPythonDoes(t *testing.T) {
	cases := []struct {
		value any
		want  string
	}{
		{nil, "null"},
		{true, "true"},
		{"plain", `"plain"`},
		{"quote\" and \\", `"quote\" and \\"`},
		{"tab\there\n", `"tab\there\n"`},
		{"caf\u00e9", "\"caf\\u00e9\""},
		{"\U0001f600", "\"\\ud83d\\ude00\""},
		{1, "1"},
		{[]any{1, "two"}, `[1, "two"]`},
		{map[string]any{"b": 2, "a": nil}, `{"a": null, "b": 2}`},
	}
	for _, tc := range cases {
		got, err := pyJSON(tc.value)
		if err != nil {
			t.Fatalf("pyJSON(%#v): %v", tc.value, err)
		}
		if got != tc.want {
			t.Errorf("pyJSON(%#v) = %s, want %s", tc.value, got, tc.want)
		}
	}
}

func TestPyReprSpellsValuesAsPythonsReprDoes(t *testing.T) {
	cases := []struct {
		value any
		want  string
	}{
		// A tuple's repr is not a list's, and a tuple of one carries the
		// comma that says so: `.delay(7)` writes "(7,)".
		{pyTuple{}, "()"},
		{pyTuple{7}, "(7,)"},
		{pyTuple{7, "two"}, `(7, 'two')`},
		{nil, "None"},
		{true, "True"},
		{false, "False"},
		{"plain", `'plain'`},
		{"it's", `"it's"`},
		{`it's "both"`, `'it\'s "both"'`},
		{[]any{1, "two"}, `[1, 'two']`},
		{map[string]any{"k": true}, `{'k': True}`},
	}
	for _, tc := range cases {
		got, err := pyRepr(tc.value)
		if err != nil {
			t.Fatalf("pyRepr(%#v): %v", tc.value, err)
		}
		if got != tc.want {
			t.Errorf("pyRepr(%#v) = %s, want %s", tc.value, got, tc.want)
		}
	}
}

func TestPyJSONRefusesWhatItCannotSpell(t *testing.T) {
	if _, err := pyJSON(struct{ A int }{1}); err == nil {
		t.Fatal("expected an error for a type no envelope carries")
	}
}
