package notes

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestATitleAskedForIsKeptTrimmed(t *testing.T) {
	if got := NoteTitle("  Ward round   list ", "# Something else"); got != "Ward round list" {
		t.Errorf("got %q", got)
	}
}

func TestANoteWithNoTitleIsNamedFromItsFirstLineOfWords(t *testing.T) {
	cases := []struct{ text, want string }{
		{"# Heading one\n\nBody", "Heading one"},
		{"\n\n---\n\n> **Bold** quote [citation:3]", "Bold quote"},
		{"```\ncode\n```", "code"},
		{"- [x] Done task", "Done task"},
		{"1. First *step* with a [link](https://example.test)", "First step with a link"},
		{"![chart](a.png)\nThe dose is $5\\,mg/kg$", "The dose is 5\\,mg/kg"},
		{"| A | B |\n|---|---|\n| 1 | 2 |", "A | B"},
	}
	for _, c := range cases {
		if got := NoteTitle("", c.text); got != c.want {
			t.Errorf("NoteTitle(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestANoteWithNothingToNameItIsUntitled(t *testing.T) {
	for _, text := range []string{"", "   \n\n", "---", "[citation:1]"} {
		if got := NoteTitle(" ", text); got != untitled {
			t.Errorf("NoteTitle(%q) = %q, want %q", text, got, untitled)
		}
	}
}

func TestALongFirstLineIsCutAtAWord(t *testing.T) {
	line := strings.Repeat("antibiotic ", 20)
	got := TitleFromText(line)
	if utf8.RuneCountInString(got) > titleFromTextLength {
		t.Errorf("%d characters: %q", utf8.RuneCountInString(got), got)
	}
	if !strings.HasSuffix(got, "antibiotic…") {
		t.Errorf("cut mid-word: %q", got)
	}
}
