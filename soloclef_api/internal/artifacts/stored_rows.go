package artifacts

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// This file is the rows the ten file-reading routes ask for, as the routes
// see them, and Store's methods that fetch them. The routes depend on the
// small interfaces declared beside each of them, not on Store, so their tests
// run against a stand-in and a real directory of files.

// StoredFile is one `artifact_files` row: a blob's description and where its
// bytes are.
type StoredFile struct {
	ID int64
	// Role is "primary" (the artifact itself) or "preview" (a rendering of
	// it, such as the PDF of a slide deck).
	Role     string
	Filename string
	MimeType string
	// Size is the length recorded when the file was saved. It is what the
	// manifest reports; a route that streams the file sends the store's own
	// figure instead (filestore.Info).
	Size int64
	// Backend and Key say where the bytes are (internal/filestore).
	Backend string
	Key     string
	// Checksum is the SHA-256 the file was saved under, in hex. A file is
	// never rewritten in place, so it is also the file's HTTP validator.
	Checksum string
}

// Download is what the download route reads.
type Download struct {
	// Title is the document's, and what the downloaded file is named after.
	Title string
	// File is the artifact's primary file, nil when it has none.
	File *StoredFile
	// Markdown is the document as Markdown, read only when File is nil.
	Markdown string
}

// SlideSet is a video artifact's slides as stored, for the audio route.
type SlideSet struct {
	Format string
	// Slides is `metadata.slides` as the column's text, nil when absent.
	Slides []byte
}

// ManifestRow is the artifact as the manifest describes it.
type ManifestRow struct {
	ID         int64
	DocumentID int64
	Title      string
	Format     string
	Generation int64
	UpdatedAt  time.Time
	Markdown   string
	// Legacy is `metadata.legacy`; FlashcardState and QuizState are the
	// caller's own stored state for each format. All three are the column's
	// text and nil when there is none.
	Legacy         []byte
	FlashcardState []byte
	QuizState      []byte
	// Files is every file of the artifact, the primary first.
	Files []StoredFile
}

// StudySource is what a study-state write reads first: what the artifact
// is, and where its deck or quiz is stored.
type StudySource struct {
	Format     string
	Generation int64
	// File is the primary file, nil for an artifact that has none.
	File *StoredFile
}

// LockedStudy is the row a study-state write holds locked: its format and
// generation as they are *now*, and one section's stored states.
type LockedStudy struct {
	Format     string
	Generation int64
	// States is the JSON object mapping user id to state, nil when the
	// section is absent.
	States []byte
}

// StudyWrite is what the route decided under the lock.
type StudyWrite struct {
	// Abandon leaves the row as it is: the route found, under the lock, a
	// reason to refuse.
	Abandon bool
	// States replaces the section's user-to-state object. Nil removes the
	// section from the row, which is what is left when nobody has any state.
	States []byte
}

// ManifestOf reads the artifact and its files. Nil when the caller has no
// such artifact.
func (s Store) ManifestOf(ctx context.Context, userID string, artifactID int64) (*ManifestRow, error) {
	row, err := New(s.Pool).ManifestOfArtifact(ctx, ManifestOfArtifactParams{
		ID: artifactID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files, err := New(s.Pool).FilesOfArtifact(ctx, FilesOfArtifactParams{ArtifactID: artifactID, UserID: userID})
	if err != nil {
		return nil, err
	}
	manifest := &ManifestRow{
		ID: row.ID, DocumentID: row.DocumentID, Title: row.Title, Format: row.Format,
		Generation: row.Generation, UpdatedAt: row.UpdatedAt, Markdown: row.Markdown,
		Legacy: row.Legacy, FlashcardState: row.FlashcardState, QuizState: row.QuizState,
		Files: make([]StoredFile, 0, len(files)),
	}
	for _, file := range files {
		manifest.Files = append(manifest.Files, StoredFile{
			ID: file.ID, Role: file.Role, Filename: file.OriginalFilename, MimeType: file.MimeType,
			Size: file.SizeBytes, Backend: file.StorageBackend, Key: file.StorageKey,
			Checksum: file.ChecksumSha256,
		})
	}
	return manifest, nil
}

// DownloadOf reads what the download sends. Nil when the caller has no
// such artifact.
func (s Store) DownloadOf(ctx context.Context, userID string, artifactID int64) (*Download, error) {
	row, err := New(s.Pool).DownloadOfArtifact(ctx, DownloadOfArtifactParams{
		ID: artifactID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	download := &Download{Title: row.Title, Markdown: row.Markdown}
	// The file's columns come from a LEFT JOIN and are all present or all
	// absent together; the id decides which.
	if row.FileID != nil {
		download.File = &StoredFile{
			ID: *row.FileID, Role: primaryRole,
			Filename: text(row.OriginalFilename), MimeType: text(row.MimeType),
			Backend: text(row.StorageBackend), Key: text(row.StorageKey),
			Checksum: text(row.ChecksumSha256),
		}
	}
	return download, nil
}

// FileIn reads one file of one artifact of the caller. Nil when the file is
// not that artifact's, or the artifact not the caller's.
func (s Store) FileIn(ctx context.Context, userID string, artifactID, fileID int64) (*StoredFile, error) {
	row, err := New(s.Pool).FileOfArtifactOfOwner(ctx, FileOfArtifactOfOwnerParams{
		FileID: fileID, ArtifactID: artifactID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &StoredFile{
		ID: row.ID, Role: row.Role, Filename: row.OriginalFilename, MimeType: row.MimeType,
		Size: row.SizeBytes, Backend: row.StorageBackend, Key: row.StorageKey,
		Checksum: row.ChecksumSha256,
	}, nil
}

// SlidesOf reads a video's slides. Nil when the caller has no such
// artifact.
func (s Store) SlidesOf(ctx context.Context, userID string, artifactID int64) (*SlideSet, error) {
	row, err := New(s.Pool).SlidesOfArtifact(ctx, SlidesOfArtifactParams{
		ID: artifactID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &SlideSet{Format: row.Format, Slides: row.Slides}, nil
}

// StudySourceOf reads what a study write needs before it locks. Nil when the
// caller has no such artifact.
func (s Store) StudySourceOf(ctx context.Context, userID string, artifactID int64) (*StudySource, error) {
	row, err := New(s.Pool).StudySourceOfArtifact(ctx, StudySourceOfArtifactParams{
		ID: artifactID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	source := &StudySource{Format: row.Format, Generation: row.Generation}
	if row.FileID != nil {
		source.File = &StoredFile{
			ID: *row.FileID, Role: primaryRole,
			Backend: text(row.StorageBackend), Key: text(row.StorageKey),
		}
	}
	return source, nil
}

// ChangeStudy is one study-state write: lock the artifact's row, show the
// route what is there, and store what the route decides -- all in one
// transaction, so the decision and the write see the same row.
//
// `decide` is handed nil when the artifact is gone (deleted between the
// route's first read and the lock). Whatever it returns for nil is not
// written; there is no row to write to.
//
// The lock is held only across the decision, which is arithmetic on a few
// hundred numbers. The slow part of a study write -- reading the deck out of
// the file store -- is done by the route *before* this is called, because a
// row lock held across file I/O would make everyone else studying that deck
// wait on a disk.
func (s Store) ChangeStudy(
	ctx context.Context,
	userID string,
	artifactID int64,
	section, holder string,
	decide func(locked *LockedStudy) StudyWrite,
) error {
	_, err := database.Write(ctx, s.Pool,
		func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
			queries := New(tx)
			row, err := queries.LockStudyStates(ctx, LockStudyStatesParams{
				Section: section, Holder: holder, ID: artifactID, UserID: userID,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				decide(nil)
				return struct{}{}, nil
			}
			if err != nil {
				return struct{}{}, err
			}
			write := decide(&LockedStudy{Format: row.Format, Generation: row.Generation, States: row.States})
			switch {
			case write.Abandon:
				return struct{}{}, nil
			case write.States == nil:
				return struct{}{}, queries.ClearStudyStates(ctx, ClearStudyStatesParams{
					Section: section, ID: artifactID, UserID: userID,
				})
			default:
				return struct{}{}, queries.StoreStudyStates(ctx, StoreStudyStatesParams{
					Section: section, Holder: holder, States: write.States, ID: artifactID, UserID: userID,
				})
			}
		})
	return err
}

// primaryRole is the role of the file that *is* the artifact.
const primaryRole = "primary"

// text is a column of a LEFT JOIN's right-hand side, read once the join is
// known to have matched.
func text(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
