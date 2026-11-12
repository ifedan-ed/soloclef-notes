package drivesync

import (
	"context"
	"time"
)

// Connection is an account's Drive connection as the job reads it.
type Connection struct {
	ID int64
	// Config is the row's config: the grant, sealed, and the feed position.
	Config []byte
	// Enabled is false for a connection the person switched off: it keeps
	// its grant and is left alone.
	Enabled bool
}

// Document is an imported Drive file's document, as the job decides from
// it.
type Document struct {
	ID     int64
	FileID string
	// Title is the document's title, which is the file's name unless the
	// person changed it here.
	Title string
	// Name is the file's name in Drive when it was last read.
	Name string
	// State and Stage are the document's status. Stage is "downloading"
	// while the document waits for this job to fetch its file.
	State, Stage string
	// Stale: a document that is "downloading" has waited longer than
	// StaleAfter.
	Stale bool
	// MD5 and Modified are what Drive said of the content that is stored;
	// both are empty until a file has been stored.
	MD5, Modified string
	// GoneSince is when a sync found the file binned or removed in Drive
	// (RFC 3339), or "" while Drive has it. A gone file's document is kept
	// and no longer synced.
	GoneSince string
}

// owed reports whether the document is waiting for its file.
func (d Document) owed() bool { return d.State == "pending" && d.Stage == "downloading" }

// Downloaded is a file now in the store, to be recorded on its document.
type Downloaded struct {
	DocumentID  int64
	UserID      string
	ConnectorID int64
	Backend     string
	Key         string
	// Name is the stored original's name, with the extension the indexer
	// chooses a parser by.
	Name        string
	ContentType string
	Size        int64
	SHA256      string
	// File is what Drive said of the file that was stored.
	File remoteFile
	At   time.Time
}

// Rows is the database as the job uses it (Store). Every method takes the
// account and names it in its statement.
type Rows interface {
	// Connection is the account's Drive connection by id, or nil: another
	// account's is nil exactly as a missing one is.
	Connection(ctx context.Context, userID string, connectorID int64) (*Connection, error)
	// MergeConfig merges a JSON object over the connection's config: a
	// renewed token, the feed position.
	MergeConfig(ctx context.Context, userID string, connectorID int64, patch []byte) error
	// DropPageToken forgets the feed position.
	DropPageToken(ctx context.Context, userID string, connectorID int64) error
	// MarkExpired flags the connection as needing a new consent.
	MarkExpired(ctx context.Context, userID string, connectorID int64) error
	// Synced stamps the connection as brought up to date now.
	Synced(ctx context.Context, userID string, connectorID int64) error

	// Documents is the account's documents for these Drive files, by file
	// id. A file with no document is not in the map.
	Documents(ctx context.Context, userID string, fileIDs []string) (map[string]Document, error)
	// Imported is every Drive file's document the account has.
	Imported(ctx context.Context, userID string) ([]Document, error)
	// Abandoned is the account's documents that have waited for their
	// download longer than StaleAfter.
	Abandoned(ctx context.Context, userID string) ([]Document, error)
	// Requeue sets a document waiting for its file again. False means it
	// is being deleted, or is gone.
	Requeue(ctx context.Context, userID string, documentID int64) (bool, error)
	// MarkGone records on a document that its file is gone from Drive,
	// and when; the document itself is left as it is.
	MarkGone(ctx context.Context, userID string, documentID int64, at time.Time) error
	// MarkBack forgets that a document's file was gone: Drive has it again.
	MarkBack(ctx context.Context, userID string, documentID int64) error
	// Rename records a file's new name, and retitles the document if its
	// title was still the old name.
	Rename(ctx context.Context, userID string, documentID int64, oldName, newName string) error
	// RecordDownloaded writes the file row and hands the document on to
	// the indexer. False means the document no longer wants this file.
	RecordDownloaded(ctx context.Context, file Downloaded) (bool, error)
	// MarkFailed marks a document still waiting for its download failed.
	MarkFailed(ctx context.Context, userID string, documentID int64, reason string) error
	// MarkQueueingFailed marks a document whose index job could not be
	// published failed, if it is still waiting for the indexer.
	MarkQueueingFailed(ctx context.Context, userID string, documentID int64, reason string) error
}
