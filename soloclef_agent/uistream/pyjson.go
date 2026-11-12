package uistream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// The frames this package writes are byte-for-byte what the Python service
// writes with json.dumps at its defaults: a space after each comma and
// colon, and every character outside printable ASCII as a \uXXXX escape.
//
// The clients parse JSON and would accept any spacing. The bytes are matched
// anyway because it makes a stream recorded from the Python service a valid
// expected output for this one: the two can be compared with a plain diff,
// which is how the change of engine is proved not to have changed the wire.
//
// Two things are outside what can be matched, and callers handing this
// package their own values should know them. A Go map is written with its
// keys sorted, where Python keeps the order they were inserted; a struct, or
// a json.RawMessage built in the wanted order, has no such difference. And a
// number is written as Go formats it, which for a float with no fraction
// drops the ".0" Python keeps. Both parse to the same value.

const hexDigits = "0123456789abcdef"

// appendString writes s as a JSON string the way Python does.
func appendString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			i++
			switch {
			case c == '"' || c == '\\':
				dst = append(dst, '\\', c)
			case c >= 0x20 && c < 0x7f:
				dst = append(dst, c)
			case c == '\n':
				dst = append(dst, '\\', 'n')
			case c == '\r':
				dst = append(dst, '\\', 'r')
			case c == '\t':
				dst = append(dst, '\\', 't')
			case c == '\b':
				dst = append(dst, '\\', 'b')
			case c == '\f':
				dst = append(dst, '\\', 'f')
			default:
				dst = appendEscape(dst, rune(c))
			}
			continue
		}
		// A byte that is not valid UTF-8 decodes as U+FFFD with size 1, and
		// is written as that character. Python strings cannot hold such a
		// byte at all, so there is no Python output to differ from.
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		dst = appendRune(dst, r)
	}
	return append(dst, '"')
}

// appendRune escapes one non-ASCII character, as a surrogate pair when it is
// outside the basic plane, which is how Python writes one.
func appendRune(dst []byte, r rune) []byte {
	if r >= 0x10000 {
		r -= 0x10000
		dst = appendEscape(dst, 0xd800+(r>>10))
		return appendEscape(dst, 0xdc00+(r&0x3ff))
	}
	return appendEscape(dst, r)
}

func appendEscape(dst []byte, r rune) []byte {
	return append(dst, '\\', 'u', hexDigits[r>>12&0xf], hexDigits[r>>8&0xf], hexDigits[r>>4&0xf], hexDigits[r&0xf])
}

// appendValue writes any JSON-encodable value the way Python does. It lets
// encoding/json do the encoding and then respaces and re-escapes the result,
// rather than reimplementing an encoder for every Go type.
func appendValue(dst []byte, v any) ([]byte, error) {
	var compact bytes.Buffer
	enc := json.NewEncoder(&compact)
	// Go would otherwise write <, > and & as six-character unicode escapes,
	// which is valid JSON but not what Python writes.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return dst, fmt.Errorf("uistream: value cannot be encoded: %w", err)
	}
	return respace(dst, bytes.TrimRight(compact.Bytes(), "\n")), nil
}

// respace copies compact JSON, adding Python's spaces between tokens and
// escaping what Python escapes inside strings.
func respace(dst, src []byte) []byte {
	inString := false
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case !inString:
			i++
			dst = append(dst, c)
			switch c {
			case '"':
				inString = true
			case ',', ':':
				dst = append(dst, ' ')
			}
		case c == '\\':
			// An escape is two bytes that go through untouched. (For \uXXXX
			// the four digits follow as ordinary characters.)
			dst = append(dst, c, src[i+1])
			i += 2
		case c == '"':
			i++
			dst = append(dst, c)
			inString = false
		case c == 0x7f:
			i++
			dst = appendEscape(dst, rune(c))
		case c < utf8.RuneSelf:
			i++
			dst = append(dst, c)
		default:
			r, size := utf8.DecodeRune(src[i:])
			i += size
			dst = appendRune(dst, r)
		}
	}
	return dst
}
