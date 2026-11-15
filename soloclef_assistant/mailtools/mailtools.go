// Package mailtools is the chat's tools for the asker's own mail: which
// mailboxes they have connected (IMAP, Microsoft, and JMAP on a Stalwart
// server), a mailbox's folders, a search, one message or a whole thread
// read, and a draft for a JMAP mailbox that may send.
//
// # Read-only, and drafts
//
// Nothing here can send, delete, move, flag, forward or reply-all, and no
// tool exists for any of it. The IMAP client (imapmail) has no command that
// changes a mailbox, opens every folder read-only and never marks a
// message as read; the JMAP reads (jmapmail) are /get and /query calls.
//
// The one thing beyond reading is draft_email: it stores a draft with the
// send guard (maildrafts) and nothing more. A draft may carry files the
// person asked to send (attach.go): their documents, Studio outputs, or an
// attachment of a message read, to forward. The draft is shown on a card,
// and only the person, pressing Send there, can send it; the guard binds
// that press to the exact content shown. A draft made because an email
// said so is still only a draft on a card, and a reply's recipients who
// were not on the message replied to are flagged on it.
//
// # Asking first
//
// The tools that connect to a mail server to read are declared tool.Ask:
// reading someone's mail is theirs to allow, which is what an automation's
// unattended run goes by. In a chat the person's own "ask first" marks
// decide instead (assembly, policy), and every tool runs unless marked.
// Listing the connected mailboxes is declared tool.Auto: it reads the
// account's own rows and shows their labels and addresses, which the
// settings page shows the same person. draft_email is tool.Auto too: it
// sends nothing and changes nothing in the mailbox, and the card it makes
// is itself the question, asked every time, with the message in it. So no
// "ask first" mark applies to it (assembly's cardIsTheQuestion) and the
// settings page does not offer one: a prompt before the card would only ask
// whether to show the person their question.
//
// # Whose mail
//
// The account is fixed when the tools are made, by the server, and is not
// an argument: the model names a mailbox by its id, and an id that is not
// one of the account's mailboxes is "no such mailbox", whatever it is in
// another account.
//
// # What a message is
//
// A message is somebody else's text. Every result that holds one says so
// and wraps it in a block the text cannot close, so a message that says
// "ignore your instructions" is read as a message that says that.
//
// # Citations
//
// A message is cited the way a web page is: every message a search lists
// or a read returns carries a [n] label from the turn's registry. It has no
// address a reader could open, so its chip shows the sender, subject and
// day instead (cite.MailSource; citations.go says how one message keeps one
// number across a search and a read).
package mailtools

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/imapmail"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/maildrafts"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
)

// The tools' names: what the model calls, and what a permission rule, an
// approval card and the step display are keyed on.
const (
	ListAccountsTool = "list_email_accounts"
	ListFoldersTool  = "list_email_folders"
	SearchTool       = "search_emails"
	ReadTool         = "read_email"
	ThreadTool       = "read_email_thread"
	DraftTool        = "draft_email"
)

// Names is every tool of this package, in the order they are offered.
func Names() []string {
	return []string{ListAccountsTool, ListFoldersTool, SearchTool, ReadTool, ThreadTool, DraftTool}
}

// Rows is an account's mailboxes in the database.
type Rows interface {
	// Mailboxes is every stored record of the owner's mail connection,
	// oldest first; none when the connection is switched off.
	Mailboxes(ctx context.Context, ownerID string) ([][]byte, error)
	// Parser is the parser the owner's mail connection chose for its
	// attachments, "" for the deployment's default.
	Parser(ctx context.Context, ownerID string) (string, error)
}

// Store is Rows over the pool, and the write a Microsoft mailbox's token
// refresh needs (mailsignin.Merger). Every statement names the owner.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) Mailboxes(ctx context.Context, ownerID string) ([][]byte, error) {
	return New(s.Pool).EnabledMailboxesOfOwner(ctx, ownerID)
}

func (s Store) Parser(ctx context.Context, ownerID string) (string, error) {
	parser, err := New(s.Pool).MailParserOfOwner(ctx, ownerID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Switched off since the turn began: the read that follows says so.
		return "", nil
	case err != nil || parser == nil:
		return "", err
	}
	return *parser, nil
}

// Merge writes keys into one of the owner's Microsoft mailboxes and
// reports whether there was one.
func (s Store) Merge(ctx context.Context, ownerID, accountID string, fields []byte) (bool, error) {
	changed, err := New(s.Pool).MergeMailboxFields(ctx, MergeMailboxFieldsParams{
		AccountID: accountID, Fields: fields, UserID: ownerID,
	})
	return changed > 0, err
}

// Mail reads a mailbox (imapmail.IMAP).
type Mail interface {
	FolderNames(ctx context.Context, settings imapmail.Settings) ([]string, error)
	Search(ctx context.Context, settings imapmail.Settings, query imapmail.Query) ([]imapmail.Summary, bool, error)
	Read(ctx context.Context, settings imapmail.Settings, folder string, uid uint32) (imapmail.Message, error)
	// ReadAttachment is Read with the named attachment's bytes held whole.
	ReadAttachment(ctx context.Context, settings imapmail.Settings, folder string, uid uint32, filename string) (imapmail.Message, error)
}

// SignIn turns a stored mailbox into settings that sign in
// (mailsignin.SignIn).
type SignIn interface {
	Settings(ctx context.Context, ownerID string, stored []byte) (imapmail.Settings, error)
}

// Files reads an attachment that is not text: a document by the parser
// the mail connection chose, a picture by the account's vision model
// (accountfiles.Reader).
type Files interface {
	Read(ctx context.Context, userID, connectionParser string, file fileread.File) (fileread.Text, error)
}

// JMAPMail reads the account's JMAP mailboxes (jmapaccounts.Mail).
type JMAPMail interface {
	Accounts(ctx context.Context, ownerID string) ([]jmapaccounts.Account, error)
	Folders(ctx context.Context, ownerID, accountID string) ([]jmapmail.Mailbox, error)
	Search(ctx context.Context, ownerID, accountID, folder string, query jmapmail.Query) ([]jmapmail.Summary, bool, error)
	Read(ctx context.Context, ownerID, accountID, messageID string) (jmapmail.Email, error)
	Thread(ctx context.Context, ownerID, accountID, messageID string) ([]jmapmail.Email, bool, error)
	Download(ctx context.Context, ownerID, accountID string, attachment jmapmail.Attachment, most int64) ([]byte, error)
	// Limits is what the mailbox's server accepts of a message's files.
	Limits(ctx context.Context, ownerID, accountID string) (jmapaccounts.Limits, error)
	// Sender is the identity a draft from the mailbox is written from.
	Sender(ctx context.Context, ownerID, accountID string) (jmapmail.Identity, error)
}

// Drafts stores a draft with the send guard (maildrafts.Guard). It is the
// only thing the mail tools can write, and it sends nothing.
type Drafts interface {
	Create(ctx context.Context, ownerID, provider string, message maildrafts.Message, replaces string) (maildrafts.Created, error)
}

// Tools makes the mail tools for a turn.
type Tools struct {
	Rows   Rows
	Mail   Mail
	SignIn SignIn
	JMAP   JMAPMail
	Drafts Drafts
	Files  Files
	// Attachments finds the files a draft carries.
	Attachments Attachments
	// SearchTimeout bounds one search across all of an account's
	// mailboxes: each mailbox is its own conversation with its own limit,
	// and this is the limit on waiting for the slowest of them.
	SearchTimeout time.Duration
	Logger        *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (t *Tools) Check() error {
	if t.Rows == nil || t.Mail == nil || t.SignIn == nil || t.JMAP == nil || t.Drafts == nil || t.Files == nil ||
		t.Attachments == nil || t.SearchTimeout <= 0 || t.Logger == nil {
		return errors.New("mailtools: Rows, Mail, SignIn, JMAP, Drafts, Files, Attachments, SearchTimeout and Logger are all required")
	}
	return nil
}

// Connected reports whether the account has a mailbox the chat may read.
// A turn of an account with none is offered no mail tools, and its prompt
// does not mention mail.
func (t *Tools) Connected(ctx context.Context, userID string) (bool, error) {
	records, err := t.Rows.Mailboxes(ctx, userID)
	if err != nil || len(records) > 0 {
		return len(records) > 0, err
	}
	accounts, err := t.JMAP.Accounts(ctx, userID)
	return len(accounts) > 0, err
}
