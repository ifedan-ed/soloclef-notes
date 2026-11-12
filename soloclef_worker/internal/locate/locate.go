// Package locate says where a chunk sits in its document: which lines, and
// which pages of the source file.
//
// Both answers are arithmetic on the byte span the chunker cut the chunk
// from. Nothing here searches the document for the chunk's text. The Python
// tier's page stamper re-read the PDF's text layer and searched for each
// chunk; a search cannot find a table chunk (it begins with a header that is
// somewhere else) and can find a repeated sentence in the wrong place.
package locate

import (
	"sort"
	"strings"
	"unicode"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// Lines answers line questions about one document. It reads the document
// once, so locating every chunk of a long document is not a rescan each.
type Lines struct {
	text string
	// newlines holds the byte offset of every "\n", in order.
	newlines []int
}

// NewLines indexes the document's line breaks.
func NewLines(text string) *Lines {
	lines := &Lines{text: text}
	for offset := 0; ; {
		next := strings.IndexByte(text[offset:], '\n')
		if next < 0 {
			break
		}
		lines.newlines = append(lines.newlines, offset+next)
		offset += next + 1
	}
	return lines
}

// Content narrows [start, end) to the part that is not surrounding white
// space. A chunk's place is where its real content is: one that ends on a
// newline must not claim the line after it, and one that begins on a blank
// line belongs to the line below. A span that is all white space is returned
// as it is.
func (l *Lines) Content(start, end int) (int, int) {
	span := l.text[start:end]
	trimmed := strings.TrimLeftFunc(span, unicode.IsSpace)
	if trimmed == "" {
		return start, end
	}
	first := start + len(span) - len(trimmed)
	return first, first + len(strings.TrimRightFunc(trimmed, unicode.IsSpace))
}

// Range is the 1-based, inclusive range of lines the content of [start, end)
// sits on. That is what a citation shows, and the convention the rows
// already in the database use.
func (l *Lines) Range(start, end int) (first, last int) {
	start, end = l.Content(start, end)
	// The line of an offset is one more than the newlines before it.
	first = 1 + sort.SearchInts(l.newlines, start)
	// The content's last byte is at end-1; an empty span has none and stays
	// on its first line.
	last = first
	if end > start {
		last = 1 + sort.SearchInts(l.newlines, end-1)
	}
	return first, last
}

// Pages is the range of source pages the span [start, end) of the Markdown
// came from, by the parser's page table. found is false for a document with
// no pages (text, CSV, HTML), whose chunks have no page.
//
// The table is in document order and its spans do not overlap, but they need
// not touch: a parser may put a separator between two pages, and a page with
// no text is an empty span. A span that starts in a separator starts on the
// page after it; one that ends in a separator ends on the page before.
func Pages(pages []parse.Page, start, end int) (first, last int, found bool) {
	if len(pages) == 0 {
		return 0, 0, false
	}
	// The first page that ends after the span starts: the page holding the
	// span's first byte, or the next page when that byte is in a separator.
	from := sort.Search(len(pages), func(i int) bool { return pages[i].End > start })
	if from == len(pages) {
		// Past the last page's text; it can only belong to the last page.
		number := pages[len(pages)-1].Number
		return number, number, true
	}
	// The last page that starts before the span ends.
	to := sort.Search(len(pages), func(i int) bool { return pages[i].Start >= end }) - 1
	// A page with no text is an empty span and holds nothing of this one;
	// step over those at either end.
	for from < len(pages)-1 && pages[from].Start == pages[from].End {
		from++
	}
	for to > from && pages[to].Start == pages[to].End {
		to--
	}
	if to < from {
		to = from
	}
	return pages[from].Number, pages[to].Number, true
}
