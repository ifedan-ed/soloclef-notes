package drivesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivefile"
)

// driveKind is a Drive connection's `connector_type`.
const driveKind = "GOOGLE_DRIVE_CONNECTOR"

// Connection is the caller's connection as these routes read it.
type Connection struct {
	ID int64
	// Kind is its connector type; only a Drive connection is synced here.
	Kind    string
	Config  []byte
	Enabled bool
	// Parser is the parser the connection's files are read with, "" when
	// none was chosen.
	Parser string
}

// Import is what one import writes.
type Import struct {
	ConnectorID int64
	UserID      string
	// NotebookID is the notebook to link the files to, or nil.
	NotebookID *int64
	Picks      []Pick
}

// Started is what an import's transaction did.
type Started struct {
	// DocumentIDs is the document of each pick, new or already there, in
	// the picks' order.
	DocumentIDs []int64
	// Waiting is the documents now waiting for the job this import owes:
	// the ones to mark failed if it cannot be published.
	Waiting []int64
}

// errBeingDeleted: a picked file's document is being deleted, and nothing
// can be attached to it; a moment later the file would be made afresh.
type errBeingDeleted struct{ name string }

func (e *errBeingDeleted) Error() string {
	return fmt.Sprintf("drivesync: the document for %q is being deleted", e.name)
}

// errNotebookGone: the notebook was deleted between the route's check and
// the import's transaction.
var errNotebookGone = errors.New("drivesync: the notebook to link to no longer exists")

// Rows is the database as the routes use it. Every method takes the
// account and every statement names it.
type Rows interface {
	// Connection is the caller's own connection, or nil: another
	// account's is nil exactly as a missing one is.
	Connection(ctx context.Context, userID string, connectorID int64) (*Connection, error)
	// OwnsNotebook reports whether the notebook is this account's.
	OwnsNotebook(ctx context.Context, userID string, notebookID int64) (bool, error)
	// Start writes an import in one transaction; on any error nothing is
	// written.
	Start(ctx context.Context, start Import) (Started, error)
	// MarkNotQueued marks the documents still waiting for a job that was
	// never published as failed.
	MarkNotQueued(ctx context.Context, userID string, documentIDs []int64, reason string) error
}

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

func (s Store) Connection(ctx context.Context, userID string, connectorID int64) (*Connection, error) {
	row, err := New(s.Pool).OwnedConnection(ctx, OwnedConnectionParams{ID: connectorID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	held := &Connection{ID: row.ID, Kind: row.Kind, Config: row.Config, Enabled: row.Enabled}
	if row.Parser != nil {
		held.Parser = *row.Parser
	}
	return held, nil
}

func (s Store) OwnsNotebook(ctx context.Context, userID string, notebookID int64) (bool, error) {
	_, err := New(s.Pool).OwnedNotebook(ctx, OwnedNotebookParams{ID: notebookID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Start is the one transaction of an import: a document per pick and the
// notebook's links. It commits before the job is published, which is why
// the documents now waiting are returned and nothing is published here.
func (s Store) Start(ctx context.Context, start Import) (Started, error) {
	return database.Write(ctx, s.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (Started, error) {
		queries := New(tx)
		var started Started
		if start.NotebookID != nil {
			touched, err := queries.TouchNotebook(ctx, TouchNotebookParams{ID: *start.NotebookID, UserID: start.UserID})
			if err != nil {
				return Started{}, err
			}
			if touched == 0 {
				return Started{}, errNotebookGone
			}
		}
		for _, pick := range start.Picks {
			id, waiting, err := document(ctx, queries, start, pick)
			if err != nil {
				return Started{}, err
			}
			started.DocumentIDs = append(started.DocumentIDs, id)
			if waiting {
				started.Waiting = append(started.Waiting, id)
			}
			if start.NotebookID != nil {
				if err := queries.LinkDocumentToNotebook(ctx, LinkDocumentToNotebookParams{
					NotebookID: *start.NotebookID, UserID: start.UserID, DocumentID: id,
				}); err != nil {
					return Started{}, err
				}
			}
		}
		return started, nil
	})
}

// document makes, or finds, the document for one pick, and reports whether
// it is now waiting for its file.
func document(ctx context.Context, queries *Queries, start Import, pick Pick) (int64, bool, error) {
	identity := drivefile.Identity(pick.ID, start.UserID)
	lock := LockImportedParams{UserID: start.UserID, Identity: identity}
	row, err := queries.LockImported(ctx, lock)
	if errors.Is(err, pgx.ErrNoRows) {
		metadata, err := pendingMetadata(start.ConnectorID, pick)
		if err != nil {
			return 0, false, err
		}
		id, err := queries.InsertPending(ctx, InsertPendingParams{
			Title: pick.Name, Metadata: metadata, PlaceholderHash: drivefile.PlaceholderHash(identity),
			Identity: identity, UserID: start.UserID, ConnectorID: start.ConnectorID,
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
	if row.State == "deleting" {
		return 0, false, &errBeingDeleted{name: pick.Name}
	}
	owed, err := queries.OweAgain(ctx, OweAgainParams{ID: row.ID, UserID: start.UserID})
	return row.ID, owed == 1, err
}

// pendingMetadata is a new document's metadata: which Drive file it is.
// What Drive says of the file's content (its checksum, when it changed)
// is added by the worker when the bytes are stored, so a document that
// has never had a file carries nothing a later sync could take for "the
// same".
func pendingMetadata(connectorID int64, pick Pick) ([]byte, error) {
	return json.Marshal(map[string]any{
		"FILE_NAME": pick.Name,
		"source":    "google_drive",
		"google_drive": map[string]any{
			"connector_id": connectorID, "file_id": pick.ID, "name": pick.Name, "mime_type": pick.MimeType,
		},
	})
}

func (s Store) MarkNotQueued(ctx context.Context, userID string, documentIDs []int64, reason string) error {
	if len(documentIDs) == 0 {
		return nil
	}
	_, err := New(s.Pool).MarkNotQueued(ctx, MarkNotQueuedParams{Reason: reason, Ids: documentIDs, UserID: userID})
	return err
}
