package requestbody

import (
	"reflect"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// The vectors came out of the model itself — UserUpdate, with
// `extra="forbid"` and `display_name`/`avatar_url` declared — rather than off
// pydantic's documentation, because the thing in question is the order two
// kinds of failure come in:
//
//	{"nope": 1}                                -> nope
//	{"display_name": 5, "nope": 1, "zz": 2}    -> display_name, nope, zz
//	{"nope": 1, "display_name": 5}             -> display_name, nope
//
// The last line is the one worth reading twice: the declared field's failure
// comes first although the request wrote the extra first, and the extras keep
// the *request's* order among themselves.
func TestRefuseExtrasReportsEveryUndeclaredKeyAfterTheDeclaredFields(t *testing.T) {
	for _, each := range []struct {
		name  string
		body  []string
		paths [][]string
	}{
		{
			name:  "one extra",
			body:  []string{"nope"},
			paths: [][]string{{"body", "nope"}},
		},
		{
			name:  "two extras keep the request's order",
			body:  []string{"zz", "nope"},
			paths: [][]string{{"body", "zz"}, {"body", "nope"}},
		},
		{
			name:  "a declared field is not an extra",
			body:  []string{"display_name", "nope"},
			paths: [][]string{{"body", "nope"}},
		},
		{
			name:  "nothing undeclared is nothing reported",
			body:  []string{"display_name", "avatar_url"},
			paths: nil,
		},
	} {
		t.Run(each.name, func(t *testing.T) {
			object := pythonjson.NewObject()
			for _, key := range each.body {
				object.Set(key, "x")
			}
			model := NewModel(object)
			model.RefuseExtras("display_name", "avatar_url")

			var got [][]string
			for _, failure := range model.Failures() {
				if failure.Message != ExtraForbidden {
					t.Errorf("message = %q, want %q", failure.Message, ExtraForbidden)
				}
				got = append(got, failure.Location)
			}
			if !reflect.DeepEqual(got, each.paths) {
				t.Errorf("locations = %v, want %v", got, each.paths)
			}
		})
	}
}

// The declared field's failure comes first because RefuseExtras is called last,
// which is the contract the call sites have to keep. This is that order asked
// as one model.
func TestADeclaredFieldsFailureComesBeforeAnExtras(t *testing.T) {
	object := pythonjson.NewObject()
	object.Set("nope", int64(1))
	object.Set("display_name", int64(5))

	model := NewModel(object)
	model.MaybeText("display_name", 0, 0)
	model.RefuseExtras("display_name", "avatar_url")

	failures := model.Failures()
	if len(failures) != 2 {
		t.Fatalf("got %d failures, want 2: %v", len(failures), failures)
	}
	if failures[0].Message != NotAString || failures[0].Location[1] != "display_name" {
		t.Errorf("first failure = %v, want the declared field's", failures[0])
	}
	if failures[1].Message != ExtraForbidden || failures[1].Location[1] != "nope" {
		t.Errorf("second failure = %v, want the extra's", failures[1])
	}
}

// A `str` field with a default is three answers and not two: pydantic does not
// validate a default it supplied, so an absent field skips the length check
// that an empty string fails.
func TestTextOrReadsADefaultWithoutValidatingIt(t *testing.T) {
	absent := NewModel(pythonjson.NewObject())
	if got := absent.TextOr("pattern", "*", 1, 255); got != "*" {
		t.Errorf("an absent field = %q, want the fallback", got)
	}
	if !absent.Valid() {
		t.Errorf("an absent field was refused: %v", absent.Failures())
	}

	nulled := pythonjson.NewObject()
	nulled.Set("pattern", nil)
	null := NewModel(nulled)
	null.TextOr("pattern", "*", 1, 255)
	if null.Valid() || null.Failures()[0].Message != NotAString {
		t.Errorf("an explicit null = %v, want %q", null.Failures(), NotAString)
	}

	emptied := pythonjson.NewObject()
	emptied.Set("pattern", "")
	empty := NewModel(emptied)
	empty.TextOr("pattern", "*", 1, 255)
	if empty.Valid() || empty.Failures()[0].Message != "String should have at least 1 character" {
		t.Errorf("an empty string = %v, want the minimum-length refusal", empty.Failures())
	}
}
