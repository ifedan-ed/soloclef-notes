// Package pythonjson writes text as Python's json module writes it with
// ensure_ascii=False, which is the spelling the smtp2go API's request body was
// pinned to (httpx 0.28.1, _content.py encode_json) when the sender was moved
// to Go. A payload written another way is still valid JSON, which is why
// nothing would fail until a provider rendered it differently.
package pythonjson

import (
	"fmt"
	"strings"
)

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
