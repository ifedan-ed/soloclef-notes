package searchconnectors

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// This file is Python's own behaviour for a value json.loads produced: its
// truth, its str() and its repr(). None of it is this app's logic, and all of
// it reaches a response -- `config or {}` decides between a 403 and a 500, and
// str() of a header value that is not text is what its mask shows the last
// four characters of. Every function here is held to
// testdata/python_values.json, recorded from Python itself while the Python
// version still ran. The recording is the reference now; it is not re-made.

// truthy is bool() of a value json.loads produced: None, False, zero in either
// number form, and the empty string, list and object are false.
func truthy(value any) bool {
	switch held := value.(type) {
	case nil:
		return false
	case bool:
		return held
	case string:
		return held != ""
	case pythonjson.RawInt:
		return strings.Trim(strings.TrimPrefix(string(held), "-"), "0") != ""
	case float64:
		return held != 0
	case []any:
		return len(held) > 0
	case *pythonjson.Object:
		return held.Len() > 0
	}
	// readColumn produces nothing else. A value of another type is a mistake
	// in this package; reading it as true sends it on to a type test that
	// refuses it rather than quietly treating it as absent.
	return true
}

// pyStr is str() of such a value, which is what mask_value masks when a header
// holds something other than text.
//
// For text it is the text. For everything else it is repr(): `None`, `True`,
// an integer's digits, a float as Python writes one (`1e+22`, `inf`), and a
// list or an object in Python's own notation -- single quotes, `: ` and `, `,
// with every string inside it escaped as repr escapes one.
func pyStr(value any) string {
	if text, isText := value.(string); isText {
		return text
	}
	var out strings.Builder
	writeRepr(&out, value)
	return out.String()
}

func writeRepr(out *strings.Builder, value any) {
	switch held := value.(type) {
	case nil:
		out.WriteString("None")
	case bool:
		if held {
			out.WriteString("True")
		} else {
			out.WriteString("False")
		}
	case string:
		writeTextRepr(out, held)
	case pythonjson.RawInt:
		out.WriteString(string(held))
	case float64:
		out.WriteString(floatRepr(held))
	case []any:
		out.WriteByte('[')
		for index, item := range held {
			if index > 0 {
				out.WriteString(", ")
			}
			writeRepr(out, item)
		}
		out.WriteByte(']')
	case *pythonjson.Object:
		out.WriteByte('{')
		for index, key := range held.Keys() {
			if index > 0 {
				out.WriteString(", ")
			}
			writeTextRepr(out, key)
			out.WriteString(": ")
			item, _ := held.Get(key)
			writeRepr(out, item)
		}
		out.WriteByte('}')
	}
}

// floatRepr is repr() of a float. pythonjson.Marshal already writes a finite
// one that way, because that is what json.dumps does; the two it refuses are
// the two json.loads can still produce from a number too large for a float64,
// and Python spells them `inf` and `-inf`.
func floatRepr(value float64) string {
	if math.IsInf(value, 1) {
		return "inf"
	}
	if math.IsInf(value, -1) {
		return "-inf"
	}
	rendered, err := pythonjson.Marshal(value)
	if err != nil {
		// A NaN, which no JSON text decodes to.
		return "nan"
	}
	return string(rendered)
}

// writeTextRepr is repr() of a str.
//
// The quote is a single one unless the text holds a single quote and no
// double quote. A backslash, the chosen quote, tab, newline and return are
// escaped by name; any other character str.isprintable() refuses is written
// as \xNN, \uNNNN or \UNNNNNNNN, by how wide it is; everything else is itself.
func writeTextRepr(out *strings.Builder, text string) {
	quote := byte('\'')
	if strings.ContainsRune(text, '\'') && !strings.ContainsRune(text, '"') {
		quote = '"'
	}
	out.WriteByte(quote)
	for len(text) > 0 {
		// One code point at a time, a lone surrogate being one (surrogates.go)
		// -- and unprintable, so it is written as its `\udXXX` escape.
		character, width := decodeCodePoint(text)
		switch {
		case character == rune(quote) || character == '\\':
			out.WriteByte('\\')
			out.WriteRune(character)
		case character == '\t':
			out.WriteString(`\t`)
		case character == '\n':
			out.WriteString(`\n`)
		case character == '\r':
			out.WriteString(`\r`)
		case pyPrintable(character):
			out.WriteString(text[:width])
		default:
			writeHexEscape(out, character)
		}
		text = text[width:]
	}
	out.WriteByte(quote)
}

func writeHexEscape(out *strings.Builder, character rune) {
	digits := strconv.FormatInt(int64(character), 16)
	switch {
	case character <= 0xff:
		out.WriteString(`\x` + strings.Repeat("0", 2-len(digits)) + digits)
	case character <= 0xffff:
		out.WriteString(`\u` + strings.Repeat("0", 4-len(digits)) + digits)
	default:
		out.WriteString(`\U` + strings.Repeat("0", 8-len(digits)) + digits)
	}
}

// pyPrintable is str.isprintable() for one character: everything but the
// "Other" and "Separator" categories, with the ASCII space let through.
//
// unicode.IsPrint is that same definition -- letters, marks, numbers,
// punctuation, symbols and the ASCII space -- over the same Unicode version
// this deployment's Python carries (15.0.0). That is a coincidence of two
// releases and not a guarantee, so TestEveryCodePointIsPrintableAsPythonSaysItIs
// sweeps all 1,112,064 scalar values against Python's own list and fails the
// day either side moves.
func pyPrintable(character rune) bool {
	return unicode.IsPrint(character)
}

// lowersToASCII is the whole of str.lower() that an ASCII search can see: the
// non-ASCII code points whose lower case contains an ASCII character. Python
// has exactly two. U+0130, the dotted capital I, lowers to an `i` *followed by
// a combining dot*, so a name spelt with it in place of the I of "NAVIGATE" does not spell "navigate" there -- and
// does under Go's strings.ToLower, which lowers it to a bare `i`. U+212A, the
// Kelvin sign, lowers to `k`.
//
// TestTheLoweringTableIsPythons holds this to a sweep of every code point.
var lowersToASCII = map[rune]string{
	0x0130: "i\u0307",
	0x212a: "k",
}

// pyLowerForASCIISearch lowers a name far enough to look for ASCII words in
// it, which is all always_asks does with the result. ASCII letters are
// lowered; the two code points above become what Python makes of them; every
// other character is left as it is, because whatever Python lowers it to is
// not ASCII and so cannot be part of a match either way.
func pyLowerForASCIISearch(name string) string {
	var out strings.Builder
	for _, character := range name {
		switch {
		case character >= 'A' && character <= 'Z':
			out.WriteRune(character + ('a' - 'A'))
		case character < utf8.RuneSelf:
			out.WriteRune(character)
		default:
			if lowered, special := lowersToASCII[character]; special {
				out.WriteString(lowered)
			} else {
				out.WriteRune(character)
			}
		}
	}
	return out.String()
}
