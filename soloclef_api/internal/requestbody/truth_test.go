package requestbody

import "testing"

// Every vector here came out of the Python tier: the same JSON body was posted
// to its own route and the answer read off, value by value. They look
// excessive for reading a boolean and they are what pydantic does — the three
// outcomes are not one rule, and the two refusals are not interchangeable.
func TestPydanticsLaxBoolean(t *testing.T) {
	cases := []struct {
		body    string
		want    bool
		refusal string
	}{
		{body: `{"is_default":true}`, want: true},
		{body: `{"is_default":false}`, want: false},

		{body: `{"is_default":"yes"}`, want: true},
		{body: `{"is_default":"no"}`, want: false},
		{body: `{"is_default":"on"}`, want: true},
		{body: `{"is_default":"off"}`, want: false},
		{body: `{"is_default":"t"}`, want: true},
		{body: `{"is_default":"f"}`, want: false},
		{body: `{"is_default":"y"}`, want: true},
		{body: `{"is_default":"n"}`, want: false},
		{body: `{"is_default":"TRUE"}`, want: true},
		{body: `{"is_default":"False"}`, want: false},
		{body: `{"is_default":"Y"}`, want: true},
		{body: `{"is_default":"ON"}`, want: true},
		{body: `{"is_default":"Off"}`, want: false},
		{body: `{"is_default":"yEs"}`, want: true},
		{body: `{"is_default":"0"}`, want: false},
		{body: `{"is_default":"1"}`, want: true},

		{body: `{"is_default":1}`, want: true},
		{body: `{"is_default":0}`, want: false},
		{body: `{"is_default":1.0}`, want: true},
		{body: `{"is_default":0.0}`, want: false},
		{body: `{"is_default":-0.0}`, want: false},

		// A whole number that names no truth, and a word that names none: the
		// value was of a kind a boolean can be read from and still could not be.
		{body: `{"is_default":2}`, refusal: BooleanUnreadable},
		{body: `{"is_default":-1}`, refusal: BooleanUnreadable},
		{body: `{"is_default":2.0}`, refusal: BooleanUnreadable},
		{body: `{"is_default":"maybe"}`, refusal: BooleanUnreadable},
		{body: `{"is_default":""}`, refusal: BooleanUnreadable},
		{body: `{"is_default":" true "}`, refusal: BooleanUnreadable},
		{body: `{"is_default":"00"}`, refusal: BooleanUnreadable},
		{body: `{"is_default":"01"}`, refusal: BooleanUnreadable},

		// A value of a kind no boolean can be read from at all. The fractional
		// float belongs here and the whole one above, which is the difference
		// the two messages carry.
		{body: `{"is_default":0.5}`, refusal: NotABoolean},
		{body: `{"is_default":null}`, refusal: NotABoolean},
		{body: `{"is_default":[]}`, refusal: NotABoolean},
		{body: `{"is_default":{}}`, refusal: NotABoolean},
	}

	for _, test := range cases {
		object, failure, err := Decode("application/json", []byte(test.body))
		if err != nil || failure != nil {
			t.Fatalf("%s: the body did not decode (%v, %v)", test.body, failure, err)
		}
		model := NewModel(object)
		got := model.Truth("is_default", false)

		if test.refusal == "" {
			if !model.Valid() {
				t.Errorf("%s: refused with %v", test.body, model.Failures())
				continue
			}
			if got != test.want {
				t.Errorf("%s: read %v, want %v", test.body, got, test.want)
			}
			continue
		}
		failures := model.Failures()
		if len(failures) != 1 || failures[0].Message != test.refusal {
			t.Errorf("%s: refusals %v, want one %q", test.body, failures, test.refusal)
			continue
		}
		if want := []string{"body", "is_default"}; len(failures[0].Location) != 2 ||
			failures[0].Location[0] != want[0] || failures[0].Location[1] != want[1] {
			t.Errorf("%s: location %v, want %v", test.body, failures[0].Location, want)
		}
	}
}

// An absent field is the model's default, and not a failure.
func TestAnAbsentBooleanIsItsDefault(t *testing.T) {
	object, _, err := Decode("application/json", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(object)
	if !model.Truth("is_default", true) || !model.Valid() {
		t.Error("an absent field did not fall back to its default")
	}
}
