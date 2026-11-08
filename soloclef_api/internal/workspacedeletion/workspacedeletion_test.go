package workspacedeletion

import (
	"testing"
	"unicode/utf8"
)

// The column is 100 characters wide and the marker takes eleven of them, so
// what is kept is the first 89 characters — characters, because Python slices
// a str and the names people give workspaces are not ASCII.
func TestTheMarkerLeavesAColumnsWorthOfTheName(t *testing.T) {
	cases := map[string]struct{ name, want string }{
		"a short name": {"Notes", "[DELETING] Notes"},
		"a name exactly as long as the room": {
			repeat("a", 89), Marker + repeat("a", 89),
		},
		"a name one character too long": {
			repeat("a", 90), Marker + repeat("a", 89),
		},
		"a name of three-byte characters": {
			repeat("—", 90), Marker + repeat("—", 89),
		},
		"an empty name": {"", Marker},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			marked := MarkedName(c.name)
			if marked != c.want {
				t.Errorf("MarkedName = %q", marked)
			}
			if count := utf8.RuneCountInString(marked); count > NameLimit {
				t.Errorf("%d characters, and the column holds %d", count, NameLimit)
			}
		})
	}
}

// The marker is also the refusal: a second delete of the same workspace is
// told it is already being deleted rather than renaming the name twice.
func TestAMarkedWorkspaceIsAlreadyBeingDeleted(t *testing.T) {
	cases := map[string]bool{
		"[DELETING] Notes":  true,
		"[DELETING] ":       true,
		"[DELETING]":        false,
		"Notes":             false,
		"":                  false,
		"My [DELETING] one": false,
	}
	for name, want := range cases {
		if got := BeingDeleted(name); got != want {
			t.Errorf("BeingDeleted(%q) = %v", name, got)
		}
	}
}

// A marked name is refused a second time, which is what keeps the marker from
// being prefixed onto itself until the column overflows.
func TestMarkingTwiceIsNotHowTheRouteBehaves(t *testing.T) {
	if !BeingDeleted(MarkedName("Notes")) {
		t.Error("a marked name is not recognised as marked")
	}
}

func repeat(of string, times int) string {
	out := make([]rune, 0, times)
	for range times {
		out = append(out, []rune(of)...)
	}
	return string(out)
}
