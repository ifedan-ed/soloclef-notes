package mailtools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailattachments"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/maildrafts"
)

// Attachments finds a file the model asked to attach among the owner's
// own (*mailattachments.Files).
type Attachments interface {
	Resolve(ctx context.Context, ownerID string, request mailattachments.Request, mostFile int64) (maildrafts.Attachment, error)
}

// attachmentArguments is one entry of draft_email's attachments.
type attachmentArguments struct {
	DocumentID int64  `json:"document_id"`
	ArtifactID int64  `json:"artifact_id"`
	Title      string `json:"title"`
	AccountID  string `json:"account_id"`
	MessageID  string `json:"message_id"`
	Filename   string `json:"filename"`
}

// attachmentsParameter is draft_email's attachments argument.
const attachmentsParameter = `"attachments":{"type":"array","description":"Files to attach, only ones the user asked to send. Each names exactly one file: document_id, or artifact_id for a Studio output (slides, a PDF, a Word file...), or title (a document's or Studio output's exact title), or account_id with message_id and filename to forward an attachment of a message read with read_email.","items":{"type":"object","properties":{` +
	`"document_id":{"type":"integer","description":"One of the user's documents: its uploaded file, or the file a Studio output made."},` +
	`"artifact_id":{"type":"integer","description":"A Studio output, by the artifact_id the Studio reported."},` +
	`"title":{"type":"string","description":"A document or Studio output by its exact title, as a search result or the Studio showed it."},` +
	`"account_id":{"type":"string","description":"With message_id and filename: the mailbox the message is in."},` +
	`"message_id":{"type":"string","description":"With account_id and filename: the message, as read_email gave it."},` +
	`"filename":{"type":"string","description":"With account_id and message_id: the attachment's name, as read_email listed it."}}}}`

// attach is the files a draft from box carries, each found, checked
// against the mail server's own limits and digested; or why one cannot be
// attached, in words the model passes on.
func (t *Tools) attach(ctx context.Context, userID string, box mailbox, requested []attachmentArguments) ([]maildrafts.Attachment, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	limits, err := t.JMAP.Limits(ctx, userID, box.account.ID)
	if err != nil {
		return nil, t.failure(ctx, err)
	}
	out := make([]maildrafts.Attachment, 0, len(requested))
	var total int64
	for _, asked := range requested {
		attachment, err := t.Attachments.Resolve(ctx, userID, mailattachments.Request{
			DocumentID: asked.DocumentID, ArtifactID: asked.ArtifactID, Title: asked.Title,
			AccountID: strings.TrimSpace(asked.AccountID), MessageID: strings.TrimSpace(asked.MessageID), Filename: asked.Filename,
		}, limits.MostFileBytes)
		var refused *mailattachments.Refused
		switch {
		case errors.As(err, &refused):
			return nil, errors.New(refused.Message)
		case errors.Is(err, jmapaccounts.ErrNoAccount):
			return nil, fmt.Errorf("no mailbox of the user has the account_id %q; call %s for the ones they have", asked.AccountID, ListAccountsTool)
		case err != nil:
			var said *jmapmail.Failure
			if errors.As(err, &said) || ctx.Err() != nil {
				return nil, t.failure(ctx, err)
			}
			t.Logger.LogAttrs(ctx, slog.LevelError, "a draft's attachment could not be found", slog.String("error", err.Error()), slog.String("user_id", userID))
			return nil, errors.New("the file could not be attached just now; try again shortly")
		}
		total += attachment.Size
		out = append(out, attachment)
	}
	// The server's limit on all of a message's files together, when it
	// states one: past it the server refuses the message, so the draft is
	// not made.
	if limits.MostAttachmentsBytes > 0 && total > limits.MostAttachmentsBytes {
		return nil, fmt.Errorf("the files are %s together; this mail server accepts at most %s of attachments in one message. Attach fewer, or tell the user",
			jmapmail.ReadableSize(total), jmapmail.ReadableSize(limits.MostAttachmentsBytes))
	}
	return out, nil
}

// attachedSummary is the files a draft carries, for the model's result.
func attachedSummary(attachments []maildrafts.Attachment) string {
	if len(attachments) == 0 {
		return ""
	}
	names := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		names = append(names, field(attachment.Name)+" ("+jmapmail.ReadableSize(attachment.Size)+")")
	}
	return ", with " + fmt.Sprint(len(attachments)) + " attachment(s): " + strings.Join(names, ", ")
}
