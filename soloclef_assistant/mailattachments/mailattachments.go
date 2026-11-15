// Package mailattachments finds the files a mail draft carries and reads
// them back when the draft is sent: a document's original upload, a Studio
// output's file (pptx, pdf, docx and the rest), or an attachment of a
// message in one of the owner's JMAP mailboxes, forwarded.
//
// # Three moments
//
//   - When the model drafts (Resolve): the file it names is found among the
//     owner's own rows, its size checked against the mail server's limit,
//     and its SHA-256 taken (the one its row recorded when it was written,
//     else read from its bytes). The result is a maildrafts.Attachment,
//     part of the draft's hashed content, so the go-ahead the person gives
//     is for exactly these bytes.
//   - When the card asks for a go-ahead (Changed): the rows behind the
//     files are looked at again, cheaply, so a draft whose file was removed
//     or replaced since is not offered for sending.
//   - When the person presses Send (Open): the bytes are streamed from
//     where they live. The send guard checks them against the digest as
//     they go (maildrafts.verified), so nothing here needs to trust them.
//
// # Whose files
//
// The owner is fixed by the server for the whole turn, and every statement
// names it: an id of another account's document is "no such document". A
// draft's card lists every file, so the person sees each one that would
// leave, with where it came from, before anything is sent.
//
// # Why bytes are not copied
//
// A stored file is never overwritten (soloclef_filestore: every key names
// one object, written once), and a JMAP blob never changes under its id
// (RFC 8620 6). So a draft records where a file is, not a copy of it: no
// second store of the person's files to keep, expire and delete.
package mailattachments

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"

	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
)

// Stores opens the backend a row names (*filestore.Stores).
type Stores interface {
	Named(backend string) (filestore.Store, error)
}

// Mailboxes reads the owner's JMAP mailboxes (*jmapaccounts.Mail).
type Mailboxes interface {
	Read(ctx context.Context, ownerID, accountID, messageID string) (jmapmail.Email, error)
	Download(ctx context.Context, ownerID, accountID string, attachment jmapmail.Attachment, most int64) ([]byte, error)
}

// Files finds and reads a draft's files. Every field is required.
type Files struct {
	Pool   *pgxpool.Pool
	Stores Stores
	Mail   Mailboxes
}

// Check reports what is missing. Called once at start-up.
func (f *Files) Check() error {
	if f.Pool == nil || f.Stores == nil || f.Mail == nil {
		return errors.New("mailattachments: Pool, Stores and Mail are all required")
	}
	return nil
}

// Request is one file the model asked to attach, by exactly one of: a
// document's id, a Studio output's artifact id, a document's title (or the
// name it was uploaded under), or a message's attachment by its file name.
type Request struct {
	DocumentID int64
	ArtifactID int64
	Title      string
	// AccountID, MessageID and Filename name an attachment of a message in
	// one of the owner's JMAP mailboxes.
	AccountID, MessageID, Filename string
}

// Refused is a file that cannot be attached, in words the model can act on
// and pass on (no such document, two documents of that title, too large).
type Refused struct{ Message string }

func (r *Refused) Error() string { return r.Message }

func refused(message string) error { return &Refused{Message: message} }

// fileName is a stored file's name as an attachment carries it: its last
// path segment, control characters out, one line. A name is somebody's
// text, and a header cannot carry a line break or a path.
func fileName(name string) string {
	if index := strings.LastIndexAny(name, `/\`); index >= 0 {
		name = name[index+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if runes := []rune(name); len(runes) > 200 {
		name = string(runes[:200])
	}
	return name
}

// mediaType is a stored file's type as an attachment is sent with it. A
// row with none (an upload whose type nobody recorded) is sent as bytes,
// the protocols' own word for "type not known", and is never guessed from
// its name.
func mediaType(recorded *string) string {
	if recorded == nil || strings.TrimSpace(*recorded) == "" {
		return "application/octet-stream"
	}
	return strings.TrimSpace(*recorded)
}
