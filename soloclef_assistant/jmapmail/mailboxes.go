package jmapmail

import (
	"context"
	"strings"
)

// Mailbox is one folder of the account.
type Mailbox struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Role is RFC 8621's role ("inbox", "drafts", "sent", "trash"...), or
	// "" for a folder of the person's own.
	Role   string `json:"role"`
	Total  int    `json:"totalEmails"`
	Unread int    `json:"unreadEmails"`
}

// Mailboxes is every folder of the session's mail account.
func (c Client) Mailboxes(ctx context.Context, session Session, credential Credential) ([]Mailbox, error) {
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	return c.mailboxes(ctx, session, credential)
}

func (c Client) mailboxes(ctx context.Context, session Session, credential Credential) ([]Mailbox, error) {
	got, err := c.call(ctx, session, credential, mailUsing, invocation{"Mailbox/get", map[string]any{
		"accountId":  session.AccountID,
		"ids":        nil,
		"properties": []string{"id", "name", "role", "totalEmails", "unreadEmails"},
	}, "m"})
	if err != nil {
		return nil, err
	}
	var listed struct {
		List []Mailbox `json:"list"`
	}
	if err := got.decode("m", "Mailbox/get", &listed); err != nil {
		return nil, err
	}
	return listed.List, nil
}

// ByRole is the account's folder with this role, and whether there is one.
func ByRole(mailboxes []Mailbox, role string) (Mailbox, bool) {
	for _, mailbox := range mailboxes {
		if mailbox.Role == role {
			return mailbox, true
		}
	}
	return Mailbox{}, false
}

// Named is the folder a person or the model named: by its role ("inbox",
// "sent"), then by its name, case and surrounding space ignored. It is how
// a folder argument is read, so "Inbox", "INBOX" and "inbox" all find the
// inbox whatever the server calls it.
func Named(mailboxes []Mailbox, name string) (Mailbox, bool) {
	wanted := strings.ToLower(strings.TrimSpace(name))
	if found, ok := ByRole(mailboxes, wanted); ok {
		return found, true
	}
	for _, mailbox := range mailboxes {
		if strings.ToLower(strings.TrimSpace(mailbox.Name)) == wanted {
			return mailbox, true
		}
	}
	return Mailbox{}, false
}
