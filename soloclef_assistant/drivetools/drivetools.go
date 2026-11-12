// Package drivetools is the chat's tools for the asker's Google Drive:
// find a file, read one, make a Google Doc or Sheet, and move a file to
// the bin.
//
// # What the grant reaches
//
// An account connects Drive with the `drive.file` scope
// (soloclef_api/internal/driveconnect): the grant reaches only the files
// the person picked in Google's picker and the files this app made. So
// "search the user's Drive" here is a search of those files, and the tool
// says so; it is never the whole Drive, and nothing here could make it so.
//
// # Asking
//
// Finding and reading ask first (tool.Ask): they send a request to Google
// with the person's grant, which is theirs to allow, per call or with
// "always allow". Making a file and binning one ask every time
// (tool.AlwaysAsk): each writes to the person's Drive something the model
// chose, and one approval says nothing about the next file. No trust rule
// and no automation's setting waives that.
//
// # Whose Drive
//
// The account is fixed when the tools are made, by the server. The grant
// used is the one on that account's own connection row, read by a
// statement that names the owner; the model never names an account.
//
// # Citations
//
// A file found or read is labelled [n] from the turn's registry and cites
// its address in Drive, the way a web result cites its page.
package drivetools

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivegrant"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
)

// The tools' names.
const (
	SearchTool = "search_google_drive"
	ReadTool   = "read_google_drive_file"
	CreateTool = "create_google_drive_file"
	TrashTool  = "trash_google_drive_file"
)

// Names is every tool of this package, in the order they are offered.
func Names() []string { return []string{SearchTool, ReadTool, CreateTool, TrashTool} }

// Google's addresses the tools talk to. Constants here and fields on
// Tools so that a test can stand in for Google.
const (
	GoogleTokenURL  = "https://oauth2.googleapis.com/token"
	GoogleDriveURL  = "https://www.googleapis.com/drive/v3"
	GoogleUploadURL = "https://www.googleapis.com/upload/drive/v3"
)

// Connection is an account's Drive connection: its row's id and config,
// and the parser it chose for its documents ("" for the default).
type Connection struct {
	ID     int64
	Config []byte
	Parser string
}

// Rows is an account's Drive connection in the database. Every method
// takes the owner and every statement names it.
type Rows interface {
	// Connection is the owner's Drive connection that is switched on;
	// found is false when there is none.
	Connection(ctx context.Context, ownerID string) (connection Connection, found bool, err error)
	// MergeConfig merges a JSON object over the connection's config: a
	// renewed token, or the flag that says to connect again.
	MergeConfig(ctx context.Context, ownerID string, connectionID int64, patch []byte) error
}

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) Connection(ctx context.Context, ownerID string) (Connection, bool, error) {
	row, err := New(s.Pool).DriveConnectionOfOwner(ctx, ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, false, nil
	}
	connection := Connection{ID: row.ID, Config: row.Config}
	if row.Parser != nil {
		connection.Parser = *row.Parser
	}
	return connection, err == nil, err
}

func (s Store) MergeConfig(ctx context.Context, ownerID string, connectionID int64, patch []byte) error {
	_, err := New(s.Pool).MergeDriveConfig(ctx, MergeDriveConfigParams{Patch: patch, ID: connectionID, UserID: ownerID})
	return err
}

// Files reads a file that is not text: a document by the parser the Drive
// connection chose, a picture by the account's vision model
// (accountfiles.Reader).
type Files interface {
	Read(ctx context.Context, userID, connectionParser string, file fileread.File) (fileread.Text, error)
}

// Tools makes the Drive tools for a turn.
type Tools struct {
	Rows Rows
	// Files reads a PDF, an Office file, an EPUB or a picture.
	Files Files
	// Google renews a grant at the project it was made for.
	Google drivegrant.Client
	// SecretKey is the deployment's secret key, which the grant's tokens
	// are sealed with.
	SecretKey string
	// DriveURL and UploadURL are Drive's API roots.
	DriveURL, UploadURL string
	// HTTP makes the requests to Drive. It must not follow redirects away
	// with the grant's token; NewHTTP makes one that does not.
	HTTP *http.Client
	// Timeout bounds one tool call's work at Google.
	Timeout time.Duration
	Now     func() time.Time
	Logger  *slog.Logger
}

// NewHTTP is a client for Drive: bounded, and never following a redirect,
// so the grant's token is only ever sent to the address it was meant for.
func NewHTTP(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Check reports what is missing. Called once at start-up.
func (t *Tools) Check() error {
	switch {
	case t.Rows == nil || t.Files == nil || t.HTTP == nil || t.Now == nil || t.Logger == nil:
		return errors.New("drivetools: Rows, Files, HTTP, Now and Logger are all required")
	case t.Google.ClientID == "" || t.Google.ClientSecret == "" || t.Google.TokenURL == "" || t.Google.HTTP == nil:
		return errors.New("drivetools: the Google project's client, secret, token address and HTTP client are all required")
	case t.SecretKey == "" || t.DriveURL == "" || t.UploadURL == "" || t.Timeout <= 0:
		return errors.New("drivetools: SecretKey, DriveURL, UploadURL and Timeout are all required")
	}
	return nil
}

// Connected reports whether the account has a Drive connection the chat
// may use. A turn of an account with none is offered no Drive tools.
func (t *Tools) Connected(ctx context.Context, userID string) (bool, error) {
	_, found, err := t.Rows.Connection(ctx, userID)
	return found, err
}
