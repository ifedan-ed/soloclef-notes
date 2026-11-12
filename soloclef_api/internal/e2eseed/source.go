package e2eseed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// seedSource adds a PDF source to a notebook: the document, its original
// file stored where an upload's is, and one chunk read from page 2, so a
// citation of that chunk can open the original at that page. The PDF is
// the fixture, two pages long; the title is the document's name.
func (r Routes) seedSource(x *exchange) {
	var asked struct {
		NotebookID int64  `json:"notebook_id"`
		Title      string `json:"title"`
	}
	if !x.body(&asked) {
		return
	}
	if asked.Title == "" {
		asked.Title = "E2E guideline"
	}
	ctx := x.ctx.Context()
	text := asked.Title + ": use a spacer with the inhaler."
	data := fixture("source.pdf")
	var answer struct {
		DocumentID int64 `json:"document_id"`
		ChunkID    int64 `json:"chunk_id"`
	}
	err := r.inTransaction(ctx, func(q *Queries, stored *[]string) error {
		userID, err := q.NotebookOwner(ctx, asked.NotebookID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errAbsent("notebook not found")
		}
		if err != nil {
			return err
		}
		hash, err := newID()
		if err != nil {
			return err
		}
		filename := asked.Title + ".pdf"
		if answer.DocumentID, err = q.SeedSourceDocument(ctx, SeedSourceDocumentParams{
			UserID: userID, Title: filename, Content: text, ContentHash: hash}); err != nil {
			return err
		}
		if err := r.storeOriginal(ctx, q, userID, answer.DocumentID, filename, data, stored); err != nil {
			return err
		}
		page := int64(2)
		if answer.ChunkID, err = q.SeedChunk(ctx, SeedChunkParams{
			DocumentID: answer.DocumentID, UserID: userID, Content: text, Page: &page}); err != nil {
			return err
		}
		return q.SeedNotebookSource(ctx, SeedNotebookSourceParams{
			NotebookID: asked.NotebookID, UserID: userID, DocumentID: &answer.DocumentID})
	})
	if x.failed("could not seed the source", err) {
		return
	}
	x.send(http.StatusOK, answer)
}

// storeOriginal stores a document's original under the key an upload's
// has (filestore.OriginalKey) and writes its row.
func (r Routes) storeOriginal(ctx context.Context, q *Queries, userID string, documentID int64, filename string, data []byte, stored *[]string) error {
	key, err := filestore.OriginalKey(userID, documentID, filename)
	if err != nil {
		return err
	}
	if err := r.Files.Put(ctx, key, data, pdfMime); err != nil {
		return err
	}
	*stored = append(*stored, key)
	sum := sha256.Sum256(data)
	checksum, mime := hex.EncodeToString(sum[:]), pdfMime
	return q.SeedOriginalFile(ctx, SeedOriginalFileParams{
		DocumentID: documentID, UserID: userID, StorageBackend: r.Files.Name(), StorageKey: key,
		OriginalFilename: filename, MimeType: &mime, SizeBytes: int64(len(data)), Checksum: &checksum,
	})
}
