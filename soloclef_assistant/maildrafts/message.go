package maildrafts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Address is one recipient or sender.
type Address struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Attachment is a file a draft carries. Every field is part of the
// content and of its hash: the name and type it is sent under, its exact
// size and SHA-256, and where its bytes are read from at send time. So a
// draft confirmed with one file is never sent with another, and the card
// shows each file the message will carry.
type Attachment struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
	// SHA256 is the hex digest of the file's bytes as the draft was made:
	// the bytes read at send time must have it (attachments.go).
	SHA256 string `json:"sha256"`
	// Origin says, for the card, where the file came from ("Studio
	// output: Quarterly slides", "Attachment of: Invoice for October").
	Origin string           `json:"origin"`
	Source AttachmentSource `json:"source"`
}

// The kinds of place a draft's file is read from.
const (
	// SourceDocument is a document's original file (document_files).
	SourceDocument = "document"
	// SourceArtifact is a Studio output's file (artifact_files).
	SourceArtifact = "artifact"
	// SourceMail is an attachment of a message in one of the owner's JMAP
	// mailboxes, forwarded.
	SourceMail = "mail"
)

// AttachmentSource is where a draft's file is read from when it is sent.
// It names rows of the owner's own; whoever reads it reads them as the
// owner, so a source naming another account's file finds nothing.
type AttachmentSource struct {
	Kind string `json:"kind"`
	// FileID is the document_files or artifact_files row (SourceDocument,
	// SourceArtifact).
	FileID int64 `json:"file_id,omitempty"`
	// AccountID, MessageID and BlobID are the mailbox, the message and the
	// attachment's blob (SourceMail).
	AccountID string `json:"account_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	BlobID    string `json:"blob_id,omitempty"`
}

// AttachmentBytes is the size of all the message's files together.
func (m Message) AttachmentBytes() int64 {
	var total int64
	for _, attachment := range m.Attachments {
		total += attachment.Size
	}
	return total
}

// Message is a draft's whole content: what the card shows, what is hashed
// and what is sent. Its fields are in a fixed order and its slices are
// never nil (canonical), so the same content always encodes, and hashes,
// the same.
type Message struct {
	AccountID string `json:"account_id"`
	// IdentityID is the mailbox's identity the message is sent as; From is
	// that identity's address.
	IdentityID string    `json:"identity_id"`
	From       Address   `json:"from"`
	To         []Address `json:"to"`
	Cc         []Address `json:"cc"`
	Bcc        []Address `json:"bcc"`
	Subject    string    `json:"subject"`
	Body       string    `json:"body"`
	// The reply's headers, from the message replied to.
	InReplyTo  []string `json:"in_reply_to"`
	References []string `json:"references"`
	// ReplyToMessageID, ReplyToSubject and ReplyToFrom say which message
	// this answers, for the card.
	ReplyToMessageID string `json:"reply_to_message_id"`
	ReplyToSubject   string `json:"reply_to_subject"`
	ReplyToFrom      string `json:"reply_to_from"`
	// ThreadParticipants is every address on the message replied to, lower
	// case: a recipient not among them is flagged on the card, which is
	// where a reply that adds an address an email asked for shows.
	ThreadParticipants []string     `json:"thread_participants"`
	Attachments        []Attachment `json:"attachments"`
}

// canonical is the message with every slice made non-nil, so that an empty
// list encodes one way.
func (m Message) canonical() Message {
	m.To, m.Cc, m.Bcc = nonNil(m.To), nonNil(m.Cc), nonNil(m.Bcc)
	m.InReplyTo, m.References, m.ThreadParticipants = nonNil(m.InReplyTo), nonNil(m.References), nonNil(m.ThreadParticipants)
	m.Attachments = nonNil(m.Attachments)
	return m
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// encoded is the canonical JSON the row stores.
func (m Message) encoded() ([]byte, error) { return json.Marshal(m.canonical()) }

// ContentHash is the SHA-256 of the canonical content: what a go-ahead is
// bound to, so a change to any field, a recipient, a character of the body
// or an attachment, is a different hash.
func (m Message) ContentHash() string {
	encoded, err := m.encoded()
	if err != nil {
		// A Message holds only strings, numbers and lists of them, which
		// always encode; a failure here is a programming error, and a hash
		// that matches nothing refuses the send.
		return "unhashable"
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

// decodeMessage reads a stored message back. The row's jsonb reorders keys,
// so a stored message is decoded and encoded again before it is hashed,
// never hashed as the database returns it.
func decodeMessage(raw []byte) (Message, error) {
	var message Message
	if err := json.Unmarshal(raw, &message); err != nil {
		return Message{}, err
	}
	return message.canonical(), nil
}

// Recipients is every recipient, To, Cc and Bcc.
func (m Message) Recipients() []Address {
	return append(append(append([]Address{}, m.To...), m.Cc...), m.Bcc...)
}

// OutsideThread is the recipients not among the participants of the
// message replied to; none for a new message.
func (m Message) OutsideThread() []string {
	if len(m.ThreadParticipants) == 0 {
		return []string{}
	}
	known := map[string]bool{}
	for _, participant := range m.ThreadParticipants {
		known[strings.ToLower(participant)] = true
	}
	out := []string{}
	for _, recipient := range m.Recipients() {
		if email := strings.ToLower(recipient.Email); !known[email] {
			out = append(out, recipient.Email)
		}
	}
	return out
}

// ParseAddress reads one recipient as the model wrote it, `Ada
// <ada@example.org>` or `ada@example.org`, into a checked Address.
func ParseAddress(text string) (Address, error) {
	parsed, err := mail.ParseAddress(strings.TrimSpace(text))
	if err != nil {
		return Address{}, fmt.Errorf("%q is not an email address", text)
	}
	address := Address{Name: strings.TrimSpace(parsed.Name), Email: parsed.Address}
	return address, address.check()
}

// check refuses an address that cannot be one recipient: control
// characters (a header injection), a name too long to show, an address
// over the protocol's length.
func (a Address) check() error {
	if len(a.Email) > 254 || strings.Count(a.Email, "@") != 1 || hasControl(a.Email) || strings.ContainsAny(a.Email, " ,;<>") {
		return fmt.Errorf("%q is not one email address", a.Email)
	}
	if utf8.RuneCountInString(a.Name) > 200 || hasControl(a.Name) {
		return fmt.Errorf("the name of %q is not one a message can carry", a.Email)
	}
	return nil
}

func hasControl(text string) bool {
	return strings.IndexFunc(text, unicode.IsControl) >= 0
}

// validate refuses a message the guard will not hold, in words the model
// can act on.
func (m Message) validate() error {
	recipients := m.Recipients()
	switch {
	case m.AccountID == "" || m.IdentityID == "" || m.From.Email == "":
		return fmt.Errorf("%w: it has no mailbox to be sent from", errDraftInvalid)
	case len(m.To) == 0:
		return fmt.Errorf("%w: it has no recipient in To", errDraftInvalid)
	case strings.TrimSpace(m.Subject) == "":
		return fmt.Errorf("%w: it has no subject", errDraftInvalid)
	case hasControl(m.Subject) || utf8.RuneCountInString(m.Subject) > mostSubjectChars:
		return fmt.Errorf("%w: the subject must be one line of at most %d characters", errDraftInvalid, mostSubjectChars)
	case strings.TrimSpace(m.Body) == "":
		return fmt.Errorf("%w: it has no text", errDraftInvalid)
	case strings.ContainsRune(m.Body, 0) || utf8.RuneCountInString(m.Body) > mostBodyChars:
		return fmt.Errorf("%w: the text must be at most %d characters", errDraftInvalid, mostBodyChars)
	}
	seen := map[string]bool{}
	for _, recipient := range recipients {
		if err := recipient.check(); err != nil {
			return fmt.Errorf("%w: %v", errDraftInvalid, err)
		}
		email := strings.ToLower(recipient.Email)
		if seen[email] {
			return fmt.Errorf("%w: %s is named twice", errDraftInvalid, recipient.Email)
		}
		seen[email] = true
	}
	for _, attachment := range m.Attachments {
		if err := attachment.check(); err != nil {
			return fmt.Errorf("%w: %v", errDraftInvalid, err)
		}
	}
	return m.From.check()
}

// mostAttachmentNameChars bounds a file's name as a message carries it.
const mostAttachmentNameChars = 255

// check refuses an attachment the message cannot carry as it is
// described: a name a header cannot hold, no type, no bytes, a digest that
// is not one, or a source that names no file.
func (a Attachment) check() error {
	name := strings.TrimSpace(a.Name)
	switch {
	case name == "" || hasControl(a.Name) || strings.ContainsAny(a.Name, `/\`) || utf8.RuneCountInString(a.Name) > mostAttachmentNameChars:
		return fmt.Errorf("the file name %q cannot be sent as an attachment's name", a.Name)
	case strings.TrimSpace(a.Type) == "" || hasControl(a.Type):
		return fmt.Errorf("the file %q has no media type", a.Name)
	case a.Size <= 0:
		return fmt.Errorf("the file %q is empty", a.Name)
	case !isDigest(a.SHA256):
		return fmt.Errorf("the file %q has no digest", a.Name)
	}
	source := a.Source
	switch source.Kind {
	case SourceDocument, SourceArtifact:
		if source.FileID <= 0 {
			return fmt.Errorf("the file %q names no stored file", a.Name)
		}
	case SourceMail:
		if source.AccountID == "" || source.MessageID == "" || source.BlobID == "" {
			return fmt.Errorf("the file %q names no message attachment", a.Name)
		}
	default:
		return fmt.Errorf("the file %q comes from nowhere a file is read from", a.Name)
	}
	return nil
}

// isDigest reports a lower-case hex SHA-256.
func isDigest(text string) bool {
	if len(text) != sha256.Size*2 {
		return false
	}
	for _, r := range text {
		if !('0' <= r && r <= '9' || 'a' <= r && r <= 'f') {
			return false
		}
	}
	return true
}
