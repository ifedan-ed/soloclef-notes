package mime

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
)

// Boundary makes one multipart boundary. The shape is email.utils's
// _make_boundary -- fifteen '=' signs, nineteen digits, two more '=' -- and the
// length is not cosmetic: at 36 characters a multipart/mixed Content-Type is
// exactly 78 and stays on one line, while multipart/alternative is 84 and
// folds. A shorter boundary moves that fold.
type Boundary func() string

// RandomBoundary is _make_boundary: a random number under sys.maxsize, in
// nineteen digits.
func RandomBoundary() string {
	limit := new(big.Int).SetUint64(1 << 63)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		panic("mime: the system has no randomness for a boundary: " + err.Error())
	}
	return strings.Repeat("=", 15) + fmt.Sprintf("%019d", n) + "=="
}

// Build is _build_mime flattened the way smtplib flattens it: CRLF line
// endings and the message's own policy, which leaves an 8bit body 8bit.
//
// sender is what goes in From: the message's own, or the deployment default,
// decided before this is called.
//
// A header value carrying CR or LF is refused rather than encoded, because
// email.policy.default refuses it when the header is set and _build_mime
// raises before anything is sent. That refusal is what stops a subject built
// out of someone's text from adding headers of its own, so it is a refusal
// here too and not an escape.
func Build(m mailer.Message, sender string, boundary Boundary) (string, error) {
	for _, header := range []struct{ name, value string }{
		{"From", sender}, {"To", m.To}, {"Subject", m.Subject}, {"Reply-To", m.ReplyTo},
	} {
		if strings.ContainsAny(header.value, "\r\n") {
			return "", fmt.Errorf("mime: %s may not contain a line ending", header.name)
		}
	}

	var b strings.Builder
	b.WriteString(foldAddress("From", sender))
	b.WriteString(foldAddress("To", m.To))
	b.WriteString(foldUnstructured("Subject", m.Subject))
	if m.ReplyTo != "" {
		b.WriteString(foldAddress("Reply-To", m.ReplyTo))
	}

	text := textPart(m.TextBody)
	switch {
	case len(m.Attachments) == 0 && m.HTMLBody == "":
		// set_content alone: the content headers land on the message itself,
		// and MIME-Version comes last because set_content adds it after the
		// two it has just set.
		b.WriteString(text)
		b.WriteString("MIME-Version: 1.0\r\n")
		b.WriteString("\r\n")
		b.WriteString(bodyOf(m.TextBody))
	case len(m.Attachments) == 0:
		b.WriteString("MIME-Version: 1.0\r\n")
		b.WriteString(alternative(m, boundary))
	default:
		b.WriteString("MIME-Version: 1.0\r\n")
		inner := alternative(m, boundary)
		if m.HTMLBody == "" {
			inner = text + "\r\n" + bodyOf(m.TextBody)
		}
		b.WriteString(mixed(m, inner, boundary))
	}
	return b.String(), nil
}

// alternative writes the multipart/alternative container and its two parts, as
// add_alternative leaves them.
//
// The html part carries a MIME-Version of its own and the text part does not.
// That is add_alternative's doing -- it moves the already-set content into a
// subpart, which is why only the one added afterwards gets the header -- and it
// is in every message this app has sent with an HTML body.
func alternative(m mailer.Message, boundary Boundary) string {
	mark := boundary()
	var b strings.Builder
	b.WriteString(foldContentType("multipart/alternative", mark))
	b.WriteString("\r\n")
	b.WriteString("--" + mark + "\r\n")
	b.WriteString(textPart(m.TextBody))
	b.WriteString("\r\n")
	b.WriteString(bodyOf(m.TextBody))
	b.WriteString("\r\n--" + mark + "\r\n")
	b.WriteString(htmlPart(m.HTMLBody))
	b.WriteString("\r\n")
	b.WriteString(bodyOf(m.HTMLBody))
	b.WriteString("\r\n--" + mark + "--\r\n")
	return b.String()
}

// mixed wraps the body part and the attachments, as add_attachment leaves them.
func mixed(m mailer.Message, inner string, boundary Boundary) string {
	mark := boundary()
	var b strings.Builder
	b.WriteString(foldContentType("multipart/mixed", mark))
	b.WriteString("\r\n")
	b.WriteString("--" + mark + "\r\n")
	b.WriteString(inner)
	for _, a := range m.Attachments {
		b.WriteString("\r\n--" + mark + "\r\n")
		b.WriteString(attachmentPart(a))
	}
	b.WriteString("\r\n--" + mark + "--\r\n")
	return b.String()
}

func textPart(body string) string {
	return "Content-Type: text/plain; charset=\"utf-8\"\r\n" +
		"Content-Transfer-Encoding: " + encodeText(body).encoding + "\r\n"
}

func htmlPart(body string) string {
	return "Content-Type: text/html; charset=\"utf-8\"\r\n" +
		"Content-Transfer-Encoding: " + encodeText(body).encoding + "\r\n" +
		"MIME-Version: 1.0\r\n"
}

// attachmentPart is add_attachment's part: always base64, always a
// Content-Disposition, and MIME-Version after both.
func attachmentPart(a mailer.Attachment) string {
	contentType := a.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return "Content-Type: " + contentType + "\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		foldContentDisposition(a.Filename) +
		"MIME-Version: 1.0\r\n" +
		"\r\n" +
		toCRLF(encodeBase64Body(a.Content))
}

// bodyOf is a part's payload on the wire: every \n a CRLF, since the generator
// is flattening with CRLF line endings.
func bodyOf(text string) string {
	return toCRLF(encodeText(text).payload)
}

func toCRLF(payload string) string {
	return strings.ReplaceAll(payload, "\n", "\r\n")
}
