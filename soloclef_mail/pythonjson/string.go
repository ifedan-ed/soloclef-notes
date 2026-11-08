// Package pythonjson writes text and numbers as Python's json module writes
// them.
//
// Two spellings, because this app's two JSON payloads use different flags and
// the difference is visible on the wire: the queue entry is json.dumps at its
// default ensure_ascii=True (app/mail_queue/job.py), and the smtp2go API
// request body is httpx's ensure_ascii=False (httpx 0.28.1, _content.py
// encode_json). A payload written with the wrong one is still valid JSON,
// which is why nothing fails until a provider renders it.
package pythonjson

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// WriteInt writes a Python int: base 10, no plus sign, which is strconv's
// default.
func WriteInt(b *strings.Builder, n int) {
	b.WriteString(strconv.Itoa(n))
}

// WriteASCIIOnly writes a Python str as json.dumps writes it with its default
// ensure_ascii=True: the six short escapes, \u00XX for the other C0
// controls, and \uXXXX for every byte above 0x7e -- including DEL, which the C
// encoder escapes because its raw range is 0x20..0x7e. Astral characters go out
// as the surrogate pair Python writes, which is why this walks UTF-16 units.
func WriteASCIIOnly(b *strings.Builder, s string) {
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
			case r >= 0x20 && r <= 0x7e:
				b.WriteByte(byte(r))
			case r > 0xffff:
				high := 0xd800 + ((r - 0x10000) >> 10)
				low := 0xdc00 + ((r - 0x10000) & 0x3ff)
				fmt.Fprintf(b, `\u%04x\u%04x`, high, low)
			default:
				fmt.Fprintf(b, `\u%04x`, r)
			}
		}
	}
	b.WriteByte('"')
}

// FormatFloat is repr(float): the shortest text that round trips, fixed
// while 1e-4 <= |x| < 1e16 and in exponent notation outside it, always with a
// decimal point or an exponent.
//
// The same renderer as soloclef_api/internal/pythonjson.formatFloat, which is
// in another module's internal tree and so cannot be imported. Changing one
// without the other is how enqueued_at starts disagreeing by a byte.
func FormatFloat(f float64) (string, error) {
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

// WriteUTF8 writes a Python str as json.dumps writes it with ensure_ascii=False:
// the six short escapes, \u00XX for the other C0 controls, and every other
// character as itself.
//
// Go's own encoder cannot be used for this even with SetEscapeHTML off,
// because it escapes U+2028 and U+2029 and Python does not -- and an
// automation's output is markdown someone else's agent wrote, so a line
// separator in it is not hypothetical.
func WriteUTF8(b *strings.Builder, s string) {
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
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}
