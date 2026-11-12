package notebookresearch

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// An import whose address is a PDF is not read here. The PDF is stored as
// the document's original file, exactly where and how an upload's is (the
// file store's original key, a document_files ORIGINAL row), and the
// document is handed to the indexing worker, which parses it with the
// chosen parser, cuts it into passages and embeds them: the pipeline every
// uploaded PDF goes through. So a PDF found by its link is searched, cited
// by page, opened in the PDF viewer and downloaded like one uploaded, and
// there is no second way of turning a PDF into text to keep in step.
//
// The document stays the import it was: a CRAWLED_URL with its address,
// found again by the same identity, so the same link added twice is still
// one document.

// Originals is where an imported PDF is kept: the store uploads are
// written to, and the upload's per-file limit, so a PDF that arrives by its
// link is held to exactly what it would be held to uploaded.
type Originals struct {
	Files    filestore.Writer
	MaxBytes int64
}

// check says what is missing.
func (o Originals) check() error {
	switch {
	case o.Files == nil:
		return errors.New("notebookresearch: the file store imported PDFs are kept in is not set")
	case o.MaxBytes < 1:
		return errors.New("notebookresearch: the per-file limit for imported PDFs is not set")
	}
	return nil
}

// storedPDF is a PDF kept as a document's original.
type storedPDF struct {
	backend, key, name string
	size               int64
	sha256             string
}

// store streams the PDF to the file store under the document's original
// key. The store counts and digests it as it writes, so it is never held
// in memory; a body that fails partway leaves nothing stored.
func (o Originals) store(ctx context.Context, userID string, documentID int64, pdf pagereader.PDF) (*storedPDF, error) {
	key, err := filestore.OriginalKey(userID, documentID, pdf.Name)
	if err != nil {
		return nil, err
	}
	stored, err := o.Files.PutStream(ctx, key, pdf.Body, "application/pdf")
	if err != nil {
		return nil, err
	}
	return &storedPDF{backend: o.Files.Name(), key: key, name: pdf.Name, size: stored.Size, sha256: stored.SHA256}, nil
}

// discard removes a stored PDF no row owns. A removal that fails leaves an
// object nothing points at, which costs storage and nothing else; it is
// logged with its key so it can be removed by hand.
func (r *Reads) discard(ctx context.Context, documentID int64, kept *storedPDF) {
	cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer done()
	if err := r.originals.Files.Remove(cleanup, kept.key); err != nil {
		r.logger.ErrorContext(ctx, "an imported PDF that no document owns could not be removed",
			"document_id", documentID, "key", kept.key, "error", err.Error())
	}
}

// recordPDF makes the stored PDF the document's original and hands the
// document to the indexer: both rows in one transaction, so the document
// never waits for a file it does not have. It reports whether the document
// was still waiting for its read; one deleted meanwhile keeps no file.
func (r *Reads) recordPDF(ctx context.Context, documentID int64, userID, title string, learned []byte, kept *storedPDF, now time.Time) (bool, error) {
	recorded, err := database.Write(ctx, r.pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (bool, error) {
		q := New(tx)
		changed, err := q.StorePDF(ctx, StorePDFParams{
			Title: title, Checksum: kept.sha256, Learned: learned, Now: now, ID: documentID, UserID: userID,
		})
		if err != nil || changed == 0 {
			return false, err
		}
		checksum := kept.sha256
		return true, q.InsertOriginal(ctx, InsertOriginalParams{
			DocumentID: documentID, UserID: userID, StorageBackend: kept.backend, StorageKey: kept.key,
			OriginalFilename: kept.name, SizeBytes: kept.size, Checksum: &checksum, Now: now,
		})
	})
	if err != nil || !recorded {
		r.discard(ctx, documentID, kept)
	}
	return recorded, err
}
