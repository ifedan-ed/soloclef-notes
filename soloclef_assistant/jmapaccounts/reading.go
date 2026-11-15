package jmapaccounts

import (
	"context"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
)

// The chat's mail tools read a JMAP mailbox through these: each signs in,
// finds the session (cached) and makes one jmapmail call, with a refused
// credential marking the mailbox to be reconnected (Use).

// Folders is the mailbox's folders.
func (m *Mail) Folders(ctx context.Context, ownerID, accountID string) ([]jmapmail.Mailbox, error) {
	var folders []jmapmail.Mailbox
	err := m.Use(ctx, ownerID, accountID, func(opened Opened) error {
		var err error
		folders, err = m.Client.Mailboxes(ctx, opened.Session, opened.Credential)
		return err
	})
	return folders, err
}

// Search searches one folder of the mailbox, named as a person or the
// model names it ("INBOX", "Sent", a folder's own name), or every folder
// for "*".
func (m *Mail) Search(ctx context.Context, ownerID, accountID, folder string, query jmapmail.Query) ([]jmapmail.Summary, bool, error) {
	var found []jmapmail.Summary
	more := false
	err := m.Use(ctx, ownerID, accountID, func(opened Opened) error {
		if strings.TrimSpace(folder) != "*" {
			folders, err := m.Client.Mailboxes(ctx, opened.Session, opened.Credential)
			if err != nil {
				return err
			}
			named, ok := jmapmail.Named(folders, folder)
			if !ok {
				return &jmapmail.Failure{Message: "The mailbox has no folder named " + strings.TrimSpace(folder) + "; list its folders for their names"}
			}
			query.MailboxID = named.ID
		}
		var err error
		found, more, err = m.Client.Search(ctx, opened.Session, opened.Credential, query)
		return err
	})
	return found, more, err
}

// Read is one message, not marked as read.
func (m *Mail) Read(ctx context.Context, ownerID, accountID, messageID string) (jmapmail.Email, error) {
	var email jmapmail.Email
	err := m.Use(ctx, ownerID, accountID, func(opened Opened) error {
		var err error
		email, err = m.Client.Read(ctx, opened.Session, opened.Credential, messageID)
		return err
	})
	return email, err
}

// Thread is the conversation a message is in, oldest first.
func (m *Mail) Thread(ctx context.Context, ownerID, accountID, messageID string) ([]jmapmail.Email, bool, error) {
	var thread []jmapmail.Email
	more := false
	err := m.Use(ctx, ownerID, accountID, func(opened Opened) error {
		var err error
		thread, more, err = m.Client.Thread(ctx, opened.Session, opened.Credential, messageID)
		return err
	})
	return thread, more, err
}

// Download is one attachment's bytes, at most most of them.
func (m *Mail) Download(ctx context.Context, ownerID, accountID string, attachment jmapmail.Attachment, most int64) ([]byte, error) {
	var data []byte
	err := m.Use(ctx, ownerID, accountID, func(opened Opened) error {
		var err error
		data, err = m.Client.Download(ctx, opened.Session, opened.Credential, attachment, most)
		return err
	})
	return data, err
}

// Sender is the identity a draft from the mailbox is written from: the
// one whose address is the mailbox's own, or its only one.
func (m *Mail) Sender(ctx context.Context, ownerID, accountID string) (jmapmail.Identity, error) {
	var chosen jmapmail.Identity
	err := m.Use(ctx, ownerID, accountID, func(opened Opened) error {
		if !opened.Session.CanSend {
			return ErrCannotSend
		}
		identities, err := m.Client.Identities(ctx, opened.Session, opened.Credential)
		if err != nil {
			return err
		}
		for _, identity := range identities {
			if strings.EqualFold(identity.Email, opened.Account.Username) || strings.EqualFold(identity.Email, opened.Session.Username) {
				chosen = identity
				return nil
			}
		}
		if len(identities) == 1 {
			chosen = identities[0]
			return nil
		}
		// Several, none of them the sign-in's own address: which one a
		// message goes out as is the person's to say, and is not guessed.
		return &jmapmail.Failure{Message: "This mailbox has several sending addresses and none is its own; drafts from it are not offered"}
	})
	return chosen, err
}

// Limits is what the mailbox's server accepts of a message's files: the
// largest one file (its upload limit) and all of them together, each 0
// where the server states none. A draft's attachments are checked against
// them when the draft is made, so a file the server would refuse is said
// to be too large then, not when the person presses Send.
type Limits struct {
	MostFileBytes        int64
	MostAttachmentsBytes int64
}

// Limits reads the mailbox's limits from its session (cached).
func (m *Mail) Limits(ctx context.Context, ownerID, accountID string) (Limits, error) {
	opened, err := m.Open(ctx, ownerID, accountID)
	if err != nil {
		return Limits{}, err
	}
	return Limits{MostFileBytes: opened.Session.MaxSizeUpload, MostAttachmentsBytes: opened.Session.MaxSizeAttachmentsPerEmail}, nil
}

// NeverMailed is the addresses, of those given, that the mailbox has never
// sent a message to (jmapmail.Client.NeverMailed).
func (m *Mail) NeverMailed(ctx context.Context, ownerID, accountID string, addresses []string) ([]string, error) {
	var out []string
	err := m.Use(ctx, ownerID, accountID, func(opened Opened) error {
		var err error
		out, err = m.Client.NeverMailed(ctx, opened.Session, opened.Credential, addresses)
		return err
	})
	return out, err
}
