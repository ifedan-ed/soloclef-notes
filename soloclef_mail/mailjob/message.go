// Package mailjob is one queued email as app/mail_queue/job.py writes it.
//
// The JSON is the contract between every producer and the mail senders, so it
// is spelled here once and compared, byte for byte, against what Python's
// MailJob.to_json produces for the same input (job_bytes_test.go).
package mailjob

// Attachment is one file on a message; the transport does the base64 for the
// wire, this carries the bytes.
type Attachment struct {
	Filename string
	Content  []byte
	// app/mailer/protocol.py defaults this to application/octet-stream; empty
	// here is written as the empty string Python would have written, because a
	// producer that leaves it off is a producer bug, not a default to guess.
	ContentType string
}

// Message mirrors app/mailer/protocol.py's OutboundEmail. To is one address:
// the Python dataclass types it str and every caller passes one.
type Message struct {
	To       string
	Subject  string
	TextBody string
	// Sender empty means the deployment's SMTP_FROM, decided by the sender
	// process rather than here, exactly as Python's None does.
	Sender      string
	ReplyTo     string
	HTMLBody    string
	Attachments []Attachment
	// Carried and ignored: no transport reads it. See AGENTS.md, "What does
	// not dedupe".
	IdempotencyKey string
}

// Job is MailJob: the message plus what the senders need to know about it.
type Job struct {
	Message Message
	ID      string
	Kind    string
	// Ref is a record id, never an address. Empty is Python's None.
	Ref string
	// NotAfter is the unix time the link or code in the message stops working;
	// nil is Python's None, meaning the message never goes stale.
	NotAfter   *float64
	Attempt    int
	EnqueuedAt float64
}

// Expired reports whether the link or code in the message has stopped working,
// as MailJob.expired does: at not_after, not after it.
func (j Job) Expired(now float64) bool {
	return j.NotAfter != nil && now >= *j.NotAfter
}

// NextAttempt is the same job counted one try further on.
func (j Job) NextAttempt() Job {
	j.Attempt++
	return j
}
