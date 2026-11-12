package tablesafe

import (
	"fmt"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_chunker/recursive"
)

// Every want below is what chunk_text_hybrid returned for the same input in
// the backend container, with EMBEDDING_TOKENIZER unset -- so chonkie's
// character tokenizer, which recursive.Characters reproduces and which needs
// no vocabulary file. Nothing here is written by hand.
//
// Two things in this list changed on 2026-11-10 and are worth looking at
// twice, because the Go side used to do something else:
//
//   - A chunk is no longer stripped. Python tests whether a piece is blank
//     and drops it if it is; a piece that survives keeps the whitespace it
//     was cut with. So "Intro line.\n" is the chunk, not "Intro line.".
//   - A table is no longer emitted whole. Its rows are packed with the
//     header repeated, and its labelled caption and notes are carried onto
//     the chunk -- which is why the caption case below has the caption text
//     twice: once as the prose above the table, once on the table chunk.
func TestTheChunksArePythonsChunks(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "no table",
			text: "Just prose.\n",
			want: []string{
				"Just prose.\n",
			},
		},
		{
			name: "a table on its own",
			text: "| a | b |\n|---|---|\n| 1 | 2 |\n",
			want: []string{
				"| a | b |\n|---|---|\n| 1 | 2 |\n",
			},
		},
		{
			name: "prose then a table",
			text: "Intro line.\n| a | b |\n|---|---|\n| 1 | 2 |\n",
			want: []string{
				"Intro line.\n",
				"| a | b |\n|---|---|\n| 1 | 2 |\n",
			},
		},
		{
			name: "a blank line before the table",
			text: "Intro line.\n\n| a | b |\n|---|---|\n| 1 | 2 |\n",
			want: []string{
				"Intro line.\n\n",
				"| a | b |\n|---|---|\n| 1 | 2 |\n",
			},
		},
		{
			name: "a table then prose",
			text: "| a | b |\n|---|---|\n| 1 | 2 |\nAfter the table.\n",
			want: []string{
				"| a | b |\n|---|---|\n| 1 | 2 |\n",
				"After the table.\n",
			},
		},
		{
			name: "two tables",
			text: "| a |\n|---|\n| 1 |\n\nmiddle prose\n\n| b |\n|---|\n| 2 |\n",
			want: []string{
				"| a |\n|---|\n| 1 |\n",
				"\nmiddle prose\n\n",
				"| b |\n|---|\n| 2 |\n",
			},
		},
		{
			name: "two adjacent tables",
			text: "| a |\n|---|\n| 1 |\n| b |\n|---|\n| 2 |\n",
			want: []string{
				"| a |\n|---|\n| 1 |\n",
				"| b |\n|---|\n| 2 |\n",
			},
		},
		{
			name: "a header and separator with no body",
			text: "| a | b |\n|---|---|\n\nprose.\n",
			want: []string{
				"| a | b |\n|---|---|\n\nprose.\n",
			},
		},
		{
			name: "a table that ends the document without a newline",
			text: "Intro.\n| a | b |\n|---|---|\n| 1 | 2 |",
			want: []string{
				"Intro.\n",
				"| a | b |\n|---|---|\n| 1 | 2 |",
			},
		},
		{
			name: "a pipe in the middle of a line is not a table",
			text: "a | b\nc | d\n",
			want: []string{
				"a | b\nc | d\n",
			},
		},
		{
			name: "pipe lines with no separator are prose",
			text: "| a |\n| b |\n| c |\n",
			want: []string{
				"| a |\n| b |\n| c |\n",
			},
		},
		{
			name: "an indented table is an indented code block",
			text: "Intro.\n\n    | A | B |\n    |---|---|\n    | 1 | 2 |\n\nAfter.\n",
			want: []string{
				"Intro.\n\n    | A | B |\n    |---|---|\n    | 1 | 2 |\n\nAfter.\n",
			},
		},
		{
			name: "a fenced block of pipes is not a table",
			text: "Here is some shell.\n\n```\n| grep foo\n| sort\n```\n\nAfter.\n",
			want: []string{
				"Here is some shell.\n\n```\n| grep foo\n| sort\n```\n\nAfter.\n",
			},
		},
		{
			name: "a ragged row ends the table",
			text: "| A | B |\n|---|---|\n| 1 | 2 |\n| 3 | 4 | 5 |\n",
			want: []string{
				"| A | B |\n|---|---|\n| 1 | 2 |\n",
				"| 3 | 4 | 5 |\n",
			},
		},
		{
			name: "a caption and notes travel with the rows",
			text: "Table 1. Doses.\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n* Adjust in renal failure.\n",
			want: []string{
				"Table 1. Doses.\n\n",
				"Table 1. Doses.\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n\n* Adjust in renal failure.",
				"\n* Adjust in renal failure.\n",
			},
		},
		{
			name: "an unlabelled paragraph above a table is not a caption",
			text: "Ordinary lead-in.\n\n| A | B |\n|---|---|\n| 1 | 2 |\n",
			want: []string{
				"Ordinary lead-in.\n\n",
				"| A | B |\n|---|---|\n| 1 | 2 |\n",
			},
		},
		{
			name: "an information separator around the prose",
			text: "\x1cprose body here\x1c",
			want: []string{
				"\x1cprose body here\x1c",
			},
		},
		{
			name: "a non-breaking space around the prose",
			text: "\u00a0prose\u00a0",
			want: []string{
				"\u00a0prose\u00a0",
			},
		},
		{
			name: "a form feed ends a line",
			text: "Before.\x0c| A | B |\n|---|---|\n| 1 | 2 |\n",
			want: []string{
				"Before.\x0c",
				"| A | B |\n|---|---|\n| 1 | 2 |\n",
			},
		},
		{
			name: "only whitespace",
			text: "   \n\n  ",
			want: nil,
		},
		{
			name: "empty",
			text: "",
			want: nil,
		},
	}

	chunker := Chunker{Size: recursive.DefaultChunkSize, Sizer: recursive.Characters{}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := chunker.Chunks(tc.text)
			if err != nil {
				t.Fatalf("Chunks: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d chunks %q, Python's %d %q",
					len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("chunk %d = %q, Python's = %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// A table longer than the ceiling is packed row by row, and every chunk after
// the first repeats the header. This is the defect the rewrite was for: the
// table used to become one chunk however large it was, which at 400 rows was
// about 5,200 BGE-M3 tokens against a 512-token model window -- one vector
// for 400 rows, which retrieves none of them.
func TestALongTableIsPackedRowByRowAndEveryChunkIsHeaded(t *testing.T) {
	const header = "| Specimen | Level |\n|---|---|\n"
	var table strings.Builder
	table.WriteString(header)
	for range 200 {
		table.WriteString("| a specimen value | another column value |\n")
	}

	chunker := Chunker{Size: recursive.DefaultChunkSize, Sizer: recursive.Characters{}}
	chunks, err := chunker.Chunks(table.String())
	if err != nil {
		t.Fatalf("Chunks: %v", err)
	}
	if len(chunks) < 10 {
		t.Fatalf("got %d chunks, expected the rows to be packed into many", len(chunks))
	}
	for i, chunk := range chunks {
		if !strings.HasPrefix(chunk, header) {
			t.Fatalf("chunk %d does not begin with the table header, so its "+
				"cells have no headings above them", i)
		}
		// No chunk is cut inside a row.
		for _, line := range strings.Split(strings.TrimSuffix(chunk, "\n"), "\n") {
			if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
				t.Fatalf("chunk %d holds a partial row: %q", i, line)
			}
		}
		if n, err := chunker.Sizer.Count(chunk); err != nil || n > OversizeFactor*chunker.Size {
			t.Fatalf("chunk %d is %d units, over the %d ceiling (err %v)",
				i, n, OversizeFactor*chunker.Size, err)
		}
	}
	// Every row is indexed once and only once.
	joined := strings.Join(chunks, "")
	if got := strings.Count(joined, "| a specimen value |"); got != 200 {
		t.Errorf("the 200 rows appear %d times across the chunks", got)
	}
}

// A zero Chunker refuses rather than picking a size or a vocabulary: the
// vocabulary decides every boundary, so a guess is a corpus that silently
// does not match its index.
func TestAChunkerWithNoSizingRefuses(t *testing.T) {
	if _, err := (Chunker{Size: 512}).Chunks("text"); err == nil {
		t.Error("expected a Chunker with no Sizer to refuse")
	}
	if _, err := (Chunker{Sizer: recursive.Characters{}}).Chunks("text"); err == nil {
		t.Error("expected a Chunker with no Size to refuse")
	}
}

// The spans Pieces reports are what a citation's line and page range are
// computed from, so they are held to three things: the texts are Chunks'
// texts, a prose piece is exactly its span, and a table piece's span is the
// rows it holds and not the header it repeats.
func TestPiecesReportTheSpanEachChunkWasCutFrom(t *testing.T) {
	var document strings.Builder
	document.WriteString("# Formulary\n\n")
	document.WriteString(strings.Repeat("Check the dose against the weight before giving it. ", 12))
	document.WriteString("\n\n| Drug | Dose |\n| --- | --- |\n")
	for row := range 30 {
		fmt.Fprintf(&document, "| Drug %02d | %d mg |\n", row, 100+row)
	}
	document.WriteString("\nGive every dose with food unless the label says otherwise.\n")
	text := document.String()
	chunker := Chunker{Size: 200, Sizer: recursive.Characters{}}

	pieces, err := chunker.Pieces(text)
	if err != nil {
		t.Fatal(err)
	}
	texts, err := chunker.Chunks(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(pieces) != len(texts) {
		t.Fatalf("%d pieces and %d chunks", len(pieces), len(texts))
	}

	const header = "| Drug | Dose |\n| --- | --- |\n"
	tables, previousEnd := 0, 0
	for index, piece := range pieces {
		if piece.Text != texts[index] {
			t.Fatalf("piece %d's text is not chunk %d's", index, index)
		}
		if piece.Start < previousEnd || piece.End <= piece.Start || piece.End > len(text) {
			t.Fatalf("piece %d spans %d..%d after %d in a document of %d", index, piece.Start, piece.End, previousEnd, len(text))
		}
		previousEnd = piece.End
		span := text[piece.Start:piece.End]
		if !strings.Contains(piece.Text, "| Drug ") {
			if span != piece.Text {
				t.Errorf("prose piece %d is not its span:\n%q\n%q", index, piece.Text, span)
			}
			continue
		}
		tables++
		if !strings.HasPrefix(piece.Text, header) || !strings.HasSuffix(piece.Text, span) {
			t.Errorf("table piece %d does not end in its span under the header:\n%q\n%q", index, piece.Text, span)
		}
		if tables > 1 && strings.Contains(span, "| --- |") {
			t.Errorf("table piece %d's span takes in a header that is not at that place: %q", index, span)
		}
	}
	if tables < 2 {
		t.Fatalf("the table made %d chunk(s); the test needs one that repeats the header", tables)
	}
}

func TestPiecesOfABlankDocumentAreNone(t *testing.T) {
	pieces, err := Chunker{Size: 100, Sizer: recursive.Characters{}}.Pieces(" \n\n\t")
	if err != nil || pieces != nil {
		t.Fatalf("pieces = %v, err = %v", pieces, err)
	}
}
