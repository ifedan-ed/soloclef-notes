package markdownmemory

import (
	"reflect"
	"testing"
)

// Python's str.strip() removes the four C0 separators that Go's
// unicode.IsSpace does not, which is why pythonStrip is not
// strings.TrimSpace. The Python tier strips a document wrapped in U+001C down
// to its heading; a document wrapped in U+001C would otherwise be a line of
// prose with no heading and refused.
func TestPythonStripRemovesTheSeparatorsGoDoesNot(t *testing.T) {
	for _, separator := range []rune{0x1c, 0x1d, 0x1e, 0x1f} {
		text := string(separator) + "## H" + string(separator)
		if got := pythonStrip(text); got != "## H" {
			t.Errorf("pythonStrip(%q) = %q, want %q", text, got, "## H")
		}
	}
	for _, space := range []string{" ", "\t", "\n", "\v", "\f", "\r", "\u00a0", "\u2028", "\u3000"} {
		text := space + "x" + space
		if got := pythonStrip(text); got != "x" {
			t.Errorf("pythonStrip(%q) = %q, want %q", text, got, "x")
		}
	}
}

// str.splitlines() breaks on ten boundaries, not one. Each of these was sent to
// the Python tier inside a memory document and came back as two lines.
func TestPythonSplitLinesBreaksWhereStrSplitLinesBreaks(t *testing.T) {
	for _, boundary := range []string{"\n", "\v", "\f", "\r", "\u001c", "\u001d", "\u001e", "\u0085", "\u2028", "\u2029"} {
		got := pythonSplitLines("a" + boundary + "b")
		if !reflect.DeepEqual(got, []string{"a", "b"}) {
			t.Errorf("pythonSplitLines(%q) = %q, want [a b]", "a"+boundary+"b", got)
		}
	}
	if got := pythonSplitLines("a\r\nb"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf(`pythonSplitLines("a\r\nb") = %q, want [a b]`, got)
	}
	if got := pythonSplitLines("a\n"); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("a trailing boundary yields no empty line: %q", got)
	}
	if got := pythonSplitLines("a\n\nb"); !reflect.DeepEqual(got, []string{"a", "", "b"}) {
		t.Errorf("a blank line between two is kept: %q", got)
	}
	if got := pythonSplitLines(""); len(got) != 0 {
		t.Errorf("an empty document is no lines: %q", got)
	}
}

// str.isalnum() is what normalize_heading keeps, and it counts the numeric
// categories as well as the letters — so "Preferences²" normalises with that
// character kept and still is not the forbidden heading.
func TestPythonIsAlphanumericCountsTheNumericCategories(t *testing.T) {
	for _, character := range []rune{'a', 'Z', '7', 0x00e9, 0x00b2, 0x3007, 0x0662} {
		if !pythonIsAlphanumeric(character) {
			t.Errorf("pythonIsAlphanumeric(%q) = false, want true", character)
		}
	}
	for _, character := range []rune{' ', '-', '!', 0x2014, 0x00a0} {
		if pythonIsAlphanumeric(character) {
			t.Errorf("pythonIsAlphanumeric(%q) = true, want false", character)
		}
	}
}

// The normalisation the forbidden-heading rule compares. The last two came out
// of the Python tier: "## personal   instructions!" is refused in team memory
// and "## Pre-fer-ences" is not, because the first normalises onto a forbidden
// heading and the second does not.
func TestNormaliseHeadingIsNormalizeHeading(t *testing.T) {
	for heading, want := range map[string]string{
		"Preferences":              "preferences",
		"  Personal   Notes!  ":    "personal notes",
		"personal   instructions!": "personal instructions",
		"Pre-fer-ences":            "pre fer ences",
		"":                         "",
		"---":                      "",
	} {
		if got := normaliseHeading(heading); got != want {
			t.Errorf("normaliseHeading(%q) = %q, want %q", heading, got, want)
		}
	}
}
