package markdownmemory

import (
	"strings"
	"unicode"
)

// The three pieces of Python string behaviour the memory parser rests on and
// that Go does not share. Each was checked against the running Python tier
// through PUT /api/v1/users/me/memory, whose response is the re-rendered
// document, and the vectors are in python_text_test.go.
//
// Do not replace these with strings.TrimSpace, strings.Split or
// unicode.IsLetter: each would be a different document for input a client can
// send today.

// pythonSpace is the character set str.strip() removes, which is str.isspace()
// — the Unicode White_Space property *plus* the four C0 separators U+001C to
// U+001F, which Go's unicode.IsSpace does not include. A heading line wrapped
// in U+001C is stripped on the Python tier.
func pythonSpace(r rune) bool {
	if r >= 0x1c && r <= 0x1f {
		return true
	}
	return unicode.IsSpace(r)
}

// pythonStrip is str.strip() with no argument.
func pythonStrip(text string) string {
	return strings.TrimFunc(text, pythonSpace)
}

// pythonRightStrip is str.rstrip() with no argument.
func pythonRightStrip(text string) string {
	return strings.TrimRightFunc(text, pythonSpace)
}

// lineBoundaries is str.splitlines()' own set, which is wider than "\n": it
// includes the vertical tab, the form feed, the three C1/C0 separators, the
// next line, and the two Unicode line and paragraph separators. A document
// carrying U+2028 or U+001C is several lines to the Python parser and one line
// to a naive Go split, which was checked both ways.
var lineBoundaries = map[rune]bool{
	'\n':     true, // line feed
	'\v':     true, // vertical tab
	'\f':     true, // form feed
	'\r':     true, // carriage return, and "\r\n" as one boundary
	'\u001c': true, // file separator
	'\u001d': true, // group separator
	'\u001e': true, // record separator
	'\u0085': true, // next line
	' ':      true, // line separator
	' ':      true, // paragraph separator
}

// pythonSplitLines is str.splitlines(): it splits on every boundary above,
// treats "\r\n" as one, and yields no trailing empty line for a document that
// ends with a boundary.
func pythonSplitLines(text string) []string {
	lines := []string{}
	current := strings.Builder{}
	runes := []rune(text)
	for index := 0; index < len(runes); index++ {
		character := runes[index]
		if !lineBoundaries[character] {
			current.WriteRune(character)
			continue
		}
		if character == '\r' && index+1 < len(runes) && runes[index+1] == '\n' {
			index++
		}
		lines = append(lines, current.String())
		current.Reset()
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return lines
}

// pythonIsAlphanumeric is str.isalnum() for one character: isalpha, isdecimal,
// isdigit or isnumeric. Those are the letter categories plus every numeric
// category, which is unicode.IsLetter or unicode.IsNumber here — so "²" and
// "〇" count, as they do there.
func pythonIsAlphanumeric(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}
