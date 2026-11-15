package mailattachments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/maildrafts"
)

// forwarded is an attachment of a message in one of the owner's JMAP
// mailboxes. It is downloaded once, now, to take its digest: the draft is
// confirmed for those bytes. When the draft is sent from the same mailbox
// the blob is named by its id and not fetched again (its bytes cannot
// change under it); from another mailbox it is fetched again and checked
// against the digest on the way (maildrafts.JMAPSender).
func (f *Files) forwarded(ctx context.Context, ownerID string, request Request, mostFile int64) (maildrafts.Attachment, error) {
	if request.AccountID == "" || request.MessageID == "" || strings.TrimSpace(request.Filename) == "" {
		return maildrafts.Attachment{}, refused("a message's attachment is named by account_id, message_id and filename, all three, as read_email showed them")
	}
	email, err := f.Mail.Read(ctx, ownerID, request.AccountID, request.MessageID)
	if err != nil {
		return maildrafts.Attachment{}, err
	}
	wanted := strings.TrimSpace(request.Filename)
	for _, part := range email.Attachments {
		if !strings.EqualFold(strings.TrimSpace(part.Name), wanted) {
			continue
		}
		name := fileName(part.Name)
		if err := within(name, int64(part.Size), mostFile); err != nil {
			return maildrafts.Attachment{}, err
		}
		// Read no further than the size the server stated: a blob longer
		// than its own description is not the attachment listed.
		data, err := f.Mail.Download(ctx, ownerID, request.AccountID, part, int64(part.Size))
		if err != nil {
			return maildrafts.Attachment{}, err
		}
		if len(data) == 0 {
			return maildrafts.Attachment{}, refused(fmt.Sprintf("the attachment %q is empty", name))
		}
		digest := sha256.Sum256(data)
		kind := part.Type
		if strings.TrimSpace(kind) == "" {
			kind = "application/octet-stream"
		}
		from := ""
		if len(email.From) > 0 {
			from = " from " + email.From[0].String()
		}
		return maildrafts.Attachment{
			Name: name, Type: kind, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]),
			Origin: "Attachment of \"" + strings.Join(strings.Fields(email.Subject), " ") + "\"" + from,
			Source: maildrafts.AttachmentSource{Kind: maildrafts.SourceMail, AccountID: request.AccountID,
				MessageID: email.ID, BlobID: part.BlobID},
		}, nil
	}
	names := make([]string, 0, len(email.Attachments))
	for _, part := range email.Attachments {
		names = append(names, fmt.Sprintf("%q", part.Name))
	}
	if len(names) == 0 {
		return maildrafts.Attachment{}, refused("that message has no attachments")
	}
	return maildrafts.Attachment{}, refused(fmt.Sprintf("that message has no attachment named %q; its attachments are %s", wanted, strings.Join(names, ", ")))
}

// download is a forwarded attachment's bytes, for a send from another
// mailbox than the one it is in.
func (f *Files) download(ctx context.Context, ownerID string, attachment maildrafts.Attachment) ([]byte, error) {
	return f.Mail.Download(ctx, ownerID, attachment.Source.AccountID,
		jmapmail.Attachment{BlobID: attachment.Source.BlobID, Name: attachment.Name, Type: attachment.Type, Size: int(attachment.Size)},
		attachment.Size)
}
