// Package nextcloudtools is the chat's tools for the asker's Nextcloud:
// find files, read one, write a new one, and move one to the bin.
//
// # What it reaches
//
// The account's own files on the deployment's one Nextcloud, through the
// app password the person granted when they connected it
// (soloclef_api/internal/nextcloudconnect). The connection's row is read
// by a statement that names the owner, and the stored password is used
// only if the row names the deployment's server (nextcloud.Credentials):
// the model never names an account or a server.
//
// # Asking
//
// Searching and reading ask first (tool.Ask): each sends the person's app
// password to their Nextcloud, which is theirs to allow, per call or with
// "always allow". Writing a file and binning one ask every time
// (tool.AlwaysAsk): each changes the person's files with something the
// model chose, and one approval says nothing about the next file. No trust
// rule and no automation's setting waives that.
//
// A write never replaces anything: a new file is refused by Nextcloud
// itself when its name is taken (If-None-Match: *), and only a file, never
// a folder, can be moved to the bin, from where the person can restore it.
//
// # Reading
//
// A file is read whole by the reader every chat tool reads with
// (accountfiles over soloclef_ingest/fileread): a PDF, Word file,
// PowerPoint or EPUB by the parser the Nextcloud connection chose, a
// picture by the account's vision model, text as it is. Its size is
// checked against the reader's limit from the listing, before a byte is
// downloaded.
//
// # Citations
//
// A file found or read is labelled [n] from the turn's registry and cites
// its address in Nextcloud's web interface ({server}/f/{fileid}), which
// follows the file when it is moved, the way a web result cites its page.
package nextcloudtools

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
)

// The tools' names: what the model calls, and what a permission rule, an
// approval card and the step display are keyed on.
const (
	SearchTool = "search_nextcloud_files"
	ReadTool   = "read_nextcloud_file"
	CreateTool = "create_nextcloud_file"
	TrashTool  = "trash_nextcloud_file"
)

// Names is every tool of this package, in the order they are offered.
func Names() []string { return []string{SearchTool, ReadTool, CreateTool, TrashTool} }

// Connection is an account's Nextcloud connection: its row's id, config
// (the sealed app password) and the parser it chose ("" for the
// deployment's default).
type Connection struct {
	ID     int64
	Config []byte
	Parser string
}

// Rows is an account's Nextcloud connection in the database.
type Rows interface {
	// Connection is the owner's Nextcloud connection that is switched on;
	// found is false when there is none.
	Connection(ctx context.Context, ownerID string) (connection Connection, found bool, err error)
}

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) Connection(ctx context.Context, ownerID string) (Connection, bool, error) {
	row, err := New(s.Pool).NextcloudConnectionOfOwner(ctx, ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, false, nil
	}
	if err != nil {
		return Connection{}, false, err
	}
	parser := ""
	if row.Parser != nil {
		parser = *row.Parser
	}
	return Connection{ID: row.ID, Config: row.Config, Parser: parser}, true, nil
}

// Files reads a file: a document by the parser the connection chose, a
// picture by the account's vision model, text as it is
// (accountfiles.Reader).
type Files interface {
	Read(ctx context.Context, userID, connectionParser string, file fileread.File) (fileread.Text, error)
}

// Tools makes the Nextcloud tools for a turn.
type Tools struct {
	Rows  Rows
	Files Files
	// Credentials opens a connection's stored app password, for the
	// deployment's one server.
	Credentials nextcloud.Credentials
	// Transport carries every request to Nextcloud. The clients built on
	// it never follow a redirect.
	Transport http.RoundTripper
	// Timing is how long a request to Nextcloud may take.
	Timing nextcloud.Timing
	Logger *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (t *Tools) Check() error {
	switch {
	case t.Rows == nil || t.Files == nil || t.Transport == nil || t.Logger == nil:
		return errors.New("nextcloudtools: Rows, Files, Transport and Logger are all required")
	case t.Credentials.SecretKey == "" || t.Credentials.ServerURL == "":
		return errors.New("nextcloudtools: the credentials must name the secret key and the server (nextcloud.NewCredentials)")
	case t.Timing.Listing <= 0 || t.Timing.Stall <= 0 || t.Timing.MaxRetryWait <= 0:
		return errors.New("nextcloudtools: every time of the timing must be positive")
	}
	return nil
}

// Connected reports whether the account has a Nextcloud connection the
// chat may use. A turn of an account with none is offered no Nextcloud
// tools, and its prompt does not mention Nextcloud.
func (t *Tools) Connected(ctx context.Context, userID string) (bool, error) {
	_, found, err := t.Rows.Connection(ctx, userID)
	return found, err
}
