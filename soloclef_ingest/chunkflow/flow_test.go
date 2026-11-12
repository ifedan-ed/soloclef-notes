package chunkflow

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_chunker/recursive"
	"git.danvics.com/danvics/soloclef/soloclef_chunker/tablesafe"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/delimited"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/localservice"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// chunker counts in characters, chonkie's own character tokenizer, so the
// test needs no vocabulary file. Production passes the embedding profile's
// tokenizer in the same field.
func chunker(size int) tablesafe.Chunker {
	return tablesafe.Chunker{Size: size, Sizer: recursive.Characters{}}
}

func TestACSVBecomesTableChunksThatEachCarryTheHeader(t *testing.T) {
	var csv strings.Builder
	csv.WriteString("Drug,Dose,Route\n")
	for row := range 40 {
		fmt.Fprintf(&csv, "Drug %02d,%d mg,oral\n", row, 100+row)
	}
	parser, err := delimited.New(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	document, err := parser.Parse(context.Background(), parse.StringSource("formulary.csv", csv.String()))
	if err != nil {
		t.Fatal(err)
	}

	chunks, err := chunker(300).Chunks(document.Markdown)
	if err != nil {
		t.Fatal(err)
	}

	if len(chunks) < 2 {
		t.Fatalf("a 40-row table at 300 characters gave %d chunk(s)", len(chunks))
	}
	rows := 0
	for index, chunk := range chunks {
		// The point of emitting a real Markdown table: the chunker sees it
		// as one and repeats the header on every piece, so a row embedded
		// alone still says which column is the dose.
		if !strings.HasPrefix(chunk, "| Drug | Dose | Route |\n| --- | --- | --- |\n") {
			t.Errorf("chunk %d does not start with the header:\n%s", index, chunk)
		}
		rows += strings.Count(chunk, "| Drug ") - 1 // less the header line
	}
	if rows != 40 {
		t.Errorf("the chunks hold %d data rows, want all 40", rows)
	}
}

func TestAParsedPDFIsChunkedAndEachChunkHasAPage(t *testing.T) {
	// The parser service's reply for a two-page PDF: prose on page 1, a
	// table on page 2.
	prose := strings.Repeat("Give the dose once daily with food. ", 12)
	reply := fmt.Sprintf(`{"parser":"pymupdf4llm","parser_version":"1.28.2","pages":[`+
		`{"number":1,"markdown":"# Dosing guide\n\n%s\n\n"},`+
		`{"number":2,"markdown":"|Drug|Dose|\n|---|---|\n|Amoxicillin|500 mg|\n|Ceftriaxone|1 g|\n\n"}]}`, prose)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(reply))
	}))
	defer service.Close()
	client, err := localservice.New(localservice.Config{BaseURL: service.URL, Timeout: 5 * time.Second, MaxResponseBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	document, err := client.Parse(context.Background(), parse.StringSource("guide.pdf", "%PDF"))
	if err != nil {
		t.Fatal(err)
	}

	chunks, err := chunker(200).Chunks(document.Markdown)
	if err != nil {
		t.Fatal(err)
	}

	if len(chunks) < 3 {
		t.Fatalf("got %d chunk(s):\n%q", len(chunks), chunks)
	}
	// A chunk's page is a lookup of where it sits in the Markdown against
	// the page spans the parser recorded. (The chunker reporting each
	// chunk's span itself is the planned addition; a prose chunk is a
	// verbatim slice, so the test can find it.)
	pageOf := func(chunk string) int {
		offset := strings.Index(document.Markdown, strings.TrimSpace(chunk))
		if offset < 0 {
			t.Fatalf("chunk is not a slice of the document: %q", chunk)
		}
		for _, page := range document.Pages {
			if offset >= page.Start && offset < page.End {
				return page.Number
			}
		}
		t.Fatalf("offset %d is on no page", offset)
		return 0
	}
	if page := pageOf(chunks[0]); page != 1 {
		t.Errorf("the first chunk is on page %d", page)
	}
	table := chunks[len(chunks)-1]
	if !strings.Contains(table, "|Amoxicillin|500 mg|") || !strings.Contains(table, "|Ceftriaxone|1 g|") {
		t.Errorf("the table was split from its rows:\n%s", table)
	}
	if page := pageOf(table); page != 2 {
		t.Errorf("the table chunk is on page %d", page)
	}
}
