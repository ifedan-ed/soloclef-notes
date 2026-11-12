package deliverables

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// savedRows reads a saved artifact back for the database tests, as its
// owner: the columns the page's manifest and download are built from.
//
// The routes that serve an artifact are the API tier's
// (soloclef_api/internal/artifacts) and cannot be imported here. That
// tier's own database test saves through this package and reads through
// those routes' reader; these tests hold the rows themselves to what was
// asked for.
type savedRows struct{ Pool *pgxpool.Pool }

type savedFile struct {
	ID                                               int64
	Role, Filename, MimeType, Backend, Key, Checksum string
	Size                                             int64
}

type savedManifest struct {
	Format, Title, Markdown string
	Generation              int64
	Files                   []savedFile
}

type savedDownload struct {
	Title, Markdown string
	File            *savedFile
}

const filesOfArtifact = `SELECT f.id, f.role::text, f.original_filename, f.mime_type, f.storage_backend, f.storage_key, f.checksum_sha256, f.size_bytes
	FROM artifact_files f JOIN artifacts a ON f.artifact_id = a.id
	WHERE f.artifact_id = $1 AND a.user_id = $2
	ORDER BY (f.role <> 'primary'), f.id`

// ManifestOf is the artifact and its files, the primary first; nil when the
// account has no such artifact.
func (s savedRows) ManifestOf(ctx context.Context, userID string, artifactID int64) (*savedManifest, error) {
	manifest := &savedManifest{Files: []savedFile{}}
	err := s.Pool.QueryRow(ctx, `SELECT a.format, d.title, a.generation, COALESCE(NULLIF(d.source_markdown, ''), d.content)
		FROM artifacts a JOIN documents d ON a.document_id = d.id
		WHERE a.id = $1 AND a.user_id = $2`, artifactID, userID).
		Scan(&manifest.Format, &manifest.Title, &manifest.Generation, &manifest.Markdown)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, filesOfArtifact, artifactID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var file savedFile
		if err := rows.Scan(&file.ID, &file.Role, &file.Filename, &file.MimeType, &file.Backend, &file.Key, &file.Checksum, &file.Size); err != nil {
			return nil, err
		}
		manifest.Files = append(manifest.Files, file)
	}
	return manifest, rows.Err()
}

// DownloadOf is what the download button sends: the primary file, or the
// document's Markdown for an artifact that has none.
func (s savedRows) DownloadOf(ctx context.Context, userID string, artifactID int64) (*savedDownload, error) {
	manifest, err := s.ManifestOf(ctx, userID, artifactID)
	if err != nil || manifest == nil {
		return nil, err
	}
	download := &savedDownload{Title: manifest.Title}
	if len(manifest.Files) > 0 && manifest.Files[0].Role == "primary" {
		download.File = &manifest.Files[0]
		return download, nil
	}
	download.Markdown = manifest.Markdown
	return download, nil
}
