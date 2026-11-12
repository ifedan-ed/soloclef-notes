package artifactsave

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// Store writes artifacts over the pool.
type Store struct {
	Pool *pgxpool.Pool
}

// Create writes the rows in one transaction and the file between them. An
// artifact with no file is the document and the artifact rows only.
//
// The file has to be written inside the transaction's span because its key
// contains the artifact's id, which exists only once the artifact row is
// inserted, and the file's row needs the key. So the order is: document,
// artifact, the stored file, the file's row, commit. If anything after the
// file was stored fails, or the commit does, the file is removed again: a
// blob no row points at would be unreachable and never cleaned up. A
// preview is a second file stored and removed the same way.
func (s Store) Create(ctx context.Context, artifact NewArtifact, files filestore.Writer, key func(int64) string) (Created, error) {
	// Every object stored for this artifact, so that all of them are
	// removed if the rows do not commit.
	var storedKeys []string
	var created Created
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		queries := New(tx)
		threadID := artifact.Owner.ThreadID
		documentID, err := insertDocument(ctx, queries, artifact.Owner.UserID, artifact.Title, artifact.Markdown)
		if err != nil {
			return err
		}
		artifactID, err := queries.CreateArtifact(ctx, CreateArtifactParams{
			DocumentID: documentID, UserID: artifact.Owner.UserID, ThreadID: &threadID,
			Format:     artifact.Format,
			ToolCallID: &artifact.ToolCallID, Metadata: artifact.Metadata,
		})
		if err != nil {
			return err
		}
		if err := queries.LinkDocumentToArtifact(ctx, LinkDocumentToArtifactParams{ArtifactID: artifactID, DocumentID: documentID, UserID: artifact.Owner.UserID}); err != nil {
			return err
		}
		created = Created{ArtifactID: artifactID, DocumentID: documentID}
		if !artifact.HasFile() {
			// An artifact that is its document: there is nothing to store
			// and no file row to point at it.
			return nil
		}
		// store puts one file and writes its row.
		store := func(role, storageKey, filename, mimeType string, data []byte) (int64, error) {
			if err := files.Put(ctx, storageKey, data, mimeType); err != nil {
				return 0, err
			}
			storedKeys = append(storedKeys, storageKey)
			sum := sha256.Sum256(data)
			return queries.CreateArtifactFile(ctx, CreateArtifactFileParams{
				ArtifactID: artifactID, Role: ArtifactFileRole(role), StorageBackend: files.Name(), StorageKey: storageKey,
				OriginalFilename: filename, MimeType: mimeType,
				SizeBytes: int64(len(data)), ChecksumSha256: hex.EncodeToString(sum[:]),
			})
		}
		if created.FileID, err = store("primary", key(artifactID), artifact.Filename, artifact.MimeType, artifact.Data); err != nil {
			return err
		}
		if len(artifact.Preview) > 0 {
			if artifact.PreviewKey == nil {
				return errors.New("artifactsave: an artifact with a preview has no key for it")
			}
			if created.PreviewFileID, err = store("preview", artifact.PreviewKey(artifactID), artifact.PreviewFilename, previewMime, artifact.Preview); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if len(storedKeys) > 0 {
			// On a context of its own: the usual reason to be here is that
			// the turn was stopped, and its context is what failed the
			// commit.
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), removeTimeout)
			defer cancel()
			for _, storedKey := range storedKeys {
				_ = files.Remove(cleanup, storedKey)
			}
		}
		return Created{}, err
	}
	return created, nil
}

// removeTimeout bounds the removal of a file whose rows were not committed.
const removeTimeout = 10 * time.Second

// insertDocument writes the artifact's document under the first free name
// in the account's tree.
func insertDocument(ctx context.Context, queries *Queries, userID, title, markdown string) (int64, error) {
	path := ""
	for attempt := 1; attempt <= pathAttempts; attempt++ {
		candidate := "/documents/" + documentName(title, attempt)
		taken, err := queries.DocumentPathTaken(ctx, DocumentPathTakenParams{UserID: userID, Path: candidate})
		if err != nil {
			return 0, err
		}
		if !taken {
			path = candidate
			break
		}
	}
	if path == "" {
		return 0, fmt.Errorf("artifactsave: no free document name for %q after %d attempts", title, pathAttempts)
	}
	contentHash, identifierHash := documentHashes(userID, path, markdown)
	return queries.CreateArtifactDocument(ctx, CreateArtifactDocumentParams{
		Title: title, Content: markdown, ContentHash: contentHash,
		UniqueIdentifierHash: &identifierHash, Path: &path,
		UserID: userID,
	})
}
