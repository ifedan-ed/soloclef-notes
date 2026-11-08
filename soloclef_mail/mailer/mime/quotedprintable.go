package mime

import "strings"

// quotedPrintableBody is email.quoprimime.body_encode with maxlinelen 78 and \n
// line endings.
//
// Its soft-break bookkeeping is reproduced rather than approximated: it refuses
// to break inside an "=XX" escape, and it encodes trailing whitespace on a line
// -- which matters because a line that ends in a space is one a receiving
// server may strip, and then the signature over the body no longer matches.
func quotedPrintableBody(body string) string {
	if body == "" {
		return body
	}
	var quoted strings.Builder
	for i := 0; i < len(body); i++ {
		quoted.WriteString(quopriBodyByte(body[i]))
	}
	translated := quoted.String()

	const softBreak = "=\n"
	maxlinelen1 := maxLineLength - 1

	var out []string
	for _, line := range splitLines([]byte(translated)) {
		text := string(line)
		start := 0
		lastStart := len(text) - 1 - maxLineLength
		for start <= lastStart {
			stop := start + maxlinelen1
			switch {
			case text[stop-2] == '=':
				out = append(out, text[start:stop-1])
				start = stop - 2
			case text[stop-1] == '=':
				out = append(out, text[start:stop])
				start = stop - 1
			default:
				out = append(out, text[start:stop]+"=")
				start = stop
			}
		}
		if text != "" && (text[len(text)-1] == ' ' || text[len(text)-1] == '\t') {
			last := text[len(text)-1]
			room := start - lastStart
			var tail string
			switch {
			case room >= 3:
				tail = "=" + upperHex(last)
			case room == 2:
				tail = string(last) + softBreak
			default:
				tail = softBreak + "=" + upperHex(last)
			}
			out = append(out, text[start:len(text)-1]+tail)
		} else {
			out = append(out, text[start:])
		}
	}
	if body[len(body)-1] == '\n' || body[len(body)-1] == '\r' {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// quopriBodyByte is the _QUOPRI_BODY_ENCODE_MAP: everything printable stays,
// tab and space stay (body_encode deals with them at end of line), and the
// rest becomes =XX. A literal '=' is always escaped.
func quopriBodyByte(c byte) string {
	switch {
	case c == '=':
		return "=3D"
	case c == '\t' || c == ' ':
		return string(rune(c))
	case c == '\n' || c == '\r':
		return string(rune(c))
	case c >= '!' && c <= '~':
		return string(rune(c))
	default:
		return "=" + upperHex(c)
	}
}
