package llamaparse

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/internal/testpdf"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// TestLiveAPI parses a two-page PDF built in the test with the real
// LlamaParse API. It runs only when LLAMA_CLOUD_API_KEY is set, needs
// LLAMA_CLOUD_BASE_URL beside it (the key's region), and is billed (two
// pages on the cheapest tier).
func TestLiveAPI(t *testing.T) {
	key := os.Getenv("LLAMA_CLOUD_API_KEY")
	if key == "" {
		t.Skip("LLAMA_CLOUD_API_KEY is not set")
	}
	base := os.Getenv("LLAMA_CLOUD_BASE_URL")
	if base == "" {
		t.Fatal("LLAMA_CLOUD_API_KEY is set and LLAMA_CLOUD_BASE_URL is not; name the key's region")
	}
	client, err := New(Config{
		APIKey: key, BaseURL: base, Tier: "fast", Version: "latest",
		PollInterval: 2 * time.Second, Timeout: 5 * time.Minute, MaxResponseBytes: 8 << 20,
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

	// Also settles the one thing the documentation leaves unsaid: that
	// page_number counts from 1.
	if len(document.Pages) != 2 || document.Pages[0].Number != 1 || document.Pages[1].Number != 2 {
		t.Fatalf("pages = %+v\n%s", document.Pages, document.Markdown)
	}
	if !strings.Contains(document.PageMarkdown(0), "first page") || !strings.Contains(document.PageMarkdown(1), "second page") {
		t.Errorf("markdown = %q", document.Markdown)
	}
}
