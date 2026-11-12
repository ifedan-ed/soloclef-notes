package tablesafe

import (
	"strings"
	"unicode/utf8"
)

// The three Python string operations this package is built out of. Go has a
// function with the same name for each, and none of the three means the same
// thing, so each is written out here against CPython's behaviour rather than
// borrowed from the standard library.
//
// Every one of them decides where a chunk begins or ends, so a disagreement
// of one character is a chunk that no longer matches the one in the database.

// strip is Python's str.strip() with no argument.
//
// strings.TrimSpace is not that function: Python strips the four
// information-separator controls U+001C-U+001F as whitespace and Go's
// unicode.IsSpace does not, so a document carrying one of them would be
// stripped to a different length and every chunk after it would move.
func strip(text string) string {
	start, end := 0, len(text)
	for start < end {
		r, width := utf8.DecodeRuneInString(text[start:])
		if !isPythonSpace(r) {
			break
		}
		start += width
	}
	for end > start {
		r, width := utf8.DecodeLastRuneInString(text[:end])
		if !isPythonSpace(r) {
			break
		}
		end -= width
	}
	return text[start:end]
}

// isBlank is Python's `not text.strip()`: text is empty or all whitespace.
func isBlank(text string) bool {
	for _, r := range text {
		if !isPythonSpace(r) {
			return false
		}
	}
	return true
}

// isPythonSpace holds the code points str.isspace() answers True for, taken
// from CPython rather than from Unicode's White_Space property: the two
// differ, and it is CPython's answer that cut the stored chunks.
//
// Checked against the running backend:
//
//	python -c "import re; print([hex(c) for c in range(0x3001) if chr(c).isspace()])"
func isPythonSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ',
		0x1c, 0x1d, 0x1e, 0x1f,
		0x85, 0xa0, 0x1680,
		0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// splitLinesKeepingEnds is Python's str.splitlines(keepends=True).
//
// It is NOT strings.SplitAfter(text, "\n"). Python ends a line at any of
// eleven boundaries -- \n, \r, \r\n, \v, \f, U+001C, U+001D, U+001E, U+0085,
// U+2028, U+2029 -- and the table detector is built entirely out of line
// numbers, so a form feed inside a document moves every line index after it
// and with them every table offset.
//
// Note which controls are line boundaries and which are only whitespace:
// U+001C-U+001E end a line, U+001F does not, although str.strip() strips all
// four. The two sets are different and both are CPython's.
//
// The returned lines carry their ending, so they concatenate back into text
// and their lengths are the offsets the caller needs.
func splitLinesKeepingEnds(text string) []string {
	var lines []string
	start := 0
	for at := 0; at < len(text); {
		r, width := utf8.DecodeRuneInString(text[at:])
		if !isPythonLineBoundary(r) {
			at += width
			continue
		}
		at += width
		// CRLF is one boundary, not two: an \n straight after an \r belongs
		// to the same line ending.
		if r == '\r' && at < len(text) && text[at] == '\n' {
			at++
		}
		lines = append(lines, text[start:at])
		start = at
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

// isPythonLineBoundary holds the code points str.splitlines() ends a line at.
//
// Checked against the running backend:
//
//	python -c "print([hex(c) for c in range(0x3001) if len(('a'+chr(c)+'b').splitlines())>1])"
func isPythonLineBoundary(r rune) bool {
	switch r {
	case '\n', '\v', '\f', '\r', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}

// hasPythonIndent is Python's `line.startswith(("    ", "\t"))`: the test for
// an indented code block, which is never a table however many pipes it holds.
func hasPythonIndent(line string) bool {
	return strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")
}
