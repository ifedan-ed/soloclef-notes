package publicshare

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/artifacts"
	"git.danvics.com/danvics/soloclef/soloclef_filestore/objecthttp"
)

// revalidate is the cache rule of every byte a link serves: a browser may
// keep the file but must ask before using it. The question is cheap (a 304
// against the file's checksum) and is what makes a revoked or expired link
// stop at once; `private` keeps a shared proxy from answering it for
// anybody else.
const revalidate = "private, no-cache"

// A stored file as a link sends it.
type linkFile struct {
	backend, key, filename, mimeType, checksum string
}

// shownInline reports whether a browser may display a shared file from the
// app's own origin when its address is opened directly: pictures of the
// kinds that cannot carry script, a PDF, sound and video. Anything else
// (HTML, SVG, JSON, a workbook) is sent to be saved, and sandboxed, so that
// a shared file can never run as a page of this site. The viewers are not
// affected: they read every file with fetch.
func shownInline(mimeType string) bool {
	return displayFor(mimeType) != "file"
}

// displayFor is how a page shows a file: as one of the kinds a browser may
// show from this origin, or as "file", a download. A link's copy records it
// for the output's main file (output_link.go).
func displayFor(mimeType string) string {
	mime, _, _ := strings.Cut(mimeType, ";")
	mime = strings.ToLower(strings.TrimSpace(mime))
	switch {
	case mime == "image/png", mime == "image/jpeg", mime == "image/gif", mime == "image/webp", mime == "image/avif":
		return "image"
	case mime == "application/pdf":
		return "pdf"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	}
	return "file"
}

// sendFile streams one of a link's files under the public headers.
func (x *exchange) sendFile(file linkFile, absent string) {
	mediaType := file.mimeType
	if strings.TrimSpace(mediaType) == "" {
		// A row that names no type is bytes, to be saved and not read.
		mediaType = "application/octet-stream"
	}
	inline := shownInline(mediaType)
	headers := x.response.Header()
	headers.Set("X-Content-Type-Options", "nosniff")
	// A file that is not shown inline is also sent under
	// `Content-Security-Policy: sandbox`; the stream sets it from the
	// disposition below, for this route and the owner's alike
	// (artifacts.sandboxUnlessInline).
	etag := ""
	if file.checksum != "" {
		etag = `"` + file.checksum + `"`
	}
	x.stream(artifacts.StoredObject{
		Backend: file.backend,
		Key:     file.key,
		Absent:  absent,
		Headers: objecthttp.Headers{
			ContentType:  mediaType,
			ETag:         etag,
			CacheControl: revalidate,
			Disposition:  artifacts.ContentDisposition(file.filename, inline),
		},
	})
}

// content is a shared output's main file: the address apps from before the
// page listed every file still ask for (the page's `file`).
func (r Routes) content(x *exchange) {
	ids, ok := x.ids("artifact_id")
	if !ok {
		return
	}
	row, err := New(r.Pool).SharedPrimaryFile(x.ctx.Context(), SharedPrimaryFileParams{
		ShareToken: x.ctx.Param("share_token"), Now: r.now(), ArtifactID: ids[0]})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, artifactAbsent)
		return
	}
	if err != nil {
		x.fail("could not read a shared artifact's main file", err)
		return
	}
	x.sendFile(linkFile{backend: row.StorageBackend, key: row.StorageKey, filename: row.OriginalFilename,
		mimeType: row.MimeType, checksum: row.ChecksumSha256}, artifactBytesGone)
}

// file is any one file of a shared artifact, by its id: the address the
// page's manifest gives each file, so a shared deck opens its PDF preview
// and flashcards read their JSON, as in the owner's viewer.
func (r Routes) file(x *exchange) {
	ids, ok := x.ids("artifact_id", "file_id")
	if !ok {
		return
	}
	row, err := New(r.Pool).SharedFile(x.ctx.Context(), SharedFileParams{
		ShareToken: x.ctx.Param("share_token"), Now: r.now(), ArtifactID: ids[0], FileID: ids[1]})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, artifactAbsent)
		return
	}
	if err != nil {
		x.fail("could not read a shared artifact's file", err)
		return
	}
	x.sendFile(linkFile{backend: row.StorageBackend, key: row.StorageKey, filename: row.OriginalFilename,
		mimeType: row.MimeType, checksum: row.ChecksumSha256}, artifactBytesGone)
}
