package agentrules

import "testing"

// `_validate_permission_string(payload.permission.strip())` split in two, and
// the half worth pinning is the strip: it is Python's and not
// strings.TrimSpace, so a value wrapped in one of the four C0 separators is
// stored trimmed there and would have failed the pattern here
// (internal/pythontext).
func TestStoredPermissionStripsBeforeItMatches(t *testing.T) {
	for _, each := range []struct {
		requested  string
		stored     string
		wellFormed bool
	}{
		{requested: "tool:create_google_drive_file", stored: "tool:create_google_drive_file", wellFormed: true},
		{requested: "  tool:x  ", stored: "tool:x", wellFormed: true},
		{requested: "\u001ctool:x\u001c", stored: "tool:x", wellFormed: true},
		{requested: " tool:x ", stored: "tool:x", wellFormed: true},
		{requested: "*", stored: "*", wellFormed: true},
		{requested: "a.b-c_d:e*", stored: "a.b-c_d:e*", wellFormed: true},
		// min_length=1 passes because the spaces are characters; the strip
		// then leaves nothing, and nothing matches a pattern needing one.
		{requested: "   ", stored: "", wellFormed: false},
		{requested: "tool x", stored: "tool x", wellFormed: false},
		{requested: "tool/x", stored: "tool/x", wellFormed: false},
		{requested: "tool\nx", stored: "tool\nx", wellFormed: false},
		{requested: "tool:é", stored: "tool:é", wellFormed: false},
		{requested: "tool:\U0001F600", stored: "tool:\U0001F600", wellFormed: false},
	} {
		stored, wellFormed := StoredPermission(each.requested)
		if stored != each.stored || wellFormed != each.wellFormed {
			t.Errorf("StoredPermission(%q) = (%q, %v), want (%q, %v)",
				each.requested, stored, wellFormed, each.stored, each.wellFormed)
		}
	}
}

// `payload.pattern.strip() or "*"`, which is the one place a validated value
// is replaced rather than refused.
func TestStoredPatternFallsBackToTheWildcard(t *testing.T) {
	for requested, want := range map[string]string{
		"production-*":  "production-*",
		"  staging-*  ": "staging-*",
		"   ":           DefaultPattern,
		"\u001c":        DefaultPattern,
		"*":             "*",
		// Anything is a pattern: it is matched against a tool argument at
		// agent build time and nothing validates its shape.
		"a b/c": "a b/c",
	} {
		if got := StoredPattern(requested); got != want {
			t.Errorf("StoredPattern(%q) = %q, want %q", requested, got, want)
		}
	}
}

// A new rule's body, field by field. `pattern` is a string with a default,
// which is three answers and not two.
func TestReadingANewRule(t *testing.T) {
	const json = "application/json"

	// The body below names a user_id, as an older app did. It is read past:
	// Requested has no such field, so nothing can pass it on.
	t.Run("the whole body", func(t *testing.T) {
		requested, failures, wide, err := ReadRequest(json, []byte(
			`{"permission":"tool:x","pattern":"p-*","action":"deny",`+
				`"user_id":"11111111-1111-1111-1111-111111111111","thread_id":7}`))
		if err != nil || len(failures) > 0 || wide {
			t.Fatalf("refused a good body: %v %v %v", failures, wide, err)
		}
		if requested.Permission != "tool:x" || requested.Pattern != "p-*" ||
			requested.Action != "deny" {
			t.Errorf("read %+v", requested)
		}
		if requested.ThreadID == nil || *requested.ThreadID != 7 {
			t.Errorf("thread_id = %v", requested.ThreadID)
		}
	})

	t.Run("an absent pattern is the model's default and is not length-checked", func(t *testing.T) {
		requested, failures, _, err := ReadRequest(json, []byte(`{"permission":"p","action":"ask"}`))
		if err != nil || len(failures) > 0 {
			t.Fatalf("refused a good body: %v %v", failures, err)
		}
		if requested.Pattern != DefaultPattern {
			t.Errorf("pattern = %q, want the default", requested.Pattern)
		}
	})

	t.Run("an explicit null pattern is not the default", func(t *testing.T) {
		_, failures, _, err := ReadRequest(json, []byte(`{"permission":"p","pattern":null,"action":"ask"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(failures) != 1 || failures[0].Message != "Input should be a valid string" {
			t.Errorf("failures = %v", failures)
		}
	})

	t.Run("an empty pattern is the minimum-length refusal", func(t *testing.T) {
		_, failures, _, err := ReadRequest(json, []byte(`{"permission":"p","pattern":"","action":"ask"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(failures) != 1 || failures[0].Message != "String should have at least 1 character" {
			t.Errorf("failures = %v", failures)
		}
	})

	t.Run("the action names every member in declaration order", func(t *testing.T) {
		_, failures, _, err := ReadRequest(json, []byte(`{"permission":"p","action":"maybe"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "Input should be 'allow', 'deny' or 'ask'"
		if len(failures) != 1 || failures[0].Message != want {
			t.Errorf("failures = %v, want %q", failures, want)
		}
	})

	t.Run("three bad fields come back in the model's order", func(t *testing.T) {
		_, failures, _, err := ReadRequest(json, []byte(
			`{"action":5,"thread_id":"seven","permission":null}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(failures) != 3 {
			t.Fatalf("got %d failures, want 3: %v", len(failures), failures)
		}
		for index, want := range []string{"permission", "action", "thread_id"} {
			if failures[index].Location[1] != want {
				t.Errorf("failure %d names %q, want %q", index, failures[index].Location[1], want)
			}
		}
	})

	t.Run("a thread id no int64 holds is read, and reported as out of range", func(t *testing.T) {
		_, failures, wide, err := ReadRequest(json, []byte(
			`{"permission":"p","action":"ask","thread_id":22000000000000000000}`))
		if err != nil || len(failures) > 0 {
			t.Fatalf("refused the body: %v %v", failures, err)
		}
		if !wide {
			t.Errorf("a twenty-digit thread id was not reported as out of range")
		}
	})
}

// An update's body: an explicit null is the same request as an omission.
func TestReadChangesTreatsNullAsAnOmission(t *testing.T) {
	const json = "application/json"

	for _, body := range []string{`{}`, `{"pattern":null,"action":null}`} {
		changes, failures, err := ReadChanges(json, []byte(body))
		if err != nil || len(failures) > 0 {
			t.Fatalf("%s refused: %v %v", body, failures, err)
		}
		if changes.Mentions() {
			t.Errorf("%s mentions nothing and reads as mentioning something", body)
		}
	}

	changes, failures, err := ReadChanges(json, []byte(`{"action":"allow"}`))
	if err != nil || len(failures) > 0 {
		t.Fatalf("refused a good body: %v %v", failures, err)
	}
	if changes.Action == nil || *changes.Action != "allow" || changes.Pattern != nil {
		t.Errorf("read %+v", changes)
	}
}

// The two settings the flag guard reads, and that the kill-switch wins.
func TestAvailabilityIsTheFlagGuard(t *testing.T) {
	for _, each := range []struct {
		available Availability
		serves    bool
	}{
		{available: Availability{Disabled: false, PermissionEnabled: true}, serves: true},
		{available: Availability{Disabled: false, PermissionEnabled: false}, serves: false},
		{available: Availability{Disabled: true, PermissionEnabled: true}, serves: false},
		{available: Availability{Disabled: true, PermissionEnabled: false}, serves: false},
	} {
		if got := each.available.Serves(); got != each.serves {
			t.Errorf("%+v.Serves() = %v, want %v", each.available, got, each.serves)
		}
	}
}
