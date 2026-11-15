package mailattachments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/maildrafts"
)

// storedFile is a file in the file store, as its row describes it.
type storedFile struct {
	kind      string
	id        int64
	backend   string
	key       string
	name      string
	mediaType *string
	size      int64
	checksum  *string
	title     string
}

// notAttachable is the answer for a document with no file of its own.
func notAttachable(documentID int64) error {
	return refused(fmt.Sprintf("document_id=%d is not one of the user's documents that has a file: a note, a saved web page or a video's transcript is text only and cannot be attached (put its text in the message instead)", documentID))
}

// document is a document's file: a Studio output's own file, else the
// upload it was made from.
func (f *Files) document(ctx context.Context, ownerID string, documentID, mostFile int64) (maildrafts.Attachment, error) {
	queries := New(f.Pool)
	output, err := queries.ArtifactFileOfDocument(ctx, ArtifactFileOfDocumentParams{DocumentID: documentID, UserID: ownerID})
	switch {
	case err == nil:
		mime := output.MimeType
		return f.describe(ctx, storedFile{kind: maildrafts.SourceArtifact, id: output.ID, backend: output.StorageBackend,
			key: output.StorageKey, name: output.OriginalFilename, mediaType: &mime, size: output.SizeBytes,
			checksum: &output.ChecksumSha256, title: output.Title}, mostFile)
	case !errors.Is(err, pgx.ErrNoRows):
		return maildrafts.Attachment{}, err
	}
	upload, err := queries.OriginalFileOfDocument(ctx, OriginalFileOfDocumentParams{DocumentID: documentID, UserID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return maildrafts.Attachment{}, notAttachable(documentID)
	}
	if err != nil {
		return maildrafts.Attachment{}, err
	}
	return f.describe(ctx, storedFile{kind: maildrafts.SourceDocument, id: upload.ID, backend: upload.StorageBackend,
		key: upload.StorageKey, name: upload.OriginalFilename, mediaType: upload.MimeType, size: upload.SizeBytes,
		checksum: upload.ChecksumSha256, title: upload.Title}, mostFile)
}

// artifact is a Studio output's file, by the artifact's id.
func (f *Files) artifact(ctx context.Context, ownerID string, artifactID, mostFile int64) (maildrafts.Attachment, error) {
	output, err := New(f.Pool).ArtifactFileOfArtifact(ctx, ArtifactFileOfArtifactParams{ArtifactID: artifactID, UserID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return maildrafts.Attachment{}, refused(fmt.Sprintf("artifact_id=%d is not one of the user's Studio outputs with a file", artifactID))
	}
	if err != nil {
		return maildrafts.Attachment{}, err
	}
	mime := output.MimeType
	return f.describe(ctx, storedFile{kind: maildrafts.SourceArtifact, id: output.ID, backend: output.StorageBackend,
		key: output.StorageKey, name: output.OriginalFilename, mediaType: &mime, size: output.SizeBytes,
		checksum: &output.ChecksumSha256, title: output.Title}, mostFile)
}

// titled is the one document a title names. None, or more than one, is
// said with what to do: the model knows titles from the search results it
// read, and ids only when it is shown them, here.
func (f *Files) titled(ctx context.Context, ownerID, title string, mostFile int64) (maildrafts.Attachment, error) {
	found, err := New(f.Pool).DocumentsTitled(ctx, DocumentsTitledParams{UserID: ownerID, Title: title})
	if err != nil {
		return maildrafts.Attachment{}, err
	}
	switch len(found) {
	case 0:
		return maildrafts.Attachment{}, refused(fmt.Sprintf("no document of the user is titled %q; use the exact title a search result showed, or a document_id", title))
	case 1:
		return f.document(ctx, ownerID, found[0].ID, mostFile)
	}
	labels := make([]string, 0, len(found))
	for _, document := range found {
		labels = append(labels, documentLabel(document.ID, document.Title)+" (added "+document.CreatedAt.UTC().Format("2006-01-02")+")")
	}
	return maildrafts.Attachment{}, refused(fmt.Sprintf("several of the user's documents are titled %q: %s. Ask the user which one if it is not clear, then attach it by document_id",
		title, strings.Join(labels, "; ")))
}

// describe is a stored file as the draft carries it. The digest is the one
// its row recorded when the file was written, when it recorded one; else
// the bytes are read once to take it. Either way the bytes sent are
// checked against it.
func (f *Files) describe(ctx context.Context, file storedFile, mostFile int64) (maildrafts.Attachment, error) {
	name := fileName(file.name)
	if name == "" {
		name = fileName(file.title)
	}
	if err := within(name, file.size, mostFile); err != nil {
		return maildrafts.Attachment{}, err
	}
	attachment := maildrafts.Attachment{Name: name, Type: mediaType(file.mediaType), Size: file.size,
		Source: maildrafts.AttachmentSource{Kind: file.kind, FileID: file.id}}
	if file.kind == maildrafts.SourceArtifact {
		attachment.Origin = "Studio output: " + file.title
	} else {
		attachment.Origin = "Document: " + file.title
	}
	if file.checksum != nil && isDigest(*file.checksum) {
		attachment.SHA256 = strings.ToLower(*file.checksum)
		return attachment, nil
	}
	store, err := f.Stores.Named(file.backend)
	if err != nil {
		return maildrafts.Attachment{}, err
	}
	stream, err := store.Open(ctx, file.key)
	if err != nil {
		return maildrafts.Attachment{}, fmt.Errorf("mailattachments: opening %s for its digest: %w", file.key, err)
	}
	defer stream.Close()
	digest := sha256.New()
	read, err := io.Copy(digest, stream)
	if err != nil {
		return maildrafts.Attachment{}, err
	}
	// The bytes, not the row, are what is sent and what is checked.
	attachment.Size, attachment.SHA256 = read, hex.EncodeToString(digest.Sum(nil))
	return attachment, within(name, read, mostFile)
}

// isDigest reports a hex SHA-256, either case.
func isDigest(text string) bool {
	if len(text) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(text)
	return err == nil
}

// storedRow is a stored file's row as Open and Changed read it.
type storedRow struct {
	backend, key string
	size         int64
	checksum     *string
}

// row is the row behind a draft's stored file, read as the owner;
// pgx.ErrNoRows when it is no longer there or not the owner's.
func (f *Files) row(ctx context.Context, ownerID string, source maildrafts.AttachmentSource) (storedRow, error) {
	queries := New(f.Pool)
	switch source.Kind {
	case maildrafts.SourceArtifact:
		row, err := queries.ArtifactFileByID(ctx, ArtifactFileByIDParams{ID: source.FileID, UserID: ownerID})
		return storedRow{backend: row.StorageBackend, key: row.StorageKey, size: row.SizeBytes, checksum: &row.ChecksumSha256}, err
	case maildrafts.SourceDocument:
		row, err := queries.DocumentFileByID(ctx, DocumentFileByIDParams{ID: source.FileID, UserID: ownerID})
		return storedRow{backend: row.StorageBackend, key: row.StorageKey, size: row.SizeBytes, checksum: row.ChecksumSha256}, err
	}
	return storedRow{}, fmt.Errorf("mailattachments: %q is not a stored file's source", source.Kind)
}
