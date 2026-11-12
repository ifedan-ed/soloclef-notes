package uploads

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folderchain"
)

// FolderFile is one stored file of an uploaded folder and where it goes.
type FolderFile struct {
	Staged
	// RelativePath is the file's path inside the folder, exactly as sent.
	RelativePath string
	// Folders is the chain of folders between the top folder and the file;
	// empty for a file directly in the top folder.
	Folders []string
}

// FolderBatch is one folder request's files and what is the same for all
// of them.
type FolderBatch struct {
	// UserID is the account the folders and documents are made for.
	UserID string
	Parser string
	// Vision is the upload's use_vision_llm (Batch.Vision).
	Vision bool
	// FolderName is the uploaded folder's name: the top folder's name when
	// RootFolderID is nil, and part of every file's identity either way.
	FolderName string
	// RootFolderID is the folder the files go under when the request named
	// one; nil to find or make the top folder by name.
	RootFolderID *int64
	Files        []FolderFile
}

// FolderRecorded is what a folder request became.
type FolderRecorded struct {
	// RootFolderID is the top folder: the one named, found or made.
	RootFolderID int64
	// Files is what each stored file became, in the batch's order.
	Files []Recorded
}

// ErrRootFolderGone: the folder the request named was there when the
// request was admitted and is gone now. Nothing is recorded.
var ErrRootFolderGone = errors.New("uploads: the folder the files were to go under no longer exists")

// FolderRows is the database as a folder upload uses it.
type FolderRows interface {
	identified
	// FolderIn reports whether this folder is the account's. Another
	// account's folder and no folder are the same answer.
	FolderIn(ctx context.Context, folderID int64, userID string) (bool, error)
	// RecordFolder writes the folders and the whole batch in one
	// transaction. On any error nothing is written.
	RecordFolder(ctx context.Context, batch FolderBatch) (FolderRecorded, error)
}

func (p PostgresRows) FolderIn(ctx context.Context, folderID int64, userID string) (bool, error) {
	_, err := New(p.Pool).FolderIn(ctx, FolderInParams{ID: folderID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// RecordFolder is the one transaction of a folder upload: the top folder,
// the folders the paths name, a document per file and each document's file
// row. It commits before any index job is queued.
//
// The Python route committed the top folder, answered, and left the rest
// to a queued task that made the folders and documents one file at a time,
// each in a transaction of its own. A task that died partway left half a
// folder with nothing to say so. Here a request's folders and files are
// recorded together or not at all.
func (p PostgresRows) RecordFolder(ctx context.Context, batch FolderBatch) (FolderRecorded, error) {
	return database.Write(ctx, p.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (FolderRecorded, error) {
		queries := New(tx)
		now := time.Now().UTC()

		var root int64
		if batch.RootFolderID != nil {
			// Held, so it cannot be deleted under the documents about to
			// be placed in it.
			id, err := queries.LockFolderIn(ctx, LockFolderInParams{ID: *batch.RootFolderID, UserID: batch.UserID})
			if errors.Is(err, pgx.ErrNoRows) {
				return FolderRecorded{}, ErrRootFolderGone
			}
			if err != nil {
				return FolderRecorded{}, err
			}
			root = id
		} else {
			// The top-level folder of this name, made if it is not there.
			// The web app sends a folder as several requests at once, all
			// of them without a root the first time; folderchain is what
			// lets them agree on one folder.
			id, _, err := folderchain.EnsureOne(ctx, tx, batch.UserID, nil, batch.FolderName, now)
			if err != nil {
				return FolderRecorded{}, err
			}
			watched, err := json.Marshal(map[string]any{"watched": true, "folder_path": batch.FolderName, "parser": batch.Parser})
			if err != nil {
				return FolderRecorded{}, err
			}
			if err := queries.MarkFolderWatched(ctx, MarkFolderWatchedParams{ID: id, UserID: batch.UserID, Metadata: watched}); err != nil {
				return FolderRecorded{}, err
			}
			root = id
		}

		// Folder chains already resolved in this transaction, by their
		// names: ten files in one folder ask for it once.
		resolved := map[string]*int64{}
		folderFor := func(chain []string) (*int64, error) {
			key := strings.Join(chain, "/")
			if id, known := resolved[key]; known {
				return id, nil
			}
			id, err := folderchain.Ensure(ctx, tx, batch.UserID, &root, chain, now)
			if err == nil {
				resolved[key] = id
			}
			return id, err
		}

		recorded := FolderRecorded{RootFolderID: root, Files: make([]Recorded, len(batch.Files))}
		common := Batch{UserID: batch.UserID, Parser: batch.Parser, Vision: batch.Vision}
		for index, file := range batch.Files {
			folderID, err := folderFor(file.Folders)
			if err != nil {
				return FolderRecorded{}, err
			}
			recorded.Files[index], err = recordFile(ctx, queries, common, file.Staged, placement{
				documentType:   folderFileType,
				folderID:       folderID,
				metadata:       folderFileMetadata(batch.FolderName, file, now),
				touchUnchanged: true,
			})
			if err != nil {
				return FolderRecorded{}, err
			}
		}
		return recorded, nil
	})
}

// folderFileMetadata is a folder file's `document_metadata`.
//
// The first four keys are the ones the document list shows for a folder
// file. `mtime` and `raw_file_hash` are what a folder sync's "which files
// changed" check reads: the time this file was last taken (seconds, as a
// number) and the SHA-256 of its bytes. The last three are the ones every upload carries.
func folderFileMetadata(folderName string, file FolderFile, now time.Time) map[string]any {
	return map[string]any{
		"folder_name":    folderName,
		"file_path":      file.RelativePath,
		"document_type":  "Local Folder File",
		"connector_type": "Local Folder",
		"mtime":          float64(now.UnixMicro()) / 1e6,
		"raw_file_hash":  file.SHA256,
		"FILE_NAME":      file.Name,
		"file_size":      file.Size,
		"upload_time":    now.Format(time.RFC3339),
	}
}
