package tablesafe

import (
	"strings"
	"testing"
)

// The answers below are CPython's, read out of the backend container:
//
//	python -c "print([hex(c) for c in range(0x3001) if chr(c).isspace()])"
//	python -c "print([hex(c) for c in range(0x3001) if len(('a'+chr(c)+'b').splitlines())>1])"
//
// The two sets are different and the difference is not a mistake: U+001C to
// U+001E end a line AND are whitespace, U+001F is whitespace and does not end
// a line, and U+00A0 is whitespace and does not end a line either.

func TestStripIsPythonsStripAndNotGos(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"nothing to strip", "abc", "abc"},
		{"ordinary spaces", "  abc \n", "abc"},
		// strings.TrimSpace leaves these: unicode.IsSpace says they are not
		// whitespace, and Python says they are. A document carrying one would
		// be stripped to a different length, moving every chunk after it.
		{"information separators", "\x1c\x1d\x1e\x1fabc\x1f\x1e\x1d\x1c", "abc"},
		{"non-breaking space", " abc ", "abc"},
		{"ogham space mark", " abc ", "abc"},
		{"ideographic space", "　abc　", "abc"},
		{"narrow no-break space", " abc ", "abc"},
		{"line and paragraph separators", "  abc  ", "abc"},
		{"all whitespace", " \t\n\x1c ", ""},
		{"empty", "", ""},
		// U+200B, the zero-width space, is NOT whitespace to Python despite
		// its name, so it is content and must survive.
		{"zero width space is content", "​abc​", "​abc​"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := strip(tc.text); got != tc.want {
				t.Errorf("strip(%q) = %q, want %q", tc.text, got, tc.want)
			}
			if isBlank(tc.text) != (tc.want == "") {
				t.Errorf("isBlank(%q) = %v, want %v", tc.text, isBlank(tc.text), tc.want == "")
			}
		})
	}
}

func TestSplitLinesIsPythonsSplitlines(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"newlines", "a\nb\n", []string{"a\n", "b\n"}},
		{"no trailing newline", "a\nb", []string{"a\n", "b"}},
		// One line ending, not two: a lone \r before an \n belongs to it.
		{"CRLF", "a\r\nb", []string{"a\r\n", "b"}},
		{"bare CR", "a\rb", []string{"a\r", "b"}},
		{"CR then CR", "a\r\rb", []string{"a\r", "\r", "b"}},
		{"vertical tab", "a\vb", []string{"a\v", "b"}},
		{"form feed", "a\fb", []string{"a\f", "b"}},
		{"file, group and record separators", "a\x1cb\x1dc\x1ed", []string{"a\x1c", "b\x1d", "c\x1e", "d"}},
		// U+001F is whitespace but not a line ending.
		{"unit separator is not a line ending", "a\x1fb", []string{"a\x1fb"}},
		{"next line", "a\u0085b", []string{"a\u0085", "b"}},
		{"line separator", "a b", []string{"a ", "b"}},
		{"paragraph separator", "a b", []string{"a ", "b"}},
		// U+00A0 is whitespace and not a line ending.
		{"non-breaking space is not a line ending", "a b", []string{"a b"}},
		{"empty", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitLinesKeepingEnds(tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("splitLinesKeepingEnds(%q) = %q, want %q", tc.text, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
			// keepends: the lines are the text, so their lengths are the
			// offsets the detector slices tables with.
			if strings.Join(got, "") != tc.text {
				t.Error("the lines do not join back into the text")
			}
		})
	}
}
