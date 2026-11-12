package tablesafe

import (
	"strings"
	"testing"
)

// Each case here is one of the detector's rules, and each rule exists because
// the regular expression it replaced broke it. The answers are the ones the
// Python app's own tests (test_table_aware_chunking.py) asserted against the
// Python detector.

func TestWhatIsAndIsNotATable(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		tables int
		rows   []int // one row count per table found
	}{
		{
			name:   "a header, a separator and one row",
			text:   "| A | B |\n|---|---|\n| 1 | 2 |\n",
			tables: 1, rows: []int{1},
		},
		{
			name:   "a fenced code block of shell pipelines",
			text:   "Here is some shell.\n\n```\n| grep foo\n| sort\n| uniq -c\n```\n\nAfter.\n",
			tables: 0,
		},
		{
			name:   "a tilde fence",
			text:   "Shell.\n\n~~~\n| cut -f1\n|---|\n| head |\n~~~\n\nAfter.\n",
			tables: 0,
		},
		{
			name: "a table at end of file with no trailing newline keeps its last row",
			text: "Intro.\n\n| A | B |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |",
			// The regular expression needed a newline after every row and
			// dropped this one, which then became a chunk of cells with no
			// header above them.
			tables: 1, rows: []int{2},
		},
		{
			name:   "pipe lines with no separator",
			text:   "Intro.\n\n| quoted line\n| another one\n\nMore prose.\n",
			tables: 0,
		},
		{
			name:   "a header with no body rows",
			text:   "| A | B |\n|---|---|\n\nprose.\n",
			tables: 0,
		},
		{
			name:   "two adjacent tables with no blank line between them",
			text:   "| A | B |\n|---|---|\n| 1 | 2 |\n| X | Y |\n|---|---|\n| 3 | 4 |\n",
			tables: 2, rows: []int{1, 1},
		},
		{
			name:   "an indented code block",
			text:   "Intro.\n\n    | A | B |\n    |---|---|\n    | 1 | 2 |\n\nAfter.\n",
			tables: 0,
		},
		{
			name: "a ragged row ends the table",
			text: "| A | B |\n|---|---|\n| 1 | 2 |\n| 3 | 4 | 5 |\n",
			// Nothing is inferred: a row of the wrong width is not a row.
			tables: 1, rows: []int{1},
		},
		{
			name:   "aligned separator cells",
			text:   "| A | B | C |\n|:---|---:|:---:|\n| 1 | 2 | 3 |\n",
			tables: 1, rows: []int{1},
		},
		{
			name:   "a separator cell of a single colon is not a separator",
			text:   "| A | B |\n| : | : |\n| 1 | 2 |\n",
			tables: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := findMarkdownTables(tc.text)
			if len(found) != tc.tables {
				t.Fatalf("found %d tables, want %d", len(found), tc.tables)
			}
			for i, table := range found {
				if got := len(table.rows); got != tc.rows[i] {
					t.Errorf("table %d has %d body rows, want %d", i, got, tc.rows[i])
				}
				// The spans have to be usable as slice bounds, and the rows
				// have to be contiguous: a gap between two rows is a row that
				// no chunk would carry.
				if table.start < 0 || table.end > len(tc.text) || table.headerEnd > table.end {
					t.Fatalf("table %d spans %d..%d (header to %d) in a %d-byte text",
						i, table.start, table.end, table.headerEnd, len(tc.text))
				}
				at := table.headerEnd
				for r, row := range table.rows {
					if row.start != at {
						t.Errorf("table %d row %d starts at %d, not where row %d ended (%d)",
							i, r, row.start, r-1, at)
					}
					at = row.end
				}
				if at != table.end {
					t.Errorf("table %d's rows end at %d, the table at %d", i, at, table.end)
				}
			}
		})
	}
}

func TestOnlyALabelledParagraphTravelsWithTheTable(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		before string
		after  string
	}{
		{
			name:   "a numbered caption and a footnote",
			text:   "Table 1. Doses.\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n* Adjust in renal failure.\n",
			before: "Table 1. Doses.",
			after:  "* Adjust in renal failure.",
		},
		{
			name: "ordinary prose either side",
			text: "Ordinary lead-in.\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\nOrdinary follow-up.\n",
			// Neither is labelled, so neither is duplicated onto the rows:
			// both stay independently retrievable prose.
			before: "", after: "",
		},
		{
			name:   "a units line",
			text:   "Units: mg/dL\n\n| A | B |\n|---|---|\n| 1 | 2 |\n",
			before: "Units: mg/dL",
		},
		{
			name: "a paragraph about tablets is not a caption",
			text: "Tablets are dispensed weekly.\n\n| A | B |\n|---|---|\n| 1 | 2 |\n",
			// `table\b` in Python is a Unicode word boundary and Go's \b is
			// ASCII-only, which is why the pattern here does not use \b at
			// all. "Tablets" must not match either way.
			before: "",
		},
		{
			name:  "a notes paragraph below",
			text:  "| A | B |\n|---|---|\n| 1 | 2 |\n\nNotes: nothing here.\n",
			after: "Notes: nothing here.",
		},
		{
			name: "bold text below is not a footnote marker",
			text: "| A | B |\n|---|---|\n| 1 | 2 |\n\n**Important** but not a note.\n",
			// A single * opens a footnote; ** opens bold. Python says so with
			// a negative lookahead, which RE2 has not got, so isNotesParagraph
			// says it by hand -- this is the case that tells them apart.
			after: "",
		},
		{
			name:  "a bold Notes heading below is a note",
			text:  "| A | B |\n|---|---|\n| 1 | 2 |\n\n**Notes** on the above.\n",
			after: "**Notes** on the above.",
		},
		{
			name: "a Table paragraph below introduces the next table",
			text: "| A | B |\n|---|---|\n| 1 | 2 |\n\nTable 2. The next one.\n",
			// Swept up as this table's notes it would be duplicated onto
			// every one of this table's chunks and lost from the next one's.
			after: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := findMarkdownTables(tc.text)
			if len(found) == 0 {
				t.Fatal("no table found")
			}
			table := found[0]
			if table.contextBefore != tc.before {
				t.Errorf("contextBefore = %q, want %q", table.contextBefore, tc.before)
			}
			if table.contextAfter != tc.after {
				t.Errorf("contextAfter = %q, want %q", table.contextAfter, tc.after)
			}
		})
	}
}

// The answers are the ones the Python test parametrises over.
func TestSplitCellsTreatsAnEscapedPipeAsText(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"| a | b |", []string{"a", "b"}},
		{"a | b", []string{"a", "b"}},
		{`| a\|b | c |`, []string{`a\|b`, "c"}},
		{"no pipes here", nil},
		{"|  |  |", []string{"", ""}},
		// Two backslashes are an escaped backslash, so the pipe after them is
		// a cell boundary again.
		{`| a\\|b |`, []string{`a\\`, "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			got := splitCells(tc.line)
			if len(got) != len(tc.want) {
				t.Fatalf("splitCells(%q) = %q, want %q", tc.line, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("cell %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// A document whose lines are separated by something other than a newline
// still has its tables found in the right place, because the detector counts
// lines the way str.splitlines() does.
func TestAFormFeedEndsALineForTheDetector(t *testing.T) {
	const table = "| A | B |\n|---|---|\n| 1 | 2 |\n"
	text := "Before the form feed.\x0c" + table
	found := findMarkdownTables(text)
	if len(found) != 1 {
		t.Fatalf("found %d tables, want 1 -- the form feed did not end a line", len(found))
	}
	if got := text[found[0].start:found[0].end]; got != table {
		t.Errorf("the table span is %q, want %q", got, table)
	}
	// A plain "\n" split would put the form feed inside the header line and
	// find nothing, so this is worth saying twice.
	if strings.Count(text, "\n") != 3 {
		t.Fatal("the fixture no longer holds a form feed as its only odd line ending")
	}
}
