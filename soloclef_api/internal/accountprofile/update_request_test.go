package accountprofile

import "testing"

// The three answers a field can give on a partial-update model, and the one
// that is new here: an undeclared key is refused rather than ignored, because
// UserUpdate is the API's first model with `extra="forbid"`.
//
// The messages and the order came out of the model itself rather than off
// pydantic's documentation (internal/requestbody.RefuseExtras has the
// vectors): the declared fields' failures first in declaration order, then the
// extras in the order the request wrote them.
func TestReadChangesIsUserUpdate(t *testing.T) {
	const json = "application/json"

	t.Run("a mention and an omission are different writes", func(t *testing.T) {
		changes, failures, err := ReadChanges(json, []byte(`{"display_name":"Ada"}`))
		if err != nil || len(failures) > 0 {
			t.Fatalf("refused a good body: %v %v", failures, err)
		}
		if !changes.DisplayName.Present || changes.DisplayName.Value == nil ||
			*changes.DisplayName.Value != "Ada" {
			t.Errorf("display_name = %+v, want the mentioned value", changes.DisplayName)
		}
		if changes.AvatarURL.Present {
			t.Errorf("avatar_url was not mentioned and reads as present")
		}
		if !changes.Mentions() {
			t.Errorf("a body that mentions one field mentions something")
		}
	})

	t.Run("an explicit null is a value and writes NULL", func(t *testing.T) {
		changes, failures, err := ReadChanges(json, []byte(`{"avatar_url":null}`))
		if err != nil || len(failures) > 0 {
			t.Fatalf("refused a good body: %v %v", failures, err)
		}
		if !changes.AvatarURL.Present || changes.AvatarURL.Value != nil {
			t.Errorf("avatar_url = %+v, want present with no value", changes.AvatarURL)
		}
	})

	t.Run("an empty body writes nothing", func(t *testing.T) {
		changes, failures, err := ReadChanges(json, []byte(`{}`))
		if err != nil || len(failures) > 0 {
			t.Fatalf("refused an empty body: %v %v", failures, err)
		}
		if changes.Mentions() {
			t.Errorf("an empty body mentions nothing")
		}
	})

	t.Run("a number is not a string", func(t *testing.T) {
		_, failures, err := ReadChanges(json, []byte(`{"display_name":5}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(failures) != 1 || failures[0].Message != "Input should be a valid string" {
			t.Fatalf("failures = %v", failures)
		}
	})

	// The two fields the model deliberately does not declare, which is the
	// whole reason it forbids extras: a client that tries to change its own
	// address learns that it did not.
	t.Run("email and password are refused rather than ignored", func(t *testing.T) {
		for _, body := range []string{
			`{"email":"other@example.com"}`,
			`{"password":"hunter2"}`,
		} {
			_, failures, err := ReadChanges(json, []byte(body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(failures) != 1 || failures[0].Message != "Extra inputs are not permitted" {
				t.Errorf("%s -> %v, want the extra-forbidden refusal", body, failures)
			}
		}
	})

	t.Run("a declared field's failure comes before an extra's", func(t *testing.T) {
		_, failures, err := ReadChanges(json, []byte(`{"nope":1,"display_name":5,"zz":2}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(failures) != 3 {
			t.Fatalf("got %d failures, want 3: %v", len(failures), failures)
		}
		for index, want := range []string{"display_name", "nope", "zz"} {
			if failures[index].Location[1] != want {
				t.Errorf("failure %d names %q, want %q", index, failures[index].Location[1], want)
			}
		}
	})
}
