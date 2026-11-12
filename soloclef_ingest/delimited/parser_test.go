package delimited

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

func table(t *testing.T, name, content string) string {
	t.Helper()
	parser, err := New(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	document, err := parser.Parse(context.Background(), parse.StringSource(name, content))
	if err != nil {
		t.Fatal(err)
	}
	if document.Pages != nil || document.Parser != parse.Builtin {
		t.Fatalf("document = %+v", document)
	}
	return document.Markdown
}

func TestCSVBecomesATableWithItsFirstRowAsHeader(t *testing.T) {
	got := table(t, "doses.csv", "Drug,Dose,Route\nAmoxicillin,500 mg,oral\nCeftriaxone,1 g,IV\n")
	want := "" +
		"| Drug | Dose | Route |\n" +
		"| --- | --- | --- |\n" +
		"| Amoxicillin | 500 mg | oral |\n" +
		"| Ceftriaxone | 1 g | IV |\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTSVSplitsOnTabsAndTreatsQuotesAsData(t *testing.T) {
	got := table(t, "sizes.tsv", "Item\tSize\nPipe\t2\" bore\n")
	want := "" +
		"| Item | Size |\n" +
		"| --- | --- |\n" +
		"| Pipe | 2\" bore |\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCellsThatWouldBreakTheTableAreEscaped(t *testing.T) {
	got := table(t, "notes.csv", "Name,Note\nA|B,\"line one\nline two\r\nline three\"\n")
	want := "" +
		"| Name | Note |\n" +
		"| --- | --- |\n" +
		"| A\\|B | line one<br>line two<br>line three |\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestARowWiderThanTheHeaderWidensTheTableInsteadOfLosingCells(t *testing.T) {
	got := table(t, "ragged.csv", "a,b\n1\n1,2,3\n")
	want := "" +
		"| a | b |  |\n" +
		"| --- | --- | --- |\n" +
		"| 1 |  |  |\n" +
		"| 1 | 2 | 3 |\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestQuotedCommasAndTheByteOrderMarkAreHandled(t *testing.T) {
	got := table(t, "people.csv", "\xEF\xBB\xBFname,city\n\"Okafor, Ada\", Lagos \n")
	want := "" +
		"| name | city |\n" +
		"| --- | --- |\n" +
		"| Okafor, Ada | Lagos |\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestABrokenQuoteInCSVIsRefusedWithItsLine(t *testing.T) {
	parser, _ := New(1 << 20)
	_, err := parser.Parse(context.Background(), parse.StringSource("bad.csv", "a,b\n1,\"unclosed\n2,3\n"))
	if parse.KindOf(err) != parse.ParseFailed || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestAnEmptyFileIsRefusedAsHavingNoText(t *testing.T) {
	parser, _ := New(1 << 20)
	_, err := parser.Parse(context.Background(), parse.StringSource("empty.csv", "\n\n"))
	if parse.KindOf(err) != parse.NoText {
		t.Fatalf("err = %v", err)
	}
}

func TestOtherExtensionsAreRefused(t *testing.T) {
	parser, _ := New(1 << 20)
	if parser.Reads(".txt") || !parser.Reads(".csv") || !parser.Reads(".tsv") {
		t.Fatal("Reads is wrong")
	}
	_, err := parser.Parse(context.Background(), parse.StringSource("a.txt", "a,b\n"))
	if parse.KindOf(err) != parse.UnsupportedFormat {
		t.Fatalf("err = %v", err)
	}
}

// spreadsheet is a CSV of rows lines by 8 columns, shaped like an export.
func spreadsheet(rows int) string {
	var content strings.Builder
	content.WriteString("id,name,department,email,city,amount,status,note\n")
	for row := range rows {
		content.WriteString(strconv.Itoa(row))
		content.WriteString(",Ada Okafor,Paediatrics,ada@notes.example,Lagos,1234.50,active,\"seen, reviewed\"\n")
	}
	return content.String()
}

func BenchmarkParseCSV(b *testing.B) {
	parser, _ := New(256 << 20)
	content := spreadsheet(100_000)
	source := parse.StringSource("export.csv", content)
	b.SetBytes(int64(len(content)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parser.Parse(context.Background(), source); err != nil {
			b.Fatal(err)
		}
	}
}
