package datalab

import (
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

const rule = "------------------------------------------------" // 48 dashes

func TestTextBeforeTheFirstMarkerBelongsToTheFirstPage(t *testing.T) {
	document, err := paged("Cover line\n\n{0}" + rule + "\n\nBody\n\n{1}" + rule + "\n\nNext\n")
	if err != nil {
		t.Fatal(err)
	}
	if document.PageMarkdown(0) != "Cover line\n\n\n\nBody" || document.PageMarkdown(1) != "Next" {
		t.Fatalf("pages = %q, %q", document.PageMarkdown(0), document.PageMarkdown(1))
	}
}

func TestPageNumbersFollowTheMarkersNotTheirPosition(t *testing.T) {
	// A page_range request returns only the pages asked for.
	document, err := paged("{4}" + rule + "\nfive\n{9}" + rule + "\nten\n")
	if err != nil {
		t.Fatal(err)
	}
	if document.Pages[0].Number != 5 || document.Pages[1].Number != 10 {
		t.Fatalf("pages = %+v", document.Pages)
	}
}

func TestABlankPageKeepsItsNumber(t *testing.T) {
	document, err := paged("{0}" + rule + "\none\n{1}" + rule + "\n\n{2}" + rule + "\nthree\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Pages) != 3 || document.PageMarkdown(1) != "" || document.Markdown != "one\n\nthree" {
		t.Fatalf("document = %+v", document)
	}
}

func TestMarkdownWithoutMarkersIsUnpaged(t *testing.T) {
	document, err := paged("\n| a | b |\n|---|---|\n| 1 | 2 |\n")
	if err != nil {
		t.Fatal(err)
	}
	if document.Pages != nil || document.Markdown != "| a | b |\n|---|---|\n| 1 | 2 |" {
		t.Fatalf("document = %+v", document)
	}
}

func TestProseThatLooksAlmostLikeAMarkerIsLeftAlone(t *testing.T) {
	text := "Use {0}---- as the placeholder\n\n{1}--------\n\ninline {2}" + rule
	document, err := paged(text)
	if err != nil {
		t.Fatal(err)
	}
	if document.Pages != nil || !strings.Contains(document.Markdown, "{1}--------") {
		t.Fatalf("document = %+v", document)
	}
}

func TestOnlyMarkersIsNoText(t *testing.T) {
	if _, err := paged("{0}" + rule + "\n\n{1}" + rule + "\n"); parse.KindOf(err) != parse.NoText {
		t.Fatalf("err = %v", err)
	}
}
