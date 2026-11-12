package markdownmemory

import (
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythontext"
)

// The three pieces of Python string behaviour the memory parser rests on and
// that Go does not share. Each was checked against the running Python tier
// through PUT /api/v1/users/me/memory, whose response is the re-rendered
// document, and the vectors are in python_text_test.go.
//
// Do not replace these with strings.TrimSpace, strings.Split or
// unicode.IsLetter: each would be a different document for input a client can
// send today.

// pythonSpace, pythonStrip and pythonRightStrip are internal/pythontext's,
// under this slice's own names. They moved there when a second slice needed
// them — internal/agentrules strips a rule's permission and pattern — because
// two spellings of one mirror is the thing a byte comparison exists to
// prevent. The vectors that pin them stay in python_text_test.go beside the
// parser they decide.
func pythonSpace(r rune) bool {
	return pythontext.IsSpace(r)
}

// pythonStrip is str.strip() with no argument.
func pythonStrip(text string) string {
	return pythontext.Strip(text)
}

// pythonRightStrip is str.rstrip() with no argument.
func pythonRightStrip(text string) string {
	return pythontext.RightStrip(text)
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
