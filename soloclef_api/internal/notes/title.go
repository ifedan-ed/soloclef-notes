package notes

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// titleFromTextLength is the longest title taken from a note's text, in
// characters: a line of a list, not a paragraph.
const titleFromTextLength = 80

// The Markdown a title taken from text should not show.
var (
	// [citation:12] and [citation:12, 13]: the chat's citation tokens, which
	// mean something in an answer and nothing in a title.
	citationToken = regexp.MustCompile(`\[citation:[^\]]*\]`)
	// ![alt](src) is dropped; [text](href) keeps its text.
	markdownImage = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	markdownLink  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	// A line's block marker: a heading, a quote, a list item, a task box.
	blockMarker = regexp.MustCompile(`^\s*(?:#{1,6}\s+|>\s*|[-*+]\s+(?:\[[ xX]\]\s+)?|\d+[.)]\s+)+`)
	// Inline emphasis and code marks, and a maths delimiter.
	inlineMarks = regexp.MustCompile("[*_`~]+|\\$+")
	// A line that is a fence, a rule or a table's divider says nothing.
	emptyLine = regexp.MustCompile(`^\s*(?:` + "```" + `.*|~~~.*|[-*_=\s]{3,}|\|?[\s:|-]+\|?)?\s*$`)
)

// NoteTitle is the title a note is written with: the one asked for, trimmed;
// else the first line of its text with the Markdown taken off; else
// "Untitled note".
func NoteTitle(requested, text string) string {
	if title := strings.Join(strings.Fields(requested), " "); title != "" {
		return title
	}
	if title := TitleFromText(text); title != "" {
		return title
	}
	return untitled
}

// TitleFromText is the first line of a note's Markdown that has words in it,
// as plain words, cut at titleFromTextLength characters with an ellipsis.
//
// This is a title for a kept chat answer or a note written without one, so
// it has to read well rather than be exact: link text is kept and its
// address dropped, citation tokens and emphasis marks go, and maths is left
// as its source with the dollar signs removed.
func TitleFromText(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if emptyLine.MatchString(line) {
			continue
		}
		line = citationToken.ReplaceAllString(line, "")
		line = markdownImage.ReplaceAllString(line, "")
		line = markdownLink.ReplaceAllString(line, "$1")
		line = blockMarker.ReplaceAllString(line, "")
		line = inlineMarks.ReplaceAllString(line, "")
		line = strings.Trim(strings.Join(strings.Fields(line), " "), " |")
		if line == "" {
			continue
		}
		return shorten(line, titleFromTextLength)
	}
	return ""
}

// shorten cuts text to at most limit characters, at a word where one is
// near, and marks the cut with an ellipsis.
func shorten(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)[:limit-1]
	cut := string(runes)
	// Back to the last space if it is in the last fifth: a cut mid-word
	// reads as a typo, but a title cut to half its length reads worse.
	if space := strings.LastIndex(cut, " "); space > len(cut)*4/5 {
		cut = cut[:space]
	}
	return strings.TrimRight(cut, " ,;:.-") + "…"
}
