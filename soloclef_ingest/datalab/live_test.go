package datalab

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/internal/testpdf"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// TestLiveAPI converts a two-page PDF built in the test with the real
// Datalab API. It runs only when DATALAB_API_KEY is set, and it is billed
// (two pages in the cheapest full mode).
func TestLiveAPI(t *testing.T) {
	key := os.Getenv("DATALAB_API_KEY")
	if key == "" {
		t.Skip("DATALAB_API_KEY is not set")
	}
	client, err := New(Config{
		APIKey: key, Endpoint: Endpoint, Mode: "fast",
		PollInterval: 2 * time.Second, RateLimitWait: 60 * time.Second,
		Timeout: 5 * time.Minute, MaxResponseBytes: 8 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	pdf := testpdf.Document("The first page is about dosing.", "The second page is about storage.")

	started := time.Now()
	document, err := client.Parse(context.Background(), parse.StringSource("two-pages.pdf", string(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d pages, %d bytes of Markdown, %s", len(document.Pages), len(document.Markdown), time.Since(started).Round(time.Millisecond))

	if len(document.Pages) != 2 || document.Pages[0].Number != 1 || document.Pages[1].Number != 2 {
		t.Fatalf("pages = %+v\n%s", document.Pages, document.Markdown)
	}
	if !strings.Contains(document.PageMarkdown(0), "first page") || !strings.Contains(document.PageMarkdown(1), "second page") {
		t.Errorf("markdown = %q", document.Markdown)
	}
}
