// Package uploads serves POST /api/v1/documents/fileupload: files a person
// uploads become documents waiting for the indexing worker. It also serves
// POST /api/v1/documents/folder-upload, the same thing for a whole folder,
// each file placed in the folder its path names (FolderRoute, in
// folder_route.go, which says what differs; everything below is true of
// both).
//
// It is the route behind the upload dialog and a notebook's "add sources"
// (which uploads here and then links the new documents to the notebook),
// and the mobile app's file picker.
//
// # What happens to an upload
//
//  1. The form's fields are read: the parser and the vision switch. They
//     must come before the files, because the body is read once, in order,
//     and nothing of a file may be stored before the request is known to
//     be one this route will carry out.
//
// Whose documents they become is never read from the form: it is the
// signed-in caller (internal/principal). A file's identity and its storage
// key both carry that account's id, so the same file name is one document
// per account and never a shared one.
//  2. Each file is streamed to the file store as it arrives
//     (filestore.Writer.PutStream), which counts and digests it in the same
//     pass. No file is ever held in memory or written to a temporary file:
//     the memory an upload costs is the copy buffer, whatever its size
//     (BenchmarkUpload measures it).
//  3. One transaction writes every document row, pending and naming its
//     parser, and every document_files row. Either the whole request is
//     recorded or none of it is, and in the second case the stored objects
//     are removed.
//  4. After the commit, each document's index job is queued
//     (internal/indexqueue). A document whose job could not be queued is
//     marked failed with a reason.
//
// # Better than the route it replaces
//
// The Python route read every file into memory, wrote it to a temporary
// file, read it back into memory to store it, and treated a storage failure
// as best effort, so a document could be indexed with no original kept. It
// decided "already uploaded" by file name alone, so a corrected file under
// the same name was reported as a duplicate and never read. Here the bytes
// are streamed once; the original is stored before any row exists; and a
// file of the same name is a duplicate only when its bytes are the same
// (decision.go).
package uploads

import (
	"context"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/pictures"
)

// Path is the route, where the web and mobile apps already post.
const Path = "/api/v1/documents/fileupload"

// The form's part names.
const (
	fieldParser = "parser"
	fieldVision = "use_vision_llm"
	fieldFiles  = "files"
)

// What the route says. The sentences a person may see are written for them.
const (
	Uploaded       = "Files uploaded for processing"
	NoFiles        = "No files provided"
	NotMultipart   = "An upload must be sent as multipart/form-data"
	FieldAfterFile = "Every form field must be sent before the first file"
	Interrupted    = "The upload was interrupted before it finished"
	StorageDown    = "The file could not be stored right now. Please try again."
)

// Vision says whether an account may switch use_vision_llm on
// (uploadvision.Vision): it may when it, or the deployment, has a vision
// model the indexer can reach. The refusal is a sentence for the person.
type Vision interface {
	Refusal(ctx context.Context, userID string) (string, error)
}

// admitVision is the vision switch checked once, before any file is read:
// "" when it is off or allowed, else the refusal. Refused, not ignored: the
// caller asked for pictures to be described, and indexing them without a
// model would be a different result from the one asked for.
func admitVision(ctx context.Context, vision Vision, on bool, userID string) (string, error) {
	if !on {
		return "", nil
	}
	return vision.Refusal(ctx, userID)
}

// readAsPicture says a file is read by the vision model and not by the
// chosen parser: a picture, with vision on. The parser's list of what it
// reads does not apply to it.
func readAsPicture(vision bool, name string) bool {
	return vision && pictures.IsPicture(name)
}

// fieldLimit bounds a text field's value. The longest real one is a parser
// name; anything larger is not a field of this form.
const fieldLimit = 256

// cleanupTimeout bounds the removal of stored objects after a request that
// was refused or failed. It runs outside the request's context (a client
// that hung up is exactly when there is something to remove), so it needs a
// limit of its own.
const cleanupTimeout = 30 * time.Second
