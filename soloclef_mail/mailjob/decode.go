package mailjob

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// wireJob is the queue entry as it is read back. Pointers say "absent" apart
// from "null", because MailJob.from_json indexes id, kind, enqueued_at and
// message directly -- a missing one is a KeyError there, and an entry the
// sender cannot read is dropped rather than sent half-formed.
type wireJob struct {
	ID         *string      `json:"id"`
	Kind       *string      `json:"kind"`
	Ref        *string      `json:"ref"`
	NotAfter   *float64     `json:"not_after"`
	Attempt    *int         `json:"attempt"`
	EnqueuedAt *float64     `json:"enqueued_at"`
	Message    *wireMessage `json:"message"`
}

type wireMessage struct {
	To             *string          `json:"to"`
	Subject        *string          `json:"subject"`
	TextBody       *string          `json:"text_body"`
	HTMLBody       *string          `json:"html_body"`
	Sender         *string          `json:"sender"`
	ReplyTo        *string          `json:"reply_to"`
	IdempotencyKey *string          `json:"idempotency_key"`
	Attachments    []wireAttachment `json:"attachments"`
}

type wireAttachment struct {
	Filename    *string `json:"filename"`
	Content     *string `json:"content"`
	ContentType *string `json:"content_type"`
}

// Decode reads one queue entry, refusing what MailJob.from_json would have
// raised on. Python's base64 decoder is lenient about padding and ignores
// characters outside the alphabet; this is not, because an entry Python would
// have decoded to different bytes than Go is an entry that must not be sent at
// all -- a strict refusal is dropped and logged, where a lenient one is a
// wrong attachment delivered.
func Decode(raw string) (Job, error) {
	var wire wireJob
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return Job{}, fmt.Errorf("mailjob: unreadable queue entry: %w", err)
	}
	if wire.ID == nil || wire.Kind == nil || wire.EnqueuedAt == nil || wire.Message == nil {
		return Job{}, fmt.Errorf("mailjob: queue entry is missing id, kind, enqueued_at or message")
	}
	m := wire.Message
	if m.To == nil || m.Subject == nil || m.TextBody == nil {
		return Job{}, fmt.Errorf("mailjob: queued message is missing to, subject or text_body")
	}

	attachments := make([]Attachment, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		if a.Filename == nil || a.Content == nil || a.ContentType == nil {
			return Job{}, fmt.Errorf("mailjob: queued attachment is missing a field")
		}
		content, err := base64.StdEncoding.DecodeString(*a.Content)
		if err != nil {
			return Job{}, fmt.Errorf("mailjob: queued attachment %q is not base64: %w", *a.Filename, err)
		}
		attachments = append(attachments, Attachment{
			Filename:    *a.Filename,
			Content:     content,
			ContentType: *a.ContentType,
		})
	}
	if len(attachments) == 0 {
		attachments = nil
	}

	job := Job{
		Message: Message{
			To:             *m.To,
			Subject:        *m.Subject,
			TextBody:       *m.TextBody,
			HTMLBody:       optional(m.HTMLBody),
			Sender:         optional(m.Sender),
			ReplyTo:        optional(m.ReplyTo),
			IdempotencyKey: optional(m.IdempotencyKey),
			Attachments:    attachments,
		},
		ID:         *wire.ID,
		Kind:       *wire.Kind,
		Ref:        optional(wire.Ref),
		NotAfter:   wire.NotAfter,
		EnqueuedAt: *wire.EnqueuedAt,
	}
	if wire.Attempt != nil {
		job.Attempt = *wire.Attempt
	}
	return job, nil
}

func optional(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
