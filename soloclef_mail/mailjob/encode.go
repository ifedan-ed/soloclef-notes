package mailjob

import (
	"encoding/base64"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_mail/pythonjson"
)

// Encode is MailJob.to_json: the exact JSON the mail senders read, with
// Python's compact separators, its key order and its ensure_ascii escaping.
//
// The key order is the order job.py writes the dict literal in, because
// Python's json keeps insertion order and a producer that reorders the keys
// writes bytes nobody has compared. Every field is written even when it is
// null: Python writes them all.
func (j Job) Encode() (string, error) {
	var b strings.Builder

	b.WriteString(`{"id":`)
	pythonjson.WriteASCIIOnly(&b, j.ID)
	b.WriteString(`,"kind":`)
	pythonjson.WriteASCIIOnly(&b, j.Kind)
	b.WriteString(`,"ref":`)
	writeOptionalString(&b, j.Ref)
	b.WriteString(`,"not_after":`)
	if j.NotAfter == nil {
		b.WriteString("null")
	} else {
		text, err := pythonjson.FormatFloat(*j.NotAfter)
		if err != nil {
			return "", err
		}
		b.WriteString(text)
	}
	b.WriteString(`,"attempt":`)
	pythonjson.WriteInt(&b, j.Attempt)
	b.WriteString(`,"enqueued_at":`)
	enqueued, err := pythonjson.FormatFloat(j.EnqueuedAt)
	if err != nil {
		return "", err
	}
	b.WriteString(enqueued)

	m := j.Message
	b.WriteString(`,"message":{"to":`)
	pythonjson.WriteASCIIOnly(&b, m.To)
	b.WriteString(`,"subject":`)
	pythonjson.WriteASCIIOnly(&b, m.Subject)
	b.WriteString(`,"text_body":`)
	pythonjson.WriteASCIIOnly(&b, m.TextBody)
	b.WriteString(`,"html_body":`)
	writeOptionalString(&b, m.HTMLBody)
	b.WriteString(`,"sender":`)
	writeOptionalString(&b, m.Sender)
	b.WriteString(`,"reply_to":`)
	writeOptionalString(&b, m.ReplyTo)
	b.WriteString(`,"idempotency_key":`)
	writeOptionalString(&b, m.IdempotencyKey)
	b.WriteString(`,"attachments":[`)
	for i, a := range m.Attachments {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"filename":`)
		pythonjson.WriteASCIIOnly(&b, a.Filename)
		b.WriteString(`,"content":`)
		pythonjson.WriteASCIIOnly(&b, base64.StdEncoding.EncodeToString(a.Content))
		b.WriteString(`,"content_type":`)
		pythonjson.WriteASCIIOnly(&b, a.ContentType)
		b.WriteByte('}')
	}
	b.WriteString("]}}")
	return b.String(), nil
}

// writeOptionalString writes Python's None for an unset field. Every optional
// field on the job is a string whose absence Python spells None, so the empty
// string and None are the same value here -- which is what the call sites mean:
// app/mailer/protocol.py's defaults are None and no caller sends "".
func writeOptionalString(b *strings.Builder, s string) {
	if s == "" {
		b.WriteString("null")
		return
	}
	pythonjson.WriteASCIIOnly(b, s)
}
