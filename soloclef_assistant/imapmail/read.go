package imapmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"golang.org/x/net/html/charset"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
)

// The bounds on one message. A message larger than mostMessageBytes is
// read as far as that and said to be partial: the text of a message comes
// before its attachments, so what is cut is the tail of the attachments.
const (
	mostMessageBytes = 4 << 20
	// mostTextBytes bounds one text part kept in memory: the body, or a
	// text attachment.
	mostTextBytes = 512 << 10
	mostParts     = 200
	mostDepth     = 8

	// MostAttachmentBytes is the most of one attachment kept whole to be
	// read: the file reader's own limit, so an attachment is never fetched
	// whole only to be refused by the reader for its size.
	MostAttachmentBytes = fileread.MaxFileBytes
	// mostWholeMessageBytes is how much of a message is fetched when one of
	// its attachments is wanted whole: room for that attachment at its
	// limit as base64 (four bytes for three, and a line break every 76),
	// and two megabytes for the text and headers around it.
	mostWholeMessageBytes = MostAttachmentBytes/3*4*78/76 + 2<<20
)

// Message is one message, read.
type Message struct {
	From, To, Cc, Subject string
	// Date is the Date header as the sender wrote it; the zero time when
	// it is absent or unreadable.
	Date time.Time
	// Body is the message's text: its plain-text part, or its HTML part
	// as Markdown when it has no plain one.
	Body        string
	Attachments []Attachment
	// Partial says the message is larger than what was read, so the list
	// of attachments may be short.
	Partial bool
}

// Attachment is one attached file.
type Attachment struct {
	Filename    string
	ContentType string
	// Bytes is the attachment's decoded size, of what was read.
	Bytes int
	// Text is the attachment's content when it is text (text/*, JSON,
	// XML), cut at mostTextBytes; "" for any other kind.
	Text string
	// Data is the attachment's decoded bytes, for the one attachment a
	// ReadAttachment asked for and only that one; nil otherwise. It holds
	// at most MostAttachmentBytes+1 bytes: one more than the limit says the
	// attachment is over it.
	Data []byte
}

// Read reads one message of a folder by the UID a search gave. It does
// not mark the message as read.
func (i IMAP) Read(ctx context.Context, settings Settings, folder string, uid uint32) (Message, error) {
	return i.read(ctx, settings, folder, uid, "", i.Timeout, mostMessageBytes)
}

// ReadAttachment reads one message as Read does, and keeps the bytes of
// its attachment named filename (case aside) whole, in that attachment's
// Data, so that it can be read as the file it is.
//
// The message is fetched further than Read fetches it (an attachment at
// its limit is about 34 MB as base64), and the conversation is given
// AttachmentTimeout instead of Timeout, since the bytes take longer to
// come than a message's text does. Only the named attachment is held:
// every other part is counted as it passes and dropped.
func (i IMAP) ReadAttachment(ctx context.Context, settings Settings, folder string, uid uint32, filename string) (Message, error) {
	return i.read(ctx, settings, folder, uid, filename, i.AttachmentTimeout, mostWholeMessageBytes)
}

// read is one message, fetched as far as most bytes, in a conversation of
// at most timeout, with the attachment named keep held whole ("" for none).
func (i IMAP) read(ctx context.Context, settings Settings, folder string, uid uint32, keep string, timeout time.Duration, most int) (Message, error) {
	talk, end, err := i.signedInFor(ctx, settings, timeout)
	if err != nil {
		return Message{}, err
	}
	defer end()
	if err := examine(talk, folder); err != nil {
		return Message{}, err
	}
	var raw []byte
	size := 0
	ok, reason := talk.command(most, func(response reply) {
		if !strings.Contains(response.text, " FETCH (") || len(response.literals) == 0 {
			return
		}
		raw = response.literals[0]
		if matched := fetchSize.FindStringSubmatch(response.text); matched != nil {
			size, _ = strconv.Atoi(matched[1])
		}
	}, "UID FETCH "+strconv.FormatUint(uint64(uid), 10)+" (UID RFC822.SIZE BODY.PEEK[]<0."+strconv.Itoa(most)+">)")
	switch {
	case !ok && reason == "":
		return Message{}, errBroken
	case !ok:
		return Message{}, &ConnectionError{Message: "The mail server would not hand over the message: " + reason}
	case raw == nil:
		return Message{}, &ConnectionError{Message: "That message is not in the folder; it may have been moved or deleted"}
	}
	message := parse(raw, keep)
	message.Partial = size > len(raw)
	return message, nil
}

// parse reads a message's bytes, holding the attachment named keep whole
// ("" for none). It keeps whatever can be read: a message cut short, or
// with a part that is malformed, still has headers and usually a body.
func parse(raw []byte, keep string) Message {
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return Message{Body: ""}
	}
	headers := decodedHeader{header: parsed.Header}
	message := Message{From: headers.Get("From"), To: headers.Get("To"), Cc: headers.Get("Cc"), Subject: headers.Get("Subject")}
	message.Date, _ = parsed.Header.Date()
	found := parts{keep: keep}
	found.walk(textproto.MIMEHeader(parsed.Header), parsed.Body, 0)
	message.Attachments = found.attachments
	switch {
	case found.plain != "":
		message.Body = found.plain
	case found.html != "":
		// Markdown keeps a message's links and lists, which is what the
		// reader of an HTML-only message needs of it.
		if converted, err := htmltomarkdown.ConvertString(found.html); err == nil {
			message.Body = strings.TrimSpace(converted)
		}
	}
	return message
}

// parts is what walking a message's MIME tree collects.
type parts struct {
	plain, html string
	attachments []Attachment
	seen        int
	// keep is the attachment whose bytes are held whole, and kept says it
	// has been: only the first of two attachments of one name is held.
	keep string
	kept bool
}

// walk reads one MIME entity: a multipart's children in turn, or a leaf.
func (p *parts) walk(header textproto.MIMEHeader, body io.Reader, depth int) {
	if p.seen++; p.seen > mostParts || depth > mostDepth {
		return
	}
	kind, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		// No type, or an unreadable one, is plain text (RFC 2045, 5.2).
		kind, params = "text/plain", map[string]string{}
	}
	if strings.HasPrefix(kind, "multipart/") && params["boundary"] != "" {
		reader := multipart.NewReader(body, params["boundary"])
		for {
			// The raw part: its transfer encoding is undone below, for
			// every encoding and not only the one NextPart knows.
			part, err := reader.NextRawPart()
			if err != nil {
				return
			}
			p.walk(part.Header, part, depth+1)
		}
	}
	switch strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))) {
	case "base64":
		body = base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		body = quotedprintable.NewReader(body)
	}
	disposition, dispositionParams, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	filename := dispositionParams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	if decoded, err := words.DecodeHeader(filename); err == nil {
		filename = decoded
	}
	textual := strings.HasPrefix(kind, "text/") || kind == "application/json" || kind == "application/xml"
	attached := disposition == "attachment" || (filename != "" && kind != "text/plain" && kind != "text/html")
	wanted := attached && !p.kept && p.keep != "" && strings.EqualFold(filename, p.keep)
	// Kept in memory only as far as a text part is useful, or, for the
	// attachment asked for, as far as it can be read; the rest is counted
	// and dropped.
	limit := int64(mostTextBytes)
	if wanted {
		limit = MostAttachmentBytes + 1
	}
	held, _ := io.ReadAll(io.LimitReader(body, limit))
	rest, _ := io.Copy(io.Discard, body)
	text := ""
	if textual {
		shown := held[:min(len(held), mostTextBytes)]
		text = string(shown)
		if reader, err := charset.NewReaderLabel(params["charset"], bytes.NewReader(shown)); err == nil && params["charset"] != "" {
			if converted, err := io.ReadAll(reader); err == nil {
				text = string(converted)
			}
		}
		text = strings.ToValidUTF8(text, "�")
	}
	switch {
	case attached:
		if filename == "" {
			filename = "unnamed"
		}
		attachment := Attachment{Filename: filename, ContentType: kind, Bytes: len(held) + int(rest), Text: text}
		if wanted {
			attachment.Data, p.kept = held, true
		}
		p.attachments = append(p.attachments, attachment)
	case kind == "text/plain" && p.plain == "":
		p.plain = strings.TrimSpace(text)
	case kind == "text/html" && p.html == "":
		p.html = text
	}
}
