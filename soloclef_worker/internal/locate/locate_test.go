package locate

import (
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

func TestRangeIsTheLinesTheContentSitsOn(t *testing.T) {
	//          line 1      2  3        4       5
	text := "# Title\n\nFirst.\nSecond.\n\nLast"
	lines := NewLines(text)
	for _, test := range []struct {
		name        string
		start, end  int
		first, last int
	}{
		{"the heading and its trailing blank line", 0, 9, 1, 1},
		{"two lines ending on a newline do not claim a third", 9, 24, 3, 4},
		{"a span that begins on a blank line belongs to the line below", 8, 15, 3, 3},
		{"the last line has no newline after it", 25, len(text), 6, 6},
		{"the whole document", 0, len(text), 1, 6},
		{"a span of only white space stays where it is", 7, 9, 1, 2},
		{"an empty span", 9, 9, 3, 3},
	} {
		first, last := lines.Range(test.start, test.end)
		if first != test.first || last != test.last {
			t.Errorf("%s: %d..%d gave lines %d-%d, want %d-%d", test.name, test.start, test.end, first, last, test.first, test.last)
		}
	}
}

func TestPagesAreLookedUpInTheParsersTable(t *testing.T) {
	// Page 1 is bytes 0..10, a two-byte separator, page 2 is empty, page 3 is
	// 12..30, and page 7 (a parser may skip numbers) is 32..40.
	pages := []parse.Page{
		{Number: 1, Start: 0, End: 10},
		{Number: 2, Start: 12, End: 12},
		{Number: 3, Start: 12, End: 30},
		{Number: 7, Start: 32, End: 40},
	}
	for _, test := range []struct {
		name        string
		start, end  int
		first, last int
	}{
		{"inside one page", 2, 8, 1, 1},
		{"across a separator", 5, 20, 1, 3},
		{"starting in a separator starts on the next page with text", 10, 20, 3, 3},
		{"ending in a separator ends on the page before", 20, 31, 3, 3},
		{"to the end", 25, 40, 3, 7},
		{"past the last page's text", 40, 44, 7, 7},
	} {
		first, last, found := Pages(pages, test.start, test.end)
		if !found || first != test.first || last != test.last {
			t.Errorf("%s: %d..%d gave pages %d-%d (found %v), want %d-%d", test.name, test.start, test.end, first, last, found, test.first, test.last)
		}
	}
}

func TestADocumentWithNoPagesHasNoPageRange(t *testing.T) {
	if _, _, found := Pages(nil, 0, 10); found {
		t.Fatal("a page was found in a document with no pages")
	}
}
