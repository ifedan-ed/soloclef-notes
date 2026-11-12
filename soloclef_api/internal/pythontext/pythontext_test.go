package pythontext

import "testing"

// The whole reason this package is not strings.TrimSpace. Python's
// str.isspace() is the Unicode White_Space property plus the four C0
// separators, and Go's unicode.IsSpace is only the first half — so a value
// wrapped in U+001C is stripped there and kept here.
//
// It is reachable from a request on two routes: a memory document wrapped in
// U+001C is stripped to its heading (internal/markdownmemory) and a rule's
// permission of "tool:x\u001c" is stripped before it is matched against the
// permission pattern (internal/agentrules), so trimming it the Go way is a 400
// where the Python tier answers 201.
func TestStripRemovesTheSeparatorsGoDoesNot(t *testing.T) {
	for _, separator := range []rune{0x1c, 0x1d, 0x1e, 0x1f} {
		text := string(separator) + "tool:x" + string(separator)
		if got := Strip(text); got != "tool:x" {
			t.Errorf("Strip(%q) = %q, want %q", text, got, "tool:x")
		}
	}
	for _, space := range []string{" ", "\t", "\n", "\v", "\f", "\r", " ", " ", "　"} {
		text := space + "x" + space
		if got := Strip(text); got != "x" {
			t.Errorf("Strip(%q) = %q, want %q", text, got, "x")
		}
	}
	// A value that is nothing but whitespace strips to the empty string, which
	// is what makes " " a 400 on the rule create and not a 422: min_length=1
	// passes and the stripped value then matches no permission.
	if got := Strip(" \u001c\t "); got != "" {
		t.Errorf("Strip of only whitespace = %q, want the empty string", got)
	}
	// Nothing inside is touched, because strip is not a replace.
	if got := Strip(" a b "); got != "a b" {
		t.Errorf("Strip(%q) = %q, want %q", " a b ", got, "a b")
	}
}

func TestRightStripLeavesTheLeadingWhitespace(t *testing.T) {
	if got := RightStrip(" x \u001c"); got != " x" {
		t.Errorf("RightStrip(%q) = %q, want %q", " x \u001c", got, " x")
	}
}

// The characters Python counts and Go does not, asked one at a time, because
// IsSpace is the predicate both functions above are built from.
func TestIsSpaceIsStrIsspace(t *testing.T) {
	for _, character := range []rune{' ', '\t', '\n', '\v', '\f', '\r', 0x1c, 0x1d, 0x1e, 0x1f, 0x85, 0xa0, 0x2028, 0x2029, 0x3000} {
		if !IsSpace(character) {
			t.Errorf("IsSpace(%#U) = false, want true", character)
		}
	}
	for _, character := range []rune{'a', '0', '*', ':', 0x200b, 0x0} {
		if IsSpace(character) {
			t.Errorf("IsSpace(%#U) = true, want false", character)
		}
	}
}
