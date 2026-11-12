package nextcloudimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folderchain"
)

// # Stuck downloads
//
// A document waiting for its file carries the status
// {"state": "pending", "stage": "downloading"}. The download is owed by a
// goroutine of the process that answered the import, not by a queue, so a
// process that is killed takes the debt with it. (A process that is
// stopped normally marks what it could not finish as failed; this is about
// the one that was not given the chance.)
//
// The rule: a document that has been "downloading" for longer than
// StaleAfter is no longer in flight. The next import or refresh that names
// it queues it again, exactly as it would a failed one. Until then it is
// treated as in flight and left alone, so a download that is really
// running is never started twice. The stage is what makes this exact: a
// document that is pending because its file is stored and the indexer has
// not reached it yet has no stage, and is never taken for stuck.

// priorImport is the document an earlier import of the same file made.
type priorImport struct {
	ID    int64
	State string
	// Stage is "downloading" while the document waits for its file.
	Stage string
	// ETag is the file's etag when it was last read; "" if none was kept.
	ETag string
	// Stale: the document was last touched longer ago than StaleAfter.
	Stale bool
}

// disposition is what an import does about one planned file.
type disposition int

const (
	// create: no document yet; make one and download the file.
	create disposition = iota
	// reuse: the account has this file as it is now, or is already
	// fetching or indexing it. Link it; download nothing.
	reuse
	// requeue: the document exists and must be read again: it failed, the
	// file changed in Nextcloud, or its download was abandoned.
	requeue
	// beingDeleted: the document is being deleted and nothing can be
	// attached to it; a moment later the file would be created afresh.
	beingDeleted
)

// decideImport is the whole rule for a file the account may have seen.
func decideImport(prior *priorImport, etag string) disposition {
	if prior == nil {
		return create
	}
	switch prior.State {
	case "deleting":
		return beingDeleted
	case "processing":
		return reuse
	case "pending":
		if prior.Stage == "downloading" && prior.Stale {
			return requeue
		}
		return reuse
	case "ready":
		// Unchanged only when both sides have an etag and they agree. A
		// server that sent none has not said the file is the same.
		if etag != "" && etag == prior.ETag {
			return reuse
		}
	}
	return requeue
}

// Options is what is the same for every file of one import.
type Options struct {
	ConnectorID int64
	// UserID is the account the documents and folders are made for.
	UserID string
	Parser string
	// Vision is the import's use_vision_llm, kept on each document so a
	// re-index or a refresh describes its pictures too.
	Vision bool
	// NotebookID is the notebook to link the files to, or nil.
	NotebookID *int64
}

// Job is one file to download for one document.
type Job struct {
	DocumentID int64
	// UserID is the document's owner. It is in the stored file's key, and
	// every statement the download ends with names it beside the id.
	UserID  string
	Account Account
	Path    string
	// Name is the file's name: the stored original's name, whose extension
	// is what the indexer matches a parser against.
	Name        string
	ContentType string
}

// Started is what an import's transaction did.
type Started struct {
	// DocumentIDs is every document the pick maps to, new or reused, in
	// plan order.
	DocumentIDs []int64
	// FolderIDs is the folder each picked folder became.
	FolderIDs []int64
	// Reused is how many picked files needed nothing.
	Reused int
	// Jobs is the downloads now owed.
	Jobs []Job
}

// errBeingDeleted: a picked file's document is being deleted.
type errBeingDeleted struct{ name string }

func (e *errBeingDeleted) Error() string {
	return fmt.Sprintf("nextcloudimport: the document for %q is being deleted", e.name)
}

// errNotebookGone: the notebook an import was to link to was deleted
// between the route's check and the import's transaction.
var errNotebookGone = errors.New("nextcloudimport: the notebook to link to no longer exists")

// Imported is a document as a refresh decides from it.
type Imported struct {
	Title string
	State string
	Stage string
	Stale bool
	// The Nextcloud facts its import recorded; zero when it was not
	// imported from Nextcloud.
	ConnectorID int64
	ServerURL   string
	Username    string
	Path        string
	ETag        string
	ContentType string
}

// Downloaded is a file now in the store, to be recorded on its document.
type Downloaded struct {
	DocumentID int64
	// UserID is the document's owner, as the job carried it.
	UserID      string
	Backend     string
	Key         string
	Name        string
	ContentType string
	Size        int64
	SHA256      string
}

// Rows is the database as the routes and the downloads use it.
type Rows interface {
	// Connection is the caller's own Nextcloud connection's config, and
	// false when there is none by that id.
	Connection(ctx context.Context, connectorID int64, owner string) ([]byte, bool, error)
	// OwnsNotebook reports whether the notebook is this account's.
	OwnsNotebook(ctx context.Context, notebookID int64, userID string) (bool, error)
	// Start writes an import in one transaction; on any error nothing is
	// written.
	Start(ctx context.Context, plan Plan, account Account, options Options) (Started, error)
	// Imported is this account's document, or nil: another account's is
	// nil exactly as a missing one is.
	Imported(ctx context.Context, documentID int64, userID string) (*Imported, error)
	// QueueRefresh sets the document waiting for its file again, recording
	// what Nextcloud says of the file now. False means a job is already on
	// its way, or the document is being deleted.
	QueueRefresh(ctx context.Context, documentID int64, userID string, connectorID int64, entry Entry) (bool, error)
	// RecordDownloaded writes the file row and hands the document on to
	// the indexer. False means the document no longer wants this file.
	RecordDownloaded(ctx context.Context, file Downloaded) (bool, error)
	// MarkFailed marks a document still waiting for its download failed.
	MarkFailed(ctx context.Context, documentID int64, userID, reason string) error
}

// PostgresRows is Rows over the pool.
type PostgresRows struct {
	Pool *pgxpool.Pool
	// Now is the clock; nil is time.Now. A test sets it.
	Now func() time.Time
}

func (p PostgresRows) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p PostgresRows) Connection(ctx context.Context, connectorID int64, owner string) ([]byte, bool, error) {
	row, err := New(p.Pool).OwnedNextcloudConnection(ctx, OwnedNextcloudConnectionParams{ID: connectorID, Owner: owner})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return row.Config, err == nil, err
}

func (p PostgresRows) OwnsNotebook(ctx context.Context, notebookID int64, userID string) (bool, error) {
	_, err := New(p.Pool).OwnedNotebook(ctx, OwnedNotebookParams{ID: notebookID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Start is the one transaction of an import: the folders, a document per
// file, and the notebook's links. It commits before any download begins,
// which is why the downloads are returned and not started here.
func (p PostgresRows) Start(ctx context.Context, plan Plan, account Account, options Options) (Started, error) {
	return database.Write(ctx, p.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (Started, error) {
		queries := New(tx)
		var started Started
		// Folder chains already made in this transaction, by their names.
		made := map[string]*int64{}
		folderFor := func(chain []string) (*int64, error) {
			key := strings.Join(chain, "\x00")
			if id, known := made[key]; known {
				return id, nil
			}
			id, err := ensureFolders(ctx, tx, options, chain, p.now())
			if err == nil {
				made[key] = id
			}
			return id, err
		}

		if options.NotebookID != nil {
			touched, err := queries.TouchNotebook(ctx, TouchNotebookParams{ID: *options.NotebookID, UserID: options.UserID})
			if err != nil {
				return Started{}, err
			}
			if touched == 0 {
				// Not this account's, or deleted since the route looked.
				return Started{}, errNotebookGone
			}
		}
		for _, picked := range plan.PickedFolders {
			id, err := folderFor([]string{picked.Name})
			if err != nil {
				return Started{}, err
			}
			started.FolderIDs = append(started.FolderIDs, *id)
			if options.NotebookID != nil {
				if err := queries.LinkFolderToNotebook(ctx, LinkFolderToNotebookParams{NotebookID: *options.NotebookID, UserID: options.UserID, FolderID: *id}); err != nil {
					return Started{}, err
				}
			}
		}

		for _, file := range plan.Files {
			folderID, err := folderFor(file.Folders)
			if err != nil {
				return Started{}, err
			}
			documentID, queued, err := p.document(ctx, queries, file, folderID, account, options)
			if err != nil {
				return Started{}, err
			}
			started.DocumentIDs = append(started.DocumentIDs, documentID)
			if queued {
				started.Jobs = append(started.Jobs, Job{
					DocumentID: documentID, UserID: options.UserID, Account: account,
					Path: file.Entry.Path, Name: file.Entry.Name, ContentType: file.Entry.ContentType,
				})
			} else {
				started.Reused++
			}
			// A file from a picked folder reaches the notebook through the
			// folder's link.
			if options.NotebookID != nil && file.PickedFolder == "" {
				if err := queries.LinkDocumentToNotebook(ctx, LinkDocumentToNotebookParams{NotebookID: *options.NotebookID, UserID: options.UserID, DocumentID: documentID}); err != nil {
					return Started{}, err
				}
			}
		}
		return started, nil
	})
}

// document creates, reuses or re-queues the document for one planned file,
// and reports whether its file is now owed a download.
func (p PostgresRows) document(ctx context.Context, queries *Queries, file PlannedFile, folderID *int64, account Account, options Options) (int64, bool, error) {
	identity := identity(account, file.Entry.Path, options.UserID)
	metadata, err := metadataFor(file.Entry, account, options.ConnectorID, options.NotebookID, p.now())
	if err != nil {
		return 0, false, err
	}
	lock := LockImportedParams{UserID: options.UserID, Identity: identity, StaleSeconds: StaleAfter.Seconds()}

	row, err := queries.LockImported(ctx, lock)
	if errors.Is(err, pgx.ErrNoRows) {
		id, err := queries.InsertPendingImport(ctx, InsertPendingImportParams{
			Title: file.Entry.Name, Metadata: metadata, PlaceholderHash: placeholderHash(identity), Identity: identity,
			UserID: options.UserID, FolderID: folderID, Parser: options.Parser, UseVisionLlm: options.Vision,
		})
		if err == nil {
			return id, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, false, err
		}
		// ON CONFLICT DO NOTHING: another import of the same file
		// committed between the read and the insert. Its row is there now.
		if row, err = queries.LockImported(ctx, lock); err != nil {
			return 0, false, err
		}
	} else if err != nil {
		return 0, false, err
	}

	prior := &priorImport{ID: row.ID, State: row.State, Stage: row.Stage, ETag: row.Etag, Stale: row.Stale}
	choice := decideImport(prior, file.Entry.ETag)
	if choice == beingDeleted {
		return 0, false, &errBeingDeleted{name: file.Entry.Name}
	}
	if folderID != nil {
		if err := queries.PlaceInFolder(ctx, PlaceInFolderParams{ID: prior.ID, UserID: options.UserID, FolderID: folderID}); err != nil {
			return 0, false, err
		}
	}
	if choice == reuse {
		return prior.ID, false, nil
	}
	err = queries.RequeueImport(ctx, RequeueImportParams{ID: prior.ID, UserID: options.UserID, Parser: options.Parser, UseVisionLlm: options.Vision, Metadata: metadata})
	return prior.ID, err == nil, err
}

// ensureFolders makes sure a chain of Nextcloud folder names exists in the
// account's tree and returns the innermost folder's id, nil for an empty
// chain. Each name is first made one a folder may carry (folderName);
// finding or making the folders is internal/folderchain's, which the folder
// upload shares.
func ensureFolders(ctx context.Context, tx pgx.Tx, options Options, chain []string, now time.Time) (*int64, error) {
	names := make([]string, len(chain))
	for index, raw := range chain {
		names[index] = folderName(raw)
	}
	return folderchain.Ensure(ctx, tx, options.UserID, nil, names, now)
}

func (p PostgresRows) Imported(ctx context.Context, documentID int64, userID string) (*Imported, error) {
	row, err := New(p.Pool).ImportedDocument(ctx, ImportedDocumentParams{ID: documentID, UserID: userID, StaleSeconds: StaleAfter.Seconds()})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	imported := &Imported{Title: row.Title, State: row.State, Stage: row.Stage, Stale: row.Stale}
	// The block is JSON anybody with a database client could have edited:
	// a key of the wrong type is a document this route cannot refresh, and
	// is read as absent rather than failing the request.
	var block struct {
		ConnectorID any `json:"connector_id"`
		ServerURL   any `json:"server_url"`
		Username    any `json:"username"`
		Path        any `json:"path"`
		ETag        any `json:"etag"`
		ContentType any `json:"content_type"`
	}
	if err := json.Unmarshal(row.Facts, &block); err != nil {
		return imported, nil
	}
	if number, isNumber := block.ConnectorID.(float64); isNumber {
		imported.ConnectorID = int64(number)
	}
	imported.ServerURL, _ = block.ServerURL.(string)
	imported.Username, _ = block.Username.(string)
	imported.Path, _ = block.Path.(string)
	imported.ETag, _ = block.ETag.(string)
	imported.ContentType, _ = block.ContentType.(string)
	return imported, nil
}

func (p PostgresRows) QueueRefresh(ctx context.Context, documentID int64, userID string, connectorID int64, entry Entry) (bool, error) {
	seen := map[string]any{"connector_id": connectorID, "etag": optional(entry.ETag), "size": entry.Size, "modified": nil}
	if entry.Modified != nil {
		seen["modified"] = entry.Modified.Format(isoTime)
	}
	block, err := json.Marshal(seen)
	if err != nil {
		return false, err
	}
	changed, err := New(p.Pool).QueueRefresh(ctx, QueueRefreshParams{ID: documentID, UserID: userID, Facts: block, StaleSeconds: StaleAfter.Seconds()})
	return changed == 1, err
}

// RecordDownloaded is one transaction: the document is handed on to the
// indexer and its file row written, or neither.
func (p PostgresRows) RecordDownloaded(ctx context.Context, file Downloaded) (bool, error) {
	return database.Write(ctx, p.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (bool, error) {
		queries := New(tx)
		changed, err := queries.RecordDownloaded(ctx, RecordDownloadedParams{ID: file.DocumentID, UserID: file.UserID, Checksum: file.SHA256})
		if err != nil || changed == 0 {
			return false, err
		}
		checksum := file.SHA256
		return true, queries.InsertImportedOriginal(ctx, InsertImportedOriginalParams{
			DocumentID: file.DocumentID, UserID: file.UserID, StorageBackend: file.Backend,
			StorageKey: file.Key, OriginalFilename: file.Name, MimeType: optional(file.ContentType),
			SizeBytes: file.Size, Checksum: &checksum,
		})
	})
}

func (p PostgresRows) MarkFailed(ctx context.Context, documentID int64, userID, reason string) error {
	_, err := New(p.Pool).MarkDownloadFailed(ctx, MarkDownloadFailedParams{ID: documentID, UserID: userID, Reason: reason})
	return err
}
