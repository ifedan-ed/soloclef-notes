package parse

import "strings"

// Document is a parsed file.
type Document struct {
	// Markdown is the whole document.
	Markdown string
	// Pages locates each source page inside Markdown, in document order. It
	// is nil for a format with no pages (text, CSV, HTML).
	//
	// Pages are byte spans and not copies of the text: the chunker reports
	// the span each chunk was cut from, so a chunk's page is a lookup in this
	// table. Nothing has to search the PDF's text for the chunk again.
	Pages []Page
	// SkippedPages are the page numbers the parser said it could not read.
	// The document is still usable; the caller decides whether to say so.
	SkippedPages []int
	// Parser is who read it.
	Parser ID
}

// Page is one source page's place in Document.Markdown.
type Page struct {
	// Number is the page's number in the source file, counted from 1.
	Number int
	// Start and End bound the page's Markdown: Markdown[Start:End].
	Start, End int
}

// PageMarkdown is the Markdown of d.Pages[index].
func (d Document) PageMarkdown(index int) string {
	page := d.Pages[index]
	return d.Markdown[page.Start:page.End]
}

// Builder assembles a paged Document from pages as a parser yields them, so
// every parser records page offsets the same way.
type Builder struct {
	// Separator goes between two pages that both have text. pymupdf4llm's
	// pages already end in a blank line and take none; the hosted parsers'
	// pages do not and take one.
	Separator string

	markdown strings.Builder
	pages    []Page
	skipped  []int
}

// Grow reserves room for size more bytes of Markdown.
func (b *Builder) Grow(size int) { b.markdown.Grow(size) }

// Page appends one page. A page with no text is still recorded, as an empty
// span, so the page numbers after it stay true.
func (b *Builder) Page(number int, markdown string) {
	if markdown != "" && b.markdown.Len() > 0 {
		b.markdown.WriteString(b.Separator)
	}
	start := b.markdown.Len()
	b.markdown.WriteString(markdown)
	b.pages = append(b.pages, Page{Number: number, Start: start, End: b.markdown.Len()})
}

// Skipped records a page the parser reported as unreadable.
func (b *Builder) Skipped(number int) { b.skipped = append(b.skipped, number) }

// Document is everything appended so far, read by parser. A document with no
// text at all is refused as NoText: indexed, it would match nothing and
// answer every question with silence.
func (b *Builder) Document(parser ID) (Document, error) {
	markdown := b.markdown.String()
	if strings.TrimSpace(markdown) == "" {
		return Document{}, &Error{Kind: NoText, Parser: parser, Message: "the file has no text to read"}
	}
	return Document{Markdown: markdown, Pages: b.pages, SkippedPages: b.skipped, Parser: parser}, nil
}

// Unpaged is a Document for a format with no pages, refused as NoText when
// blank for the same reason as Builder.Document.
func Unpaged(parser ID, markdown string) (Document, error) {
	if strings.TrimSpace(markdown) == "" {
		return Document{}, &Error{Kind: NoText, Parser: parser, Message: "the file has no text to read"}
	}
	return Document{Markdown: markdown, Parser: parser}, nil
}
