package tablesafe

import "unicode/utf8"

// trimPythonWhitespace is Python's str.strip() with no argument.
//
// Go's strings.TrimSpace is not the same function: Python strips the four
// information-separator controls U+001C-U+001F as whitespace and Go's
// unicode.IsSpace does not, so a document carrying one of them would be
// stripped to a different length and every chunk after it would move.
func trimPythonWhitespace(text string) string {
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

// isPythonSpace holds the code points str.isspace() answers True for, taken
// from CPython rather than from Unicode's White_Space property: the two
// differ, and it is CPython's answer that cut the stored chunks.
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
