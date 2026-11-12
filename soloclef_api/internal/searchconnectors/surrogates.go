package searchconnectors

import "unicode/utf8"

// A Python str can hold a lone surrogate and a Go string of valid UTF-8
// cannot. The values this package reads out of a `json` column can hold one
// (column.go), and Python then treats that str differently in five places:
// its length, its last four characters, its repr, whether it can be
// percent-encoded, and whether pydantic will write it.
//
// So a lone surrogate is kept in a Go string as the three bytes it would
// have if UTF-8 encoded surrogates: ED A0 80 for U+D800 through ED BF BF for
// U+DFFF. That is deliberately *not* valid UTF-8 -- in valid UTF-8 the byte
// ED is never followed by A0 or above -- so no decoded text can be mistaken
// for one, and the functions below are the only ones that need to know.

// surrogateBytes is those three bytes.
func surrogateBytes(surrogate rune) string {
	return string([]byte{
		0xed,
		0x80 | byte(surrogate>>6)&0x3f,
		0x80 | byte(surrogate)&0x3f,
	})
}

// hasLoneSurrogate reports whether text holds one.
func hasLoneSurrogate(text string) bool {
	for index := 0; index+1 < len(text); index++ {
		if text[index] == 0xed && text[index+1] >= 0xa0 {
			return true
		}
	}
	return false
}

// decodeCodePoint is utf8.DecodeRuneInString that reads a lone surrogate as
// one code point, three bytes wide, as Python counts it.
func decodeCodePoint(text string) (rune, int) {
	if len(text) >= 3 && text[0] == 0xed && text[1] >= 0xa0 && text[1] <= 0xbf &&
		text[2] >= 0x80 && text[2] <= 0xbf {
		return 0xd000 | rune(text[1]&0x3f)<<6 | rune(text[2]&0x3f), 3
	}
	return utf8.DecodeRuneInString(text)
}

// codePointLen is len() of a str, which counts code points and not bytes.
func codePointLen(text string) int {
	count := 0
	for len(text) > 0 {
		_, width := decodeCodePoint(text)
		text = text[width:]
		count++
	}
	return count
}

// lastCodePoints is text[-count:].
func lastCodePoints(text string, count int) string {
	starts := make([]int, 0, len(text))
	for offset := 0; offset < len(text); {
		starts = append(starts, offset)
		_, width := decodeCodePoint(text[offset:])
		offset += width
	}
	if count >= len(starts) {
		return text
	}
	return text[starts[len(starts)-count]:]
}
