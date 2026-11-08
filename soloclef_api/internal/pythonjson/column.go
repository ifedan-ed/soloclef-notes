package pythonjson

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// MarshalColumn renders a value as SQLAlchemy writes it into a `json` column,
// which is not how Starlette writes a response.
//
// SQLAlchemy's JSON type serialises with `json.dumps` and the standard
// library's own defaults, so the stored text carries a space after every comma
// and colon and escapes every non-ASCII character — `{"a": 1, "u": "é"}`
// rather than `{"a":1,"u":"é"}`. The difference never reaches a response, since
// both tiers read the column back through json.loads before rendering it; it
// reaches the *row*, and a write that stored the compact form would leave a
// different database behind than the route it replaces.
//
// Read off a row the Python tier wrote, rather than from the documentation.
func MarshalColumn(v any) ([]byte, error) {
	var b strings.Builder
	if err := writeColumn(&b, v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func writeColumn(b *strings.Builder, v any) error {
	switch value := v.(type) {
	case []any:
		b.WriteByte('[')
		for index, item := range value {
			if index > 0 {
				b.WriteString(", ")
			}
			if err := writeColumn(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case *Object:
		b.WriteByte('{')
		for index, key := range value.keys {
			if index > 0 {
				b.WriteString(", ")
			}
			writeASCIIString(b, key)
			b.WriteString(": ")
			if err := writeColumn(b, value.values[key]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case string:
		writeASCIIString(b, value)
	default:
		// Scalars are written identically either way, so the one renderer
		// decides them.
		return write(b, v)
	}
	return nil
}

// writeASCIIString is CPython's json escaping with ensure_ascii left at its
// default: the quote, the backslash, the five short escapes, and every other
// character outside printable ASCII as \uXXXX — astral characters as a
// surrogate pair, which is what CPython emits.
func writeASCIIString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			switch {
			case r >= 0x20 && r < 0x7f:
				b.WriteRune(r)
			case r > 0xffff:
				high, low := utf16.EncodeRune(r)
				writeEscape(b, high)
				writeEscape(b, low)
			case r == utf8.RuneError:
				writeEscape(b, utf8.RuneError)
			default:
				writeEscape(b, r)
			}
		}
	}
	b.WriteByte('"')
}

func writeEscape(b *strings.Builder, r rune) {
	b.WriteString(`\u`)
	text := strconv.FormatInt(int64(r), 16)
	for len(text) < 4 {
		text = "0" + text
	}
	b.WriteString(text)
}
