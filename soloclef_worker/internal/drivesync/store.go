package drivesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivefile"
)

// Store is Rows over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

// ownedConnection is the WHERE of every statement on the connection: the
// row by id, the account's own, and a Drive connection.
const ownedConnection = `id = $1 AND user_id = $2::uuid AND connector_type = 'GOOGLE_DRIVE_CONNECTOR'`

func (s Store) Connection(ctx context.Context, userID string, connectorID int64) (*Connection, error) {
	held := Connection{ID: connectorID}
	err := s.Pool.QueryRow(ctx, `SELECT config, enabled FROM search_source_connectors WHERE `+ownedConnection,
		connectorID, userID).Scan(&held.Config, &held.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("drivesync: reading connection %d: %w", connectorID, err)
	}
	return &held, nil
}

// MergeConfig is one statement, so a renewed token and a person's change
// to the same row made at the same moment both land: only the keys in the
// patch are written.
func (s Store) MergeConfig(ctx context.Context, userID string, connectorID int64, patch []byte) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE search_source_connectors SET config = config || $3::jsonb, updated_at = now()
		WHERE `+ownedConnection+` AND jsonb_typeof(config) = 'object'`, connectorID, userID, patch)
	return err
}

func (s Store) DropPageToken(ctx context.Context, userID string, connectorID int64) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE search_source_connectors SET config = config - 'start_page_token', updated_at = now()
		WHERE `+ownedConnection+` AND jsonb_typeof(config) = 'object'`, connectorID, userID)
	return err
}

func (s Store) MarkExpired(ctx context.Context, userID string, connectorID int64) error {
	return s.MergeConfig(ctx, userID, connectorID, []byte(`{"auth_expired": true}`))
}

func (s Store) Synced(ctx context.Context, userID string, connectorID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE search_source_connectors SET last_indexed_at = now() WHERE `+ownedConnection,
		connectorID, userID)
	return err
}

// documentColumns is a Drive document as Document reads it. The facts are
// in the `google_drive` block of its metadata, written when its file was
// last stored (RecordDownloaded).
const documentColumns = `
	id, COALESCE(document_metadata #>> '{google_drive,file_id}', ''), title,
	COALESCE(document_metadata #>> '{google_drive,name}', ''),
	COALESCE(status ->> 'state', 'ready'), COALESCE(status ->> 'stage', ''),
	COALESCE(updated_at, created_at) < now() - make_interval(secs => $2::float8),
	COALESCE(document_metadata #>> '{google_drive,md5_checksum}', ''),
	COALESCE(document_metadata #>> '{google_drive,modified_time}', ''),
	COALESCE(document_metadata #>> '{google_drive,gone_from_drive_at}', '')`

func (s Store) documents(ctx context.Context, where string, userID string, more ...any) ([]Document, error) {
	arguments := append([]any{userID, StaleAfter.Seconds()}, more...)
	rows, err := s.Pool.Query(ctx, `SELECT `+documentColumns+` FROM documents
		WHERE user_id = $1::uuid AND document_type = 'GOOGLE_DRIVE_FILE' AND `+where+` ORDER BY id`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("drivesync: reading the account's Drive documents: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Document, error) {
		var document Document
		err := row.Scan(&document.ID, &document.FileID, &document.Title, &document.Name,
			&document.State, &document.Stage, &document.Stale, &document.MD5, &document.Modified, &document.GoneSince)
		return document, err
	})
}

// Documents finds each file's document by its identity, which is the
// unique index (uq_documents_owner_identity): one probe a file, however
// many documents the account has.
func (s Store) Documents(ctx context.Context, userID string, fileIDs []string) (map[string]Document, error) {
	identities := make([]string, len(fileIDs))
	byIdentity := make(map[string]string, len(fileIDs))
	for index, id := range fileIDs {
		identities[index] = drivefile.Identity(id, userID)
		byIdentity[identities[index]] = id
	}
	rows, err := s.Pool.Query(ctx, `SELECT unique_identifier_hash, `+documentColumns+` FROM documents
		WHERE user_id = $1::uuid AND document_type = 'GOOGLE_DRIVE_FILE' AND unique_identifier_hash = ANY($3)`,
		userID, StaleAfter.Seconds(), identities)
	if err != nil {
		return nil, fmt.Errorf("drivesync: reading documents by Drive file: %w", err)
	}
	found := map[string]Document{}
	var identity string
	var document Document
	_, err = pgx.ForEachRow(rows, []any{&identity, &document.ID, &document.FileID, &document.Title, &document.Name,
		&document.State, &document.Stage, &document.Stale, &document.MD5, &document.Modified, &document.GoneSince}, func() error {
		// The file id is the one the identity was made from, not the one
		// in the metadata, which a person with a database client can edit.
		document.FileID = byIdentity[identity]
		found[document.FileID] = document
		return nil
	})
	return found, err
}

// Imported leaves out a file already found gone from Drive: it is no
// longer synced, so checking everything does not ask Drive about it on
// every pass. The change feed still says if it comes back.
func (s Store) Imported(ctx context.Context, userID string) ([]Document, error) {
	return s.documents(ctx, `document_metadata #>> '{google_drive,file_id}' IS NOT NULL
		AND document_metadata #> '{google_drive,gone_from_drive_at}' IS NULL
		AND COALESCE(status ->> 'state', 'ready') <> 'deleting'`, userID)
}

func (s Store) Abandoned(ctx context.Context, userID string) ([]Document, error) {
	return s.documents(ctx, `document_metadata #>> '{google_drive,file_id}' IS NOT NULL
		AND status ->> 'state' = 'pending' AND status ->> 'stage' = 'downloading'
		AND COALESCE(updated_at, created_at) < now() - make_interval(secs => $2::float8)`, userID)
}

// Requeue leaves the document's text and chunks alone, so it stays
// searchable as it was until the indexer replaces them.
func (s Store) Requeue(ctx context.Context, userID string, documentID int64) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE documents SET status = '{"state": "pending", "stage": "downloading"}'::jsonb, updated_at = now()
		WHERE id = $1 AND user_id = $2::uuid AND document_type = 'GOOGLE_DRIVE_FILE'
		  AND COALESCE(status ->> 'state', 'ready') <> 'deleting'`, documentID, userID)
	return tag.RowsAffected() == 1, err
}

// MarkGone writes the moment into the document's google_drive block,
// where the UI reads it. The first moment is kept: a file binned and then
// reported again stays gone since the first time.
func (s Store) MarkGone(ctx context.Context, userID string, documentID int64, at time.Time) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE documents
		SET document_metadata = jsonb_set(document_metadata, '{google_drive,gone_from_drive_at}', to_jsonb($3::text)),
		    updated_at = now()
		WHERE id = $1 AND user_id = $2::uuid AND document_type = 'GOOGLE_DRIVE_FILE'
		  AND jsonb_typeof(document_metadata -> 'google_drive') = 'object'
		  AND document_metadata #> '{google_drive,gone_from_drive_at}' IS NULL
		  AND COALESCE(status ->> 'state', 'ready') <> 'deleting'`,
		documentID, userID, at.UTC().Format(time.RFC3339))
	return err
}

func (s Store) MarkBack(ctx context.Context, userID string, documentID int64) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE documents
		SET document_metadata = document_metadata #- '{google_drive,gone_from_drive_at}', updated_at = now()
		WHERE id = $1 AND user_id = $2::uuid AND document_type = 'GOOGLE_DRIVE_FILE'
		  AND document_metadata #> '{google_drive,gone_from_drive_at}' IS NOT NULL`, documentID, userID)
	return err
}

// Rename keeps a title the person wrote themselves: the document is
// retitled only if its title was still the file's old name.
func (s Store) Rename(ctx context.Context, userID string, documentID int64, oldName, newName string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE documents
		SET title = CASE WHEN title = $3 THEN $4 ELSE title END,
		    document_metadata = jsonb_set(jsonb_set(COALESCE(document_metadata, '{}'::jsonb),
		        '{google_drive,name}', to_jsonb($4::text)), '{FILE_NAME}', to_jsonb($4::text)),
		    updated_at = now()
		WHERE id = $1 AND user_id = $2::uuid AND document_type = 'GOOGLE_DRIVE_FILE'
		  AND jsonb_typeof(document_metadata -> 'google_drive') = 'object'`, documentID, userID, oldName, newName)
	return err
}

// facts is the `google_drive` block of a document's metadata: which file
// it is and what Drive said of the content that is stored. The next sync
// compares Drive's answer with it.
type facts struct {
	ConnectorID int64  `json:"connector_id"`
	FileID      string `json:"file_id"`
	Name        string `json:"name"`
	MimeType    string `json:"mime_type"`
	MD5         string `json:"md5_checksum"`
	Modified    string `json:"modified_time"`
	SyncedAt    string `json:"synced_at"`
}

// RecordDownloaded is one transaction: the document is handed on to the
// indexer and its file row written, or neither. It is written only while
// the document is still waiting for this download, so one deleted, or
// fetched by another job meanwhile, is not touched.
func (s Store) RecordDownloaded(ctx context.Context, file Downloaded) (bool, error) {
	metadata, err := json.Marshal(map[string]any{
		"FILE_NAME": file.File.Name, "file_size": file.Size, "source": "google_drive",
		"google_drive": facts{
			ConnectorID: file.ConnectorID, FileID: file.File.ID, Name: file.File.Name, MimeType: file.File.MimeType,
			MD5: file.File.MD5, Modified: file.File.Modified, SyncedAt: file.At.UTC().Format("2006-01-02T15:04:05Z07:00"),
		},
	})
	if err != nil {
		return false, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// The title follows the file's name unless the person retitled the
	// document here: it is replaced only while it is still the name the
	// file had (or the document has never had a file).
	tag, err := tx.Exec(ctx, `
		UPDATE documents
		SET status = '{"state": "pending"}'::jsonb, content_hash = $3,
		    title = CASE WHEN title = COALESCE(document_metadata #>> '{google_drive,name}', title) THEN $4 ELSE title END,
		    document_metadata = COALESCE(document_metadata, '{}'::jsonb) || $5::jsonb,
		    connector_id = (SELECT id FROM search_source_connectors WHERE id = $6 AND user_id = $2::uuid),
		    updated_at = now()
		WHERE id = $1 AND user_id = $2::uuid AND document_type = 'GOOGLE_DRIVE_FILE'
		  AND status ->> 'state' = 'pending' AND status ->> 'stage' = 'downloading'`,
		file.DocumentID, file.UserID, file.SHA256, file.File.Name, metadata, file.ConnectorID)
	if err != nil {
		return false, fmt.Errorf("drivesync: handing document %d to the indexer: %w", file.DocumentID, err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	var mime *string
	if file.ContentType != "" {
		mime = &file.ContentType
	}
	// A new row, not an edit of the last: the indexer reads a document's
	// newest ORIGINAL, and a reader streaming the old one keeps its file.
	if _, err := tx.Exec(ctx, `
		INSERT INTO document_files (document_id, user_id, kind, storage_backend, storage_key,
		                            original_filename, mime_type, size_bytes, checksum_sha256)
		VALUES ($1, $2::uuid, 'ORIGINAL', $3, $4, $5, $6, $7, $8)`,
		file.DocumentID, file.UserID, file.Backend, file.Key, file.Name, mime, file.Size, file.SHA256); err != nil {
		return false, fmt.Errorf("drivesync: recording the file of document %d: %w", file.DocumentID, err)
	}
	return true, tx.Commit(ctx)
}

// MarkFailed marks only a document still waiting for this download:
// anything newer is the truth and this failure is not.
func (s Store) MarkFailed(ctx context.Context, userID string, documentID int64, reason string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE documents SET status = jsonb_build_object('state', 'failed', 'reason', left($3::text, 500)), updated_at = now()
		WHERE id = $1 AND user_id = $2::uuid
		  AND status ->> 'state' = 'pending' AND status ->> 'stage' = 'downloading'`, documentID, userID, reason)
	return err
}

// MarkQueueingFailed marks only a document still waiting for the indexer
// with no stage: one whose file this job stored and could not announce.
func (s Store) MarkQueueingFailed(ctx context.Context, userID string, documentID int64, reason string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE documents SET status = jsonb_build_object('state', 'failed', 'reason', left($3::text, 500)), updated_at = now()
		WHERE id = $1 AND user_id = $2::uuid
		  AND status ->> 'state' = 'pending' AND status ->> 'stage' IS NULL`, documentID, userID, reason)
	return err
}
