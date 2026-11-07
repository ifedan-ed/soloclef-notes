package pythonjson

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// RawInt is an integer kept as the text it arrived as. Python's json round-trip
// of an integer is exact at any width, so the text is reproduced verbatim
// rather than squeezed through int64.
type RawInt string

// Marshal renders a value the way Starlette's JSONResponse renders it.
//
// The accepted value model is deliberately narrow: nil, bool, string, the Go
// integer types, RawInt, float64, []any and *Object. Anything else is an error
// rather than a guess, because a guess here is a response that differs from
// Python's by bytes no test would notice.
func Marshal(v any) ([]byte, error) {
	var b strings.Builder
	if err := write(&b, v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func write(b *strings.Builder, v any) error {
	switch value := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if value {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		writeString(b, value)
	case RawInt:
		b.WriteString(string(value))
	case int:
		b.WriteString(strconv.FormatInt(int64(value), 10))
	case int32:
		b.WriteString(strconv.FormatInt(int64(value), 10))
	case int64:
		b.WriteString(strconv.FormatInt(value, 10))
	case float64:
		text, err := formatFloat(value)
		if err != nil {
			return err
		}
		b.WriteString(text)
	case []any:
		b.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := write(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case *Object:
		b.WriteByte('{')
		for i, key := range value.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, key)
			b.WriteByte(':')
			if err := write(b, value.values[key]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("pythonjson: unsupported type %T", v)
	}
	return nil
}

// writeString escapes exactly what CPython's json escapes with
// ensure_ascii=False: the quote, the backslash, the five short control escapes,
// and every other control character as lowercase \u00xx. Everything else,
// including U+2028, is written through as UTF-8.
func writeString(b *strings.Builder, s string) {
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
			case r < 0x20:
				b.WriteString(fmt.Sprintf(`\u%04x`, r))
			case r == utf8.RuneError:
				// A lone invalid byte. Python decodes the column to str before
				// encoding, so invalid UTF-8 cannot reach it; write the
				// replacement character rather than raw bytes.
				b.WriteRune(utf8.RuneError)
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// formatFloat reproduces Python's repr(float): the shortest text that round
// trips, in fixed notation while 1e-4 <= |x| < 1e16 and in exponent notation
// outside it, always with a decimal point or an exponent.
//
// NaN and the infinities are an error: json.dumps uses allow_nan=False, so the
// Python tier raises instead of emitting them, and this tier must not invent a
// value where Python would have failed.
func formatFloat(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fmt.Errorf("pythonjson: %v is out of range for JSON", f)
	}
	magnitude := math.Abs(f)
	if f != 0 && (magnitude < 1e-4 || magnitude >= 1e16) {
		text := strconv.FormatFloat(f, 'e', -1, 64)
		mantissa, exponent, _ := strings.Cut(text, "e")
		sign := "+"
		if strings.HasPrefix(exponent, "-") || strings.HasPrefix(exponent, "+") {
			sign = exponent[:1]
			exponent = exponent[1:]
		}
		for len(exponent) < 2 {
			exponent = "0" + exponent
		}
		return mantissa + "e" + sign + exponent, nil
	}
	text := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(text, ".") {
		text += ".0"
	}
	return text, nil
}
