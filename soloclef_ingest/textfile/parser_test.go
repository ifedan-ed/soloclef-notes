package textfile

import (
	"context"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

func TestMarkdownIsReturnedAsWritten(t *testing.T) {
	parser, err := New(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	note := "# Ward round\n\n| Drug | Dose |\n| --- | --- |\n| Amoxicillin | 500 mg |\n"

	document, err := parser.Parse(context.Background(), parse.StringSource("round.md", note))
	if err != nil {
		t.Fatal(err)
	}
	if document.Markdown != note || document.Pages != nil || document.Parser != parse.Builtin {
		t.Fatalf("document = %+v", document)
	}
}

func TestAnEmptyFileIsRefusedAsHavingNoText(t *testing.T) {
	parser, _ := New(1 << 20)
	_, err := parser.Parse(context.Background(), parse.StringSource("empty.txt", "  \n"))
	if parse.KindOf(err) != parse.NoText {
		t.Fatalf("err = %v", err)
	}
}

func TestItReadsTextExtensionsOnly(t *testing.T) {
	parser, _ := New(1)
	for extension, want := range map[string]bool{".md": true, ".txt": true, ".py": true, ".csv": false, ".pdf": false} {
		if parser.Reads(extension) != want {
			t.Errorf("Reads(%q) = %v", extension, !want)
		}
	}
}

func TestTheSizeLimitIsRequired(t *testing.T) {
	if _, err := New(0); err == nil {
		t.Fatal("a zero limit was accepted")
	}
}

func BenchmarkParseMarkdown(b *testing.B) {
	parser, _ := New(64 << 20)
	note := strings.Repeat("## Heading\n\nA paragraph of ordinary prose, long enough to look like a note.\n\n", 16*1024)
	source := parse.StringSource("note.md", note)
	b.SetBytes(int64(len(note)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parser.Parse(context.Background(), source); err != nil {
			b.Fatal(err)
		}
	}
}
