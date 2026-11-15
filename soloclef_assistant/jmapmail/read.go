package jmapmail

import (
	"context"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

// maxBodyBytes is how much of each text part of a message the server is
// asked for. A message is read to be answered from, not archived; the
// server says when it cut one (isTruncated), and the message says so.
const maxBodyBytes = 256 << 10

// threadBodyBytes is the same for each message of a thread, which is many
// messages in one answer.
const threadBodyBytes = 32 << 10

// Email is one message as it is read.
type Email struct {
	ID, ThreadID string
	// MessageID, InReplyTo and References are the message's own header
	// ids, which a reply to it carries.
	MessageID, InReplyTo, References []string
	From, To, Cc, ReplyTo            []Address
	// Bcc is set only on the sender's own copy (a sent message); the
	// server removes it from every delivered one.
	Bcc     []Address
	Subject string
	// SentAt is the Date header; ReceivedAt when the server took it.
	SentAt, ReceivedAt time.Time
	// Body is the message's text: its plain-text parts, or its HTML as
	// Markdown when it has no plain one.
	Body string
	// Truncated says the server sent less than the whole text.
	Truncated   bool
	Attachments []Attachment
	Unread      bool
}

// Attachment is one attached file, by the blob that holds it.
type Attachment struct {
	BlobID string `json:"blobId"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int    `json:"size"`
}

// bodyPart is the part of RFC 8621's EmailBodyPart that is read.
type bodyPart struct {
	PartID string `json:"partId"`
	BlobID string `json:"blobId"`
	Type   string `json:"type"`
	Name   string `json:"name"`
	Size   int    `json:"size"`
}

type bodyValue struct {
	Value       string `json:"value"`
	IsTruncated bool   `json:"isTruncated"`
}

// fullEmail is the Email object as a read fetches it.
type fullEmail struct {
	listedEmail
	MessageID  []string             `json:"messageId"`
	InReplyTo  []string             `json:"inReplyTo"`
	References []string             `json:"references"`
	Cc         []Address            `json:"cc"`
	Bcc        []Address            `json:"bcc"`
	ReplyTo    []Address            `json:"replyTo"`
	SentAt     *time.Time           `json:"sentAt"`
	TextBody   []bodyPart           `json:"textBody"`
	Attached   []bodyPart           `json:"attachments"`
	BodyValues map[string]bodyValue `json:"bodyValues"`
}

// readProperties is what a read fetches of a message.
var readProperties = []string{
	"id", "threadId", "messageId", "inReplyTo", "references", "from", "to", "cc", "bcc", "replyTo",
	"subject", "sentAt", "receivedAt", "keywords", "preview", "textBody", "attachments", "bodyValues",
}

func (e fullEmail) email() Email {
	out := Email{
		ID: e.ID, ThreadID: e.ThreadID, MessageID: e.MessageID, InReplyTo: e.InReplyTo, References: e.References,
		From: e.From, To: e.To, Cc: e.Cc, Bcc: e.Bcc, ReplyTo: e.ReplyTo, Subject: e.Subject, ReceivedAt: e.ReceivedAt,
		Unread: !e.Keywords["$seen"],
	}
	if e.SentAt != nil {
		out.SentAt = *e.SentAt
	}
	// textBody is RFC 8621's "what to show as text": the plain parts, or
	// the HTML ones where a message has no plain alternative.
	texts := make([]string, 0, len(e.TextBody))
	for _, part := range e.TextBody {
		value, fetched := e.BodyValues[part.PartID]
		if !fetched {
			continue
		}
		out.Truncated = out.Truncated || value.IsTruncated
		text := value.Value
		if strings.EqualFold(part.Type, "text/html") {
			// Markdown keeps a message's links and lists, which is what a
			// reader of it needs; the markup itself is noise.
			if converted, err := htmltomarkdown.ConvertString(text); err == nil {
				text = converted
			}
		}
		texts = append(texts, strings.TrimSpace(text))
	}
	out.Body = strings.Join(texts, "\n\n")
	for _, part := range e.Attached {
		out.Attachments = append(out.Attachments, Attachment{BlobID: part.BlobID, Name: part.Name, Type: part.Type, Size: part.Size})
	}
	return out
}

// emailGet is the arguments of an Email/get that reads whole messages.
func emailGet(accountID string, ids any, perPart int) map[string]any {
	arguments := map[string]any{
		"accountId":           accountID,
		"properties":          readProperties,
		"fetchTextBodyValues": true,
		"maxBodyValueBytes":   perPart,
	}
	if reference, isReference := ids.(backReference); isReference {
		arguments["#ids"] = reference
	} else {
		arguments["ids"] = ids
	}
	return arguments
}

// Read is one message, by its id. It does not mark the message as read.
func (c Client) Read(ctx context.Context, session Session, credential Credential, emailID string) (Email, error) {
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	got, err := c.call(ctx, session, credential, mailUsing,
		invocation{"Email/get", emailGet(session.AccountID, []string{emailID}, maxBodyBytes), "r"})
	if err != nil {
		return Email{}, err
	}
	var fetched struct {
		List []fullEmail `json:"list"`
	}
	if err := got.decode("r", "Email/get", &fetched); err != nil {
		return Email{}, err
	}
	if len(fetched.List) != 1 || fetched.List[0].ID != emailID {
		return Email{}, errNotFound
	}
	return fetched.List[0].email(), nil
}
