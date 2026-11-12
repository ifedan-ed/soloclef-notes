package deliverables

import (
	"strings"
	"testing"
)

// The grammar is the web viewer's (study-text/parse-text.ts): each case
// here is one that parser answers null for, or accepts.
func TestLaTeXDelimiters(t *testing.T) {
	for text, says := range map[string]string{
		`plain text, (parentheses) and [brackets]`:     "",
		`inline \(x^2\) and displayed \[\frac{a}{b}\]`: "",
		`two in a row \(a\)\(b\)`:                      "",
		// An escaped backslash before a parenthesis is text, not a delimiter.
		`a literal \\( stays text`:           "",
		`braces escaped inside \(\{a\}\)`:    "",
		`a path C:\dir and a newline \n`:     "",
		`closes \) first`:                    `a closing LaTeX delimiter \) with nothing open`,
		`\] alone`:                           `a closing LaTeX delimiter \] with nothing open`,
		`never closed \(x`:                   `a LaTeX delimiter \( that is never closed`,
		`\[ x \)`:                            `LaTeX opened with \[ and closed with \)`,
		`\( a \( b \) \)`:                    "a LaTeX delimiter opened inside another",
		`empty \( \)`:                        "an empty LaTeX expression",
		`\(\frac{a}{b\)`:                     "unbalanced braces in a LaTeX expression",
		`\(a}{\)`:                            "unbalanced braces in a LaTeX expression",
		`ends on a backslash \`:              "",
		`three backslashes \\\( x \) is one`: "",
	} {
		if got := latexProblem(text); got != says {
			t.Errorf("%q: %q, want %q", text, got, says)
		}
	}
}

func TestTextsAReaderWouldCallTheSame(t *testing.T) {
	same := newSameness()
	for _, pair := range [][2]string{
		{"Résumé", "RE\u0301SUME\u0301"}, // composed and decomposed, two cases
		{"a  b\tc", " a b c "},
		{"ｆｕｌｌ width", "full width"},
		{"Straße", "STRASSE"},
	} {
		if same.key(pair[0]) != same.key(pair[1]) {
			t.Errorf("%q and %q are told apart", pair[0], pair[1])
		}
	}
	if same.key("mitosis") == same.key("meiosis") {
		t.Error("two different words are called the same")
	}
}

func TestCharactersNoArtifactMayHold(t *testing.T) {
	for text, says := range map[string]string{
		"two\nlines\tand a tab": "",
		"a\x00b":                "a control character (U+0000)",
		"a\rb":                  "a control character (U+000D)",
		"a\x7fb":                "a control character (U+007F)",
		"\uFEFFtext":            "a byte-order mark",
		"bad \xff bytes":        "text that is not valid UTF-8",
	} {
		if got := badCharacter(text); got != says {
			t.Errorf("%q: %q, want %q", text, got, says)
		}
	}
	var problems []string
	if got := checkTitle(&problems, "  A title  "); got != "A title" || len(problems) != 0 {
		t.Errorf("title = %q, %v", got, problems)
	}
	checkTitle(&problems, strings.Repeat("é", 201))
	if len(problems) != 1 || !strings.Contains(problems[0], "longer than 200") {
		t.Errorf("a title of 201 characters: %v", problems)
	}
}
