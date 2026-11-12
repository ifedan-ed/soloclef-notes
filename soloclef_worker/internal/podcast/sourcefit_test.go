package podcast

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestASourceThatFitsIsGivenWhole(t *testing.T) {
	text, condensed := fitSource("  # A\n\nShort.  ", 100)
	if condensed || text != "# A\n\nShort." {
		t.Fatalf("got %q, %v", text, condensed)
	}
	if strings.Contains(sourceBlock("short"), condensedNote) {
		t.Fatal("a source that fits was called condensed")
	}
}

// A long paper keeps every section's heading and opening, instead of
// losing everything after the first third.
func TestALongSourceKeepsEverySection(t *testing.T) {
	var paper strings.Builder
	for section := 1; section <= 30; section++ {
		paper.WriteString("## Section " + string(rune('A'+section%26)) + "\n\n")
		paper.WriteString(strings.Repeat("Opening sentence of the section. ", 30) + "\n\n")
		paper.WriteString(strings.Repeat("A later paragraph with detail. ", 60) + "\n\n")
		paper.WriteString("- first item\n- second item\n\n| a | b |\n| 1 | 2 |\n\n")
	}
	budget := 20_000
	text, condensed := fitSource(paper.String(), budget)
	if !condensed {
		t.Fatal("a source over the budget was not condensed")
	}
	if utf8.RuneCountInString(text) > budget {
		t.Fatalf("the condensed source is %d runes, over %d", utf8.RuneCountInString(text), budget)
	}
	if strings.Count(text, "## Section") != 30 {
		t.Fatalf("%d of 30 headings kept", strings.Count(text, "## Section"))
	}
	if !strings.Contains(text, "- first item") || !strings.Contains(text, "| 1 | 2 |") || !strings.Contains(text, "[…]") {
		t.Fatal("lists, tables or the shortening mark were lost")
	}
}

func TestAClippedPassageEndsAtASentenceWhenOneIsNear(t *testing.T) {
	if got := clipText("One sentence here. Another one that runs on and on.", 30); got != "One sentence here. […]" {
		t.Fatalf("got %q", got)
	}
	if got := clipText("unbroken words without any stop at all", 20); got != "unbroken words […]" {
		t.Fatalf("got %q", got)
	}
}
