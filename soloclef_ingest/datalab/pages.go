package datalab

import (
	"regexp"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// pageMarker is the line Datalab writes before each page when asked to
// paginate: the page's index from 0 in braces, then 48 dashes.
//
//	{0}------------------------------------------------
//
// Exactly 48 and the whole line, so prose that happens to contain "{3}--"
// is not taken for a page break.
var pageMarker = regexp.MustCompile(`(?m)^\{(\d{1,9})\}-{48}[ \t]*$`)

// paged removes the page markers and records where each page landed.
func paged(markdown string) (parse.Document, error) {
	markers := pageMarker.FindAllStringSubmatchIndex(markdown, -1)
	if len(markers) == 0 {
		// A format with no pages (a spreadsheet, an image).
		return parse.Unpaged(parse.Datalab, strings.TrimSpace(markdown))
	}
	builder := parse.Builder{Separator: "\n\n"}
	builder.Grow(len(markdown))
	for index, marker := range markers {
		end := len(markdown)
		if index+1 < len(markers) {
			end = markers[index+1][0]
		}
		body := markdown[marker[1]:end]
		if index == 0 {
			// Anything before the first marker belongs to the first page.
			body = markdown[:marker[0]] + body
		}
		// Cannot fail: the pattern admits one to nine digits.
		number, _ := strconv.Atoi(markdown[marker[2]:marker[3]])
		// Datalab counts pages from 0; a person, and Document, from 1.
		builder.Page(number+1, strings.Trim(body, "\n"))
	}
	return builder.Document(parse.Datalab)
}
