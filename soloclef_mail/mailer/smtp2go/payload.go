// Package smtp2go sends through smtp2go's HTTP API
// (MAIL_TRANSPORT=smtp2go_api): one HTTPS request per message over a
// kept-alive client, with no SMTP conversation, STARTTLS or login per send.
//
// What the provider does and this does not: smtp2go builds the MIME itself
// from these fields, so the subject and the sender's display name go up as
// plain UTF-8 JSON and the RFC 2047 encoding of them is smtp2go's job. That is
// not a choice made here -- it is what app/mailer/smtp2go_api.py sends, and
// pre-encoding a header this way would reach the recipient as the literal
// text "=?utf-8?q?...?=" if the provider encoded it again.
package smtp2go

import (
	"encoding/base64"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
	"git.danvics.com/danvics/soloclef/soloclef_mail/pythonjson"
)

// Path is the one endpoint this transport calls, under BaseURL.
const Path = "/email/send"

// BaseURL is smtp2go's v3 API.
const BaseURL = "https://api.smtp2go.com/v3"

// requestBody is _payload() as httpx encodes it: compact separators, raw
// UTF-8, and the fields in the order the Python dict is built, since that is
// the order json.dumps writes them in.
//
// The optional fields are written only when they are set, exactly as the
// Python function adds them: an html_body of "" is no html_body at all there,
// and sending the key with an empty value is a different request.
func requestBody(m mailer.Message, sender string) string {
	var b strings.Builder
	b.WriteString(`{"sender":`)
	pythonjson.WriteUTF8(&b, sender)
	b.WriteString(`,"to":[`)
	pythonjson.WriteUTF8(&b, m.To)
	b.WriteString(`],"subject":`)
	pythonjson.WriteUTF8(&b, m.Subject)
	b.WriteString(`,"text_body":`)
	pythonjson.WriteUTF8(&b, m.TextBody)
	if m.HTMLBody != "" {
		b.WriteString(`,"html_body":`)
		pythonjson.WriteUTF8(&b, m.HTMLBody)
	}
	if m.ReplyTo != "" {
		// Reply-To is a custom header rather than a field: the API has no
		// reply_to of its own.
		b.WriteString(`,"custom_headers":[{"header":"Reply-To","value":`)
		pythonjson.WriteUTF8(&b, m.ReplyTo)
		b.WriteString(`}]`)
	}
	if len(m.Attachments) > 0 {
		b.WriteString(`,"attachments":[`)
		for i, a := range m.Attachments {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"filename":`)
			pythonjson.WriteUTF8(&b, a.Filename)
			b.WriteString(`,"fileblob":`)
			pythonjson.WriteUTF8(&b, base64.StdEncoding.EncodeToString(a.Content))
			b.WriteString(`,"mimetype":`)
			pythonjson.WriteUTF8(&b, a.ContentType)
			b.WriteByte('}')
		}
		b.WriteByte(']')
	}
	b.WriteByte('}')
	return b.String()
}
