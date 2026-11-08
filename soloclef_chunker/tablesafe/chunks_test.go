package tablesafe

import (
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_chunker/recursive"
)

// Every want below is what chunk_text_hybrid returned for the same input in
// the backend container, with the chunker the app builds there.
func TestTheTableBlocksAreTheOnesPythonKeepsWhole(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"no table", "Just prose.\n", []string{"Just prose."}},
		{
			"a table on its own",
			"| a | b |\n|---|---|\n| 1 | 2 |\n",
			[]string{"| a | b |\n|---|---|\n| 1 | 2 |"},
		},
		{
			"prose then a table",
			"Intro line.\n| a | b |\n|---|---|\n",
			[]string{"Intro line.", "| a | b |\n|---|---|"},
		},
		{
			"a blank line before the table",
			"Intro line.\n\n| a | b |\n|---|---|\n",
			[]string{"Intro line.", "| a | b |\n|---|---|"},
		},
		{
			"a table then prose",
			"| a | b |\n|---|---|\nAfter the table.\n",
			[]string{"| a | b |\n|---|---|", "After the table."},
		},
		{
			"two tables",
			"| a |\n|---|\n\nmiddle prose\n\n| b |\n|---|\n",
			[]string{"| a |\n|---|", "middle prose", "| b |\n|---|"},
		},
		{
			"an indented table",
			"Intro.\n   | a | b |\n\t|---|---|\n",
			[]string{"Intro.", "| a | b |\n\t|---|---|"},
		},
		// A row has to end in a newline, so the unterminated last row is
		// prose and lands in a chunk of its own.
		{
			"a table that ends the document without a newline",
			"Intro.\n| a | b |\n|---|---|",
			[]string{"Intro.", "| a | b |", "|---|---|"},
		},
		{
			"a pipe in the middle of a line is not a table",
			"a | b\nc | d\n",
			[]string{"a | b\nc | d"},
		},
		{
			"prose between two table rows",
			"| a |\n|---|\nprose\n| b |\n|---|\n",
			[]string{"| a |\n|---|", "prose", "| b |\n|---|"},
		},
		// U+001C is whitespace to Python and not to Go: the chunk is
		// "prose body here", not "\x1cprose body here\x1c".
		{"an information separator around the prose", "\x1cprose body here\x1c", []string{"prose body here"}},
		{"a non-breaking space around the prose", " prose ", []string{"prose"}},
		{"only whitespace", "   \n\n  ", nil},
		{"empty", "", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Chunks(tc.text, recursive.DefaultChunkSize)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d chunks %q, Python's %d %q", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("chunk %d = %q, Python's = %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// A table longer than the ceiling still stays whole: the table branch never
// reaches the recursive chunker.
func TestALongTableIsOneChunk(t *testing.T) {
	var table strings.Builder
	for range 200 {
		table.WriteString("| a column value | another column value |\n")
	}
	chunks := Chunks(table.String(), recursive.DefaultChunkSize)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want the table in one", len(chunks))
	}
}
