package notebooklinks

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/artifacts"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentfiles"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/rollingdeadline"
	"git.danvics.com/danvics/soloclef/soloclef_filestore/objecthttp"
)

// The cache rules for a visitor's files.
const (
	// A viewer's file may be kept but must be revalidated on every use:
	// the revalidation runs resolve again, so a stopped link stops serving
	// what a browser already holds. (The owner's own file routes cache for
	// a year; a visitor's right to the bytes can end, the owner's cannot.)
	revalidate = "private, no-cache"
	// A download is fetched because somebody pressed a button.
	noStore = "private, no-store"
)

// storedFile is one file row as the byte routes send it.
type storedFile struct {
	backend, key, filename, mediaType, checksum string
}

// stream sends a stored file to a visitor, with Range, validators and a
// write deadline that moves while they keep reading (artifacts.StreamStored,
// over objecthttp and rollingdeadline).
//
// A shared file is somebody's content served from this site's origin.
// Shown in the page, only a PDF or an MP4 is (artifacts.ShownInline: their
// built-in viewers run nothing); everything else, and every download, is an
// attachment under a sandbox, so that opening its address directly cannot
// run a script with this site's cookies.
func (x *exchange) stream(file storedFile, cacheControl string, viewed bool) {
	inline := viewed && artifacts.ShownInline(file.mediaType)
	if !inline {
		x.response.Header().Set("Content-Security-Policy", "sandbox")
	}
	tag := ""
	if file.checksum != "" {
		tag = `"` + file.checksum + `"`
	}
	artifacts.Gate{Errors: x.routes.Errors, Logger: x.routes.Logger}.StreamStored(
		x.ctx, x.response, x.requestID, x.routes.Files, x.routes.StallTimeout,
		artifacts.StoredObject{
			Backend: file.backend, Key: file.key, Absent: gone,
			Headers: objecthttp.Headers{
				ContentType:  file.mediaType,
				ETag:         tag,
				CacheControl: cacheControl,
				Disposition:  artifacts.ContentDisposition(file.filename, inline),
			},
		})
}

// artifactFile sends one file of a shared artifact to a viewer.
func (r Routes) artifactFile(x *exchange, link shared) {
	_, files, ok := x.artifact(link)
	if !ok {
		return
	}
	fileID := x.visitorID("file_id")
	for _, file := range files {
		if file.ID == fileID {
			x.stream(storedFile{file.StorageBackend, file.StorageKey, file.OriginalFilename,
				typeOr(file.MimeType), file.ChecksumSha256}, revalidate, true)
			return
		}
	}
	x.refuse(http.StatusNotFound, gone)
}

// artifactSave sends a shared output to save, named after its title: its
// primary file, or, for an output that is only text, its Markdown.
func (r Routes) artifactSave(x *exchange, link shared) {
	row, files, ok := x.artifact(link)
	if !ok {
		return
	}
	// The primary is first when there is one (ArtifactFiles' order).
	if len(files) > 0 && files[0].Role == "primary" {
		primary := files[0]
		x.stream(storedFile{primary.StorageBackend, primary.StorageKey,
			artifacts.DownloadName(row.Title, primary.OriginalFilename),
			typeOr(primary.MimeType), primary.ChecksumSha256}, noStore, false)
		return
	}
	body := []byte(row.Markdown)
	// The same moving deadline a file gets: a long report to a slow reader
	// is not cut at the server's write timeout.
	steady, err := rollingdeadline.Wrap(x.response, x.routes.StallTimeout)
	if err != nil {
		x.fail("a download's write deadline cannot be moved", err)
		return
	}
	headers := steady.Header()
	headers.Set("Content-Type", "text/markdown; charset=utf-8")
	headers.Set("Content-Length", strconv.Itoa(len(body)))
	headers.Set("Content-Disposition", artifacts.ContentDisposition(artifacts.MarkdownName(row.Title), false))
	headers.Set("Cache-Control", noStore)
	headers.Set("Content-Security-Policy", "sandbox")
	steady.WriteHeader(http.StatusOK)
	_, _ = steady.Write(body)
}

// sourceFile sends a shared source's original file to a viewer.
func (r Routes) sourceFile(x *exchange, link shared) {
	documentID, fileID := x.visitorID("document_id"), x.visitorID("file_id")
	if documentID == 0 || fileID == 0 {
		x.refuse(http.StatusNotFound, gone)
		return
	}
	// The document must be one the link lists, by the list's own statement.
	documents, err := x.sources(link, documentID)
	if err != nil {
		x.fail("could not look for a shared source", err)
		return
	}
	if len(documents) == 0 {
		x.refuse(http.StatusNotFound, gone)
		return
	}
	file, err := New(r.Pool).SourceFile(x.ctx.Context(), SourceFileParams{FileID: fileID, DocumentID: documentID, UserID: x.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, gone)
		return
	}
	if err != nil {
		x.fail("could not read a shared source's file", err)
		return
	}
	storedType, checksum := "", ""
	if file.MimeType != nil {
		storedType = *file.MimeType
	}
	if file.ChecksumSha256 != nil {
		checksum = *file.ChecksumSha256
	}
	// A PDF stored as plain bytes is sent as the PDF its name says it is,
	// as the owner's viewer is sent it.
	mediaType := documentfiles.ViewedType(file.OriginalFilename, storedType)
	if mediaType == "" {
		mediaType = typeOr(storedType)
	}
	x.stream(storedFile{file.StorageBackend, file.StorageKey, file.OriginalFilename, mediaType, checksum}, revalidate, true)
}
