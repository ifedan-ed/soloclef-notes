package localservice

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/internal/testpdf"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// TestLiveService runs against a real soloclef_parse_service. It is opt-in:
// set SOLOCLEF_PARSE_SERVICE_URL, or run
// scripts/parse_service_container_test.sh, which builds the image, starts a
// throwaway container, runs this and removes the container.
func TestLiveService(t *testing.T) {
	address := os.Getenv("SOLOCLEF_PARSE_SERVICE_URL")
	if address == "" {
		t.Skip("SOLOCLEF_PARSE_SERVICE_URL is not set")
	}
	client, err := New(Config{BaseURL: address, Timeout: 2 * time.Minute, MaxResponseBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := client.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}

	t.Run("a PDF with a text layer comes back page by page", func(t *testing.T) {
		pdf := testpdf.Document("The first page is about dosing.", "The second page is about storage.")
		started := time.Now()
		document, err := client.Parse(ctx, parse.StringSource("two pages.pdf", string(pdf)))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("parsed %d bytes into %d bytes of Markdown in %s", len(pdf), len(document.Markdown), time.Since(started).Round(time.Millisecond))
		if len(document.Pages) != 2 || document.Pages[0].Number != 1 || document.Pages[1].Number != 2 {
			t.Fatalf("pages = %+v", document.Pages)
		}
		if !strings.Contains(document.PageMarkdown(0), "The first page is about dosing.") ||
			!strings.Contains(document.PageMarkdown(1), "The second page is about storage.") {
			t.Errorf("markdown = %q", document.Markdown)
		}
		if document.Parser != parse.Local {
			t.Errorf("parser = %q", document.Parser)
		}
	})

	t.Run("a damaged file is refused as a failed parse", func(t *testing.T) {
		_, err := client.Parse(ctx, parse.StringSource("damaged.pdf", "this is not a PDF"))
		if parse.KindOf(err) != parse.ParseFailed {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("a format the service does not read is refused by the service", func(t *testing.T) {
		_, err := client.Parse(ctx, parse.StringSource("sheet.xlsx", "PK"))
		if parse.KindOf(err) != parse.UnsupportedFormat {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("a PDF with no text on any page is refused as having no text", func(t *testing.T) {
		_, err := client.Parse(ctx, parse.StringSource("blank.pdf", string(testpdf.Document("", ""))))
		if parse.KindOf(err) != parse.NoText {
			t.Fatalf("err = %v", err)
		}
	})
}
