package documents

import (
	"strings"
	"unicode/utf8"
)

// starletteQuery is a request's query string as the Python tier reads it:
// `parse_qsl(query_string.decode("latin-1"), keep_blank_values=True)`, put
// into a dict so that the last value of a name is the one a route sees.
//
// The four reads beside this file (reads.go) use it instead of net/url, because
// net/url reads three things differently and one of these routes writes a
// query value back into its answer (`Invalid document id: …`), where every
// difference is visible:
//
//   - `?document_ids=61;63`: Go has refused a semicolon in a query since 1.17
//     and drops the whole pair, so the parameter would be *missing* -- a 422
//     here where the Python route answers 400 and names "61;63";
//   - `?document_ids=%zz`: Go drops a pair with a broken escape; Python keeps
//     the three characters as they are;
//   - `?document_ids=%E4%B8`: bytes that are not UTF-8. Go hands them on as
//     they are; Python replaces them with U+FFFD, one for each run of bytes
//     that could have begun a character.
//
// The older routes of this package still read their query with net/url, as
// every other slice of this tier does. That is the same divergence on their
// parameters, and it is tier-wide rather than theirs; it is written up in
// the hand-over notes instead of being changed under five replayed routes.
type starletteQuery map[string]string

// readStarletteQuery reads the raw query string (the part after the `?`,
// undecoded).
func readStarletteQuery(raw string) starletteQuery {
	read := starletteQuery{}
	for _, pair := range strings.Split(raw, "&") {
		// parse_qsl skips an empty piece (`a=1&&b=2`), and with
		// keep_blank_values a name with no `=` is that name with "".
		if pair == "" {
			continue
		}
		name, value, _ := strings.Cut(pair, "=")
		read[unquotePlus(name)] = unquotePlus(value)
	}
	return read
}

// value is the parameter's last value and whether the request named it at
// all, which for a required parameter is the difference between a value and
// "Field required".
func (q starletteQuery) value(name string) (string, bool) {
	text, named := q[name]
	return text, named
}

// unquotePlus is urllib.parse.unquote_plus over text that was decoded as
// latin-1: a plus is a space, each unbroken run of `%XX` escapes is decoded
// as UTF-8 with errors="replace", and every other byte is the character of
// the same number.
func unquotePlus(text string) string {
	var decoded strings.Builder
	for index := 0; index < len(text); {
		if run, length := escapedRun(text[index:]); length > 0 {
			decoded.WriteString(decodeReplacing(run))
			index += length
			continue
		}
		switch character := text[index]; {
		case character == '+':
			decoded.WriteByte(' ')
		default:
			// latin-1: the byte is the code point. For ASCII that is the
			// byte itself; a raw byte past 0x7F in a query string becomes
			// the two-byte character of that number, as it does there.
			decoded.WriteRune(rune(character))
		}
		index++
	}
	return decoded.String()
}

// escapedRun reads the `%XX` escapes text opens with: the bytes they name and
// how much of text they took. A percent sign not followed by two hexadecimal
// digits is not an escape and ends the run; it is then an ordinary character.
func escapedRun(text string) ([]byte, int) {
	var named []byte
	length := 0
	for length+2 < len(text) && text[length] == '%' {
		high, highOK := hexadecimal(text[length+1])
		low, lowOK := hexadecimal(text[length+2])
		if !highOK || !lowOK {
			break
		}
		named = append(named, high<<4|low)
		length += 3
	}
	return named, length
}

func hexadecimal(character byte) (byte, bool) {
	switch {
	case character >= '0' && character <= '9':
		return character - '0', true
	case character >= 'a' && character <= 'f':
		return character - 'a' + 10, true
	case character >= 'A' && character <= 'F':
		return character - 'A' + 10, true
	}
	return 0, false
}

// decodeReplacing is bytes.decode("utf-8", errors="replace").
//
// The two languages agree on what is valid and disagree on how much one
// U+FFFD stands for. Go's decoder gives one per bad byte. CPython gives one
// per "maximal subpart": the longest run of bytes that is the start of some
// valid character. So `E4 B8` with nothing after it is one replacement there
// and two here, and that is what this function exists to match.
func decodeReplacing(encoded []byte) string {
	var decoded strings.Builder
	for len(encoded) > 0 {
		character, size := utf8.DecodeRune(encoded)
		if character != utf8.RuneError || size > 1 {
			decoded.WriteRune(character)
			encoded = encoded[size:]
			continue
		}
		decoded.WriteRune(utf8.RuneError)
		encoded = encoded[maximalSubpart(encoded):]
	}
	return decoded.String()
}

// maximalSubpart is how many bytes of an invalid sequence one replacement
// character stands for: the lead byte and each continuation byte after it
// that a valid character could have had there, stopping before the one that
// could not be (Unicode 15, table 3-7, and the rule CPython's decoder
// follows).
func maximalSubpart(encoded []byte) int {
	lead := encoded[0]
	var wanted int
	// The second byte's allowed range is narrower after four lead bytes,
	// which is what excludes overlong forms, surrogates and code points past
	// U+10FFFF.
	low, high := byte(0x80), byte(0xBF)
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		wanted = 1
	case lead >= 0xE0 && lead <= 0xEF:
		wanted = 2
		switch lead {
		case 0xE0:
			low = 0xA0
		case 0xED:
			high = 0x9F
		}
	case lead >= 0xF0 && lead <= 0xF4:
		wanted = 3
		switch lead {
		case 0xF0:
			low = 0x90
		case 0xF4:
			high = 0x8F
		}
	default:
		// A continuation byte with no lead, or a byte no character starts
		// with: it is replaced alone.
		return 1
	}
	length := 1
	for length <= wanted && length < len(encoded) {
		if encoded[length] < low || encoded[length] > high {
			break
		}
		low, high = 0x80, 0xBF
		length++
	}
	return length
}
