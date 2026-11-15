package maildrafts

import (
	"context"
	"errors"
	"io"
	"slices"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
)

// JMAPSender sends a confirmed draft from a JMAP mailbox, names the mailbox
// for the card, and says whom the mailbox has written to before.
type JMAPSender struct {
	Mail *jmapaccounts.Mail
	// Files reads the draft's files that are not already in the mailbox.
	Files AttachmentFiles
}

func addresses(list []Address) []jmapmail.Address {
	out := make([]jmapmail.Address, 0, len(list))
	for _, address := range list {
		out = append(out, jmapmail.Address{Name: address.Name, Email: address.Email})
	}
	return out
}

// Send builds the outgoing message from the stored draft alone: nothing
// reaches the server that is not in the content the owner confirmed. Each
// file is either a blob this mailbox already holds (an attachment of one
// of its messages, forwarded by its id, whose bytes cannot change) or read
// from its source through a stream that fails unless it is exactly the
// confirmed bytes (verified), so a file changed since the draft was made
// fails the send before any message exists.
func (s JMAPSender) Send(ctx context.Context, ownerID string, draft Draft) (string, error) {
	message := draft.Message
	var streams []*verifiedFile
	files := make([]jmapmail.OutgoingAttachment, 0, len(message.Attachments))
	for _, attachment := range message.Attachments {
		file := jmapmail.OutgoingAttachment{Name: attachment.Name, Type: attachment.Type, Size: attachment.Size}
		if attachment.Source.Kind == SourceMail && attachment.Source.AccountID == draft.AccountID {
			file.BlobID = attachment.Source.BlobID
		} else {
			file.Open = func(ctx context.Context) (io.ReadCloser, error) {
				source, err := s.Files.Open(ctx, ownerID, attachment)
				if err != nil {
					return nil, &jmapmail.Failure{Message: "The file " + attachment.Name + " could not be read to be sent: it may have been removed. Ask for a new draft."}
				}
				stream := verified(source, attachment)
				streams = append(streams, stream)
				return stream, nil
			}
		}
		files = append(files, file)
	}
	id, err := s.Mail.Send(ctx, ownerID, draft.AccountID, jmapmail.Outgoing{
		IdentityID: message.IdentityID,
		From:       jmapmail.Address{Name: message.From.Name, Email: message.From.Email},
		To:         addresses(message.To), Cc: addresses(message.Cc), Bcc: addresses(message.Bcc),
		Subject: message.Subject, Body: message.Body,
		InReplyTo: message.InReplyTo, References: message.References,
		Attachments: files,
	})
	if index := slices.IndexFunc(streams, (*verifiedFile).wasChanged); index >= 0 {
		// Whatever else the server said, this is the reason: the upload
		// was stopped because the bytes were not the confirmed ones.
		return "", &SendFailure{Message: "The file " + streams[index].want.Name +
			" changed after this draft was made, so nothing was sent. Ask for a new draft to send the file as it is now."}
	}
	var failure *jmapmail.Failure
	if errors.As(err, &failure) {
		return "", &SendFailure{Message: failure.Message}
	}
	return id, err
}

// Describe names one of the owner's JMAP mailboxes.
func (s JMAPSender) Describe(ctx context.Context, ownerID, provider, accountID string) (AccountView, error) {
	if provider != ProviderJMAP {
		return AccountView{}, errors.New("maildrafts: not a JMAP draft")
	}
	accounts, err := s.Mail.Accounts(ctx, ownerID)
	if err != nil {
		return AccountView{}, err
	}
	for _, account := range accounts {
		if account.ID == accountID {
			return AccountView{ID: account.ID, Label: account.Label, Username: account.Username}, nil
		}
	}
	// The mailbox was removed, and its drafts with it (the foreign key);
	// a draft read in between is shown without a name rather than failing.
	return AccountView{ID: accountID}, nil
}

// NeverMailed is the recipients the mailbox's Sent folder has no message
// to.
func (s JMAPSender) NeverMailed(ctx context.Context, ownerID, provider, accountID string, recipients []string) ([]string, error) {
	if provider != ProviderJMAP {
		return nil, errors.New("maildrafts: not a JMAP draft")
	}
	return s.Mail.NeverMailed(ctx, ownerID, accountID, recipients)
}
