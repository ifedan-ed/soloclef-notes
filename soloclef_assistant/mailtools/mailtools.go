// Package mailtools is the chat's tools for the asker's own mail: which
// mailboxes they have connected, a mailbox's folders, a search, and one
// message read.
//
// # Read-only
//
// Nothing here can send, delete, move or flag mail. That is not a rule
// the tools apply: the client they read with (imapmail) has no command
// that changes a mailbox, opens every folder read-only and never marks a
// message as read.
//
// # Asking first
//
// The three tools that connect to a mail server ask the person first
// (tool.Ask): reading someone's mail is theirs to allow, per call or with
// "always allow". Listing the connected mailboxes does not ask: it reads
// the account's own rows and shows their labels and addresses, which the
// settings page shows the same person.
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
// A message has no address a reader could open, so results carry no [n]
// labels: the prompt has the model name the sender, subject and date in
// its answer instead.
package mailtools

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/imapmail"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
)

// The tools' names: what the model calls, and what a permission rule, an
// approval card and the step display are keyed on.
const (
	ListAccountsTool = "list_email_accounts"
	ListFoldersTool  = "list_email_folders"
	SearchTool       = "search_emails"
	ReadTool         = "read_email"
)

// Names is every tool of this package, in the order they are offered.
func Names() []string { return []string{ListAccountsTool, ListFoldersTool, SearchTool, ReadTool} }

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

// Tools makes the mail tools for a turn.
type Tools struct {
	Rows   Rows
	Mail   Mail
	SignIn SignIn
	Files  Files
	// SearchTimeout bounds one search across all of an account's
	// mailboxes: each mailbox is its own conversation with its own limit,
	// and this is the limit on waiting for the slowest of them.
	SearchTimeout time.Duration
	Logger        *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (t *Tools) Check() error {
	if t.Rows == nil || t.Mail == nil || t.SignIn == nil || t.Files == nil || t.SearchTimeout <= 0 || t.Logger == nil {
		return errors.New("mailtools: Rows, Mail, SignIn, Files, SearchTimeout and Logger are all required")
	}
	return nil
}

// Connected reports whether the account has a mailbox the chat may read.
// A turn of an account with none is offered no mail tools, and its prompt
// does not mention mail.
func (t *Tools) Connected(ctx context.Context, userID string) (bool, error) {
	records, err := t.Rows.Mailboxes(ctx, userID)
	return len(records) > 0, err
}
