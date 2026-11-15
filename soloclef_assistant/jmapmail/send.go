package jmapmail

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// Outgoing is one message to send, exactly as its owner confirmed it.
type Outgoing struct {
	// IdentityID is the identity it is sent as; From must be that
	// identity's address.
	IdentityID string
	From       Address
	To, Cc     []Address
	// Bcc is delivered to and left out of the copies the others receive:
	// the server removes the header on delivery (checked against Stalwart
	// in this package's end-to-end test).
	Bcc     []Address
	Subject string
	// Body is the plain text, sent as it is: never markup.
	Body string
	// InReplyTo and References make a reply part of its thread.
	InReplyTo, References []string
	// Attachments are the files the message carries, in the order the
	// owner saw them.
	Attachments []OutgoingAttachment
}

// OutgoingAttachment is one file a message carries: either a blob the
// account already holds (an attachment of a message in this mailbox,
// forwarded as it is) or a file streamed to the server's upload endpoint
// just before the message is made.
type OutgoingAttachment struct {
	Name string
	Type string
	Size int64
	// BlobID names a blob already in this account. A blob's bytes never
	// change under its id (RFC 8620 6), so the file sent is the one the
	// owner confirmed without being fetched and sent back again.
	BlobID string
	// Open streams the bytes of a file that is not in the account, for
	// the upload. The caller's stream is where a file's integrity is
	// checked: a read that fails, or a Close that returns an error once
	// the upload is done, fails the send, so this package never names a
	// blob whose bytes the caller did not vouch for.
	Open func(ctx context.Context) (io.ReadCloser, error)
}

// discardTimeout bounds removing the copy a failed send left in Drafts.
const discardTimeout = 10 * time.Second

// setResult is the part of a /set response a send reads.
type setResult struct {
	Created    map[string]json.RawMessage `json:"created"`
	NotCreated map[string]methodError     `json:"notCreated"`
}

// Send sends one message and returns the id of the copy kept in the Sent
// folder. It is the one method of this package that writes, and its only
// caller is the send guard (soloclef_assistant/maildrafts), after the owner
// pressed Send on the card that showed this exact message.
//
// It is two requests. The first reads the folders and checks the identity
// is still the one the draft was written from: a draft's sender is part of
// what was confirmed, and an identity that changed meanwhile is refused,
// not sent as whatever it is now. The second creates the message in Drafts
// and submits it in one request, with the submission moving it to Sent on
// success (RFC 8621 7.5's onSuccessUpdateEmail), so there is no moment in
// which a sent message is left looking like a draft.
//
// A message with files that are not in the account yet uploads them
// between the two (upload.go), each streamed and each checked by the
// server's own count of its bytes, and the whole send then has the
// deadline of a conversation that moves files.
func (c Client) Send(ctx context.Context, session Session, credential Credential, message Outgoing) (string, error) {
	bound := c.bounded
	if len(message.Attachments) > 0 {
		bound = c.boundedForFiles
	}
	ctx, cancel := bound(ctx)
	defer cancel()
	if !session.CanSend {
		return "", &Failure{Message: "This sign-in may not send mail on the server (for an app password, check its permissions in Stalwart)"}
	}
	got, err := c.call(ctx, session, credential, submissionUsing,
		invocation{"Mailbox/get", map[string]any{"accountId": session.AccountID, "ids": nil, "properties": []string{"id", "name", "role"}}, "m"},
		invocation{"Identity/get", map[string]any{"accountId": session.AccountID, "ids": []string{message.IdentityID}}, "i"})
	if err != nil {
		return "", err
	}
	var folders struct {
		List []Mailbox `json:"list"`
	}
	if err := got.decode("m", "Mailbox/get", &folders); err != nil {
		return "", err
	}
	var identities struct {
		List []Identity `json:"list"`
	}
	if err := got.decode("i", "Identity/get", &identities); err != nil {
		return "", err
	}
	if len(identities.List) != 1 || !strings.EqualFold(identities.List[0].Email, message.From.Email) {
		return "", &Failure{Message: "The address this draft is from is no longer one this mailbox may send as; ask for a new draft"}
	}
	drafts, found := ByRole(folders.List, "drafts")
	if !found {
		return "", &Failure{Message: "The mailbox has no Drafts folder to send from"}
	}
	// Moved to Sent on success; a mailbox without a Sent folder keeps the
	// sent copy where it was made, no longer marked a draft.
	onSuccess := map[string]any{"keywords/$draft": nil}
	if sent, hasSent := ByRole(folders.List, "sent"); hasSent {
		onSuccess["mailboxIds/"+drafts.ID] = nil
		onSuccess["mailboxIds/"+sent.ID] = true
	}
	attached, err := c.attach(ctx, session, credential, message.Attachments)
	if err != nil {
		return "", err
	}
	email := map[string]any{
		"mailboxIds": map[string]bool{drafts.ID: true},
		"keywords":   map[string]bool{"$draft": true, "$seen": true},
		"from":       []Address{message.From},
		"subject":    message.Subject,
		"textBody":   []map[string]string{{"partId": "body", "type": "text/plain"}},
		"bodyValues": map[string]map[string]string{"body": {"value": message.Body}},
	}
	for key, addresses := range map[string][]Address{"to": message.To, "cc": message.Cc, "bcc": message.Bcc} {
		if len(addresses) > 0 {
			email[key] = addresses
		}
	}
	if len(message.InReplyTo) > 0 {
		email["inReplyTo"] = message.InReplyTo
	}
	if len(message.References) > 0 {
		email["references"] = message.References
	}
	if len(attached) > 0 {
		// With textBody and attachments given, the server builds the
		// multipart/mixed message itself (RFC 8621 4.6, Email/set).
		email["attachments"] = attached
	}
	got, err = c.call(ctx, session, credential, submissionUsing,
		invocation{"Email/set", map[string]any{"accountId": session.AccountID, "create": map[string]any{"draft": email}}, "e"},
		invocation{"EmailSubmission/set", map[string]any{
			"accountId":            session.AccountID,
			"create":               map[string]any{"send": map[string]string{"identityId": message.IdentityID, "emailId": "#draft"}},
			"onSuccessUpdateEmail": map[string]any{"#send": onSuccess},
		}, "s"})
	if err != nil {
		return "", err
	}
	var made setResult
	if err := got.decode("e", "Email/set", &made); err != nil {
		return "", err
	}
	createdRaw, created := made.Created["draft"]
	if !created {
		refused := made.NotCreated["draft"]
		return "", refusal(refused.Type, refused.Description)
	}
	var createdEmail struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(createdRaw, &createdEmail) != nil || createdEmail.ID == "" {
		return "", errBroken
	}
	var submitted setResult
	if err := got.decode("s", "EmailSubmission/set", &submitted); err != nil {
		c.discardDraft(ctx, session, credential, createdEmail.ID)
		return "", err
	}
	if _, sent := submitted.Created["send"]; !sent {
		refused := submitted.NotCreated["send"]
		// The message was made and not sent: removed again, so the owner's
		// Drafts do not fill with copies of a send that failed.
		c.discardDraft(ctx, session, credential, createdEmail.ID)
		if refused.Type == "forbidden" || refused.Type == "forbiddenToSend" || refused.Type == "forbiddenFrom" {
			return "", &Failure{Message: "The mail server does not allow this sign-in to send this message (for an app password, check its permissions in Stalwart)"}
		}
		return "", refusal(refused.Type, refused.Description)
	}
	return createdEmail.ID, nil
}

// attachedPart is one attachment as Email/set takes it.
type attachedPart struct {
	BlobID      string `json:"blobId"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Disposition string `json:"disposition"`
}

// attach is the message's attachments as Email/set names them, uploading
// each file the account does not hold yet. One that fails to open or to
// upload fails the send before any message is made.
func (c Client) attach(ctx context.Context, session Session, credential Credential, files []OutgoingAttachment) ([]attachedPart, error) {
	parts := make([]attachedPart, 0, len(files))
	for _, file := range files {
		blobID := file.BlobID
		if blobID == "" {
			if file.Open == nil {
				return nil, &Failure{Message: "An attachment has no content to send"}
			}
			if session.MaxSizeUpload > 0 && file.Size > session.MaxSizeUpload {
				return nil, TooLargeToUpload(file.Name, file.Size, session.MaxSizeUpload)
			}
			body, err := file.Open(ctx)
			if err != nil {
				return nil, err
			}
			blobID, err = c.upload(ctx, session, credential, body, file.Size, file.Type)
			// Closed before the blob is used: Close is where the caller says
			// the bytes were not the ones meant.
			if closed := body.Close(); err == nil && closed != nil {
				err = closed
			}
			if err != nil {
				return nil, err
			}
		}
		kind := file.Type
		if kind == "" {
			kind = "application/octet-stream"
		}
		parts = append(parts, attachedPart{BlobID: blobID, Type: kind, Name: file.Name, Disposition: "attachment"})
	}
	return parts, nil
}

// discardDraft removes a message a failed send made. It is best effort:
// the send has already failed and says so; a draft left behind is the
// owner's to delete, and is no worse than one they made themselves.
func (c Client) discardDraft(ctx context.Context, session Session, credential Credential, emailID string) {
	// Its own short deadline: the send's may be what ran out.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discardTimeout)
	defer cancel()
	_, _ = c.call(ctx, session, credential, mailUsing,
		invocation{"Email/set", map[string]any{"accountId": session.AccountID, "destroy": []string{emailID}}, "d"})
}
