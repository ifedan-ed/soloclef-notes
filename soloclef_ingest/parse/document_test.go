package parse

import (
	"errors"
	"fmt"
	"testing"
)

func TestBuilderRecordsEachPagesSpan(t *testing.T) {
	builder := Builder{Separator: "\n\n"}
	builder.Page(1, "# One")
	builder.Page(2, "")
	builder.Page(3, "Three")

	document, err := builder.Document(Datalab)
	if err != nil {
		t.Fatal(err)
	}
	if document.Markdown != "# One\n\nThree" {
		t.Fatalf("markdown = %q", document.Markdown)
	}
	if got := document.PageMarkdown(0); got != "# One" {
		t.Errorf("page 1 = %q", got)
	}
	// A blank page keeps its number and takes no separator of its own.
	if page := document.Pages[1]; page.Number != 2 || page.Start != page.End {
		t.Errorf("blank page = %+v", page)
	}
	if got := document.PageMarkdown(2); got != "Three" {
		t.Errorf("page 3 = %q", got)
	}
	if document.Parser != Datalab {
		t.Errorf("parser = %q", document.Parser)
	}
}

func TestBuilderWithNoSeparatorConcatenates(t *testing.T) {
	var builder Builder
	builder.Page(1, "a\n\n")
	builder.Page(2, "b\n\n")

	document, err := builder.Document(Local)
	if err != nil {
		t.Fatal(err)
	}
	if document.Markdown != "a\n\nb\n\n" || document.PageMarkdown(1) != "b\n\n" {
		t.Fatalf("document = %+v", document)
	}
}

func TestADocumentWithNoTextIsRefused(t *testing.T) {
	var builder Builder
	builder.Page(1, " \n")
	if _, err := builder.Document(Local); KindOf(err) != NoText {
		t.Fatalf("paged: err = %v", err)
	}
	if _, err := Unpaged(Builtin, "\n\t"); KindOf(err) != NoText {
		t.Fatalf("unpaged: err = %v", err)
	}
}

func TestErrorKindSurvivesWrapping(t *testing.T) {
	cause := errors.New("connection refused")
	err := fmt.Errorf("indexing document 7: %w", &Error{
		Kind: Unavailable, Parser: Local, Message: "the parser service did not answer", Err: cause,
	})

	if KindOf(err) != Unavailable {
		t.Fatalf("kind = %q", KindOf(err))
	}
	if !errors.Is(err, cause) {
		t.Fatal("the cause is not reachable")
	}
	if KindOf(cause) != "" {
		t.Fatal("a plain error has no kind")
	}
}

func TestOnlyTransientKindsAreRetryable(t *testing.T) {
	retryable := map[Kind]bool{RateLimited: true, Unavailable: true}
	for _, kind := range []Kind{
		NoText, UnsupportedFormat, ParseFailed, TooLarge, NotConfigured,
		Unauthorized, Rejected, BadResponse, RateLimited, Unavailable, TimedOut,
	} {
		if got := (&Error{Kind: kind}).Retryable(); got != retryable[kind] {
			t.Errorf("%s: retryable = %v", kind, got)
		}
	}
}
