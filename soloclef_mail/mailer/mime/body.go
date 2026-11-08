package mime

import (
	"bytes"
	"encoding/base64"
	"strings"
)

// transferEncoded is a text body as email.contentmanager._encode_text leaves
// it: the transfer encoding it picked, and the payload in that encoding.
//
// The order of the heuristic is the order Python wrote it, and the answers it
// gives are not the obvious ones: a body of short ASCII lines is 7bit, the
// same body with an accent in it is *8bit* and goes on the wire as raw UTF-8
// (because email.policy.default has cte_type 8bit and smtplib does not
// downgrade it), and only a body with a line over 78 bytes is quoted-printable
// or base64.
type transferEncoded struct {
	encoding string
	payload  string
}

func encodeText(text string) transferEncoded {
	lines := splitLines([]byte(text))
	if longestLine(lines) <= maxLineLength {
		normal := normalBody(lines)
		if isASCII(normal) {
			return transferEncoded{"7bit", string(normal)}
		}
		// cte_type is 8bit on email.policy.default, so this is where a
		// non-ASCII body stops being encoded at all.
		return transferEncoded{"8bit", string(normal)}
	}

	// The sniff: quoted-printable unless base64 is shorter over the first ten
	// lines. "A little unfair to qp", as the comment there says, because the
	// line separators are counted on one side and not the other.
	sniff := embeddedBody(lines, 10)
	sniffQP := quotedPrintableBody(string(sniff))
	sniffBase64 := base64Line(sniff)
	if len(sniffQP) > len(sniffBase64) {
		return transferEncoded{"base64", encodeBase64Body(embeddedBody(lines, len(lines)))}
	}
	if len(lines) <= 10 {
		return transferEncoded{"quoted-printable", sniffQP}
	}
	return transferEncoded{"quoted-printable", quotedPrintableBody(string(normalBody(lines)))}
}

// splitLines is bytes.splitlines(): it drops the line endings, and it treats
// more than \r, \n and \r\n as one -- which is why a body carrying a form feed
// comes out with it turned into a line break rather than kept.
func splitLines(raw []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e:
			lines = append(lines, raw[start:i])
			start = i + 1
		case '\r':
			lines = append(lines, raw[start:i])
			if i+1 < len(raw) && raw[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(raw) {
		lines = append(lines, raw[start:])
	}
	return lines
}

// normalBody is the body joined with \n and given a trailing one: every line
// ending in a queued message is normalised here, before the generator turns
// each \n into CRLF.
func normalBody(lines [][]byte) []byte {
	return append(bytes.Join(lines, []byte("\n")), '\n')
}

// embeddedBody is the same join with the policy's own separator, which is \n
// on email.policy.default -- not CRLF, even though the message is flattened
// with CRLF.
func embeddedBody(lines [][]byte, upTo int) []byte {
	if upTo > len(lines) {
		upTo = len(lines)
	}
	return append(bytes.Join(lines[:upTo], []byte("\n")), '\n')
}

func longestLine(lines [][]byte) int {
	longest := 0
	for _, line := range lines {
		if len(line) > longest {
			longest = len(line)
		}
	}
	return longest
}

func isASCII(raw []byte) bool {
	for _, b := range raw {
		if b >= 0x80 {
			return false
		}
	}
	return true
}

// base64Line is binascii.b2a_base64: the whole thing on one line with a
// newline after it. Only its length is used, to decide the sniff.
func base64Line(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw) + "\n"
}

// encodeBase64Body is contentmanager._encode_base64 with max_line_length 78:
// 57 bytes per line, which is 76 characters out.
func encodeBase64Body(raw []byte) string {
	const bytesPerLine = maxLineLength / 4 * 3
	var b strings.Builder
	for i := 0; i < len(raw); i += bytesPerLine {
		end := min(i+bytesPerLine, len(raw))
		b.WriteString(base64.StdEncoding.EncodeToString(raw[i:end]))
		b.WriteByte('\n')
	}
	return b.String()
}
