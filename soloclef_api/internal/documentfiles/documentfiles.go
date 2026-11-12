// Package documentfiles serves a document's stored files: how the viewer
// should open a document, and the bytes of the file it was uploaded as.
//
//	GET /api/v1/documents/{document_id}/view-manifest                 how to open it
//	GET /api/v1/documents/{document_id}/download-original             the uploaded file, to save
//	GET /api/v1/documents/{document_id}/files/{file_id}/content       one stored file, for a viewer
//
// The third is the address the manifest hands out as `content_url`: the PDF
// viewer fetches the pages it shows from it by byte range.
//
// # What it does better than the Python
//
//   - **The manifest is one statement**, the document and its newest original
//     together, where the Python checked a membership, read the document and
//     then read the file.
//   - **The download honours Range and validators**, sends its length,
//     and decides everything that can fail before the status line, through
//     the code the artifact routes stream with (artifacts.Gate.StreamStored,
//     over objecthttp). The Python download handed a generator to
//     StreamingResponse: no length, no resume, and a missing file was a 200
//     that broke off.
//   - **A long download is not cut at the server's write timeout**: the body
//     is paced by a deadline that moves while the client keeps reading
//     (internal/rollingdeadline).
//   - **A file is read from the backend its row names**, so a file stored on
//     disk before a deployment moved to a bucket still opens.
//   - **The download's name survives a title in any script** (`filename*`),
//     and a row with no media type is sent as bytes to save, never sniffed.
//   - **Every statement names the owner twice**, on the document and on the
//     file, and a document being deleted has no files to send.
package documentfiles

import (
	"net/http"
	"path"
	"strconv"
	"strings"
)

// The paths.
const (
	ManifestPath = "/api/v1/documents/{document_id}/view-manifest"
	DownloadPath = "/api/v1/documents/{document_id}/download-original"
	ContentPath  = "/api/v1/documents/{document_id}/files/{file_id}/content"
)

// Served is every route as a "METHOD /path" pair.
func Served() []string {
	return []string{
		http.MethodGet + " " + ManifestPath,
		http.MethodGet + " " + DownloadPath,
		http.MethodGet + " " + ContentPath,
	}
}

// What a caller is told.
const (
	documentAbsent = "Document not found"
	originalAbsent = "No original file stored for this document"
	fileAbsent     = "Document file not found"
)

// viewedOriginals are the files the web app opens as themselves and not as
// their extracted text, by extension, with the media type its viewers are
// keyed by. A row's stored type is not trusted for this: uploads have
// arrived as `application/octet-stream`, and a PDF is a PDF by its name.
var viewedOriginals = map[string]string{
	".pdf":  "application/pdf",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
}

// viewedType is the viewer's media type for a file of this name and stored
// type, or "" for one that is shown as text.
func viewedType(filename, storedType string) string {
	if byName, viewed := viewedOriginals[strings.ToLower(path.Ext(filename))]; viewed {
		return byName
	}
	for _, viewed := range viewedOriginals {
		if storedType == viewed {
			return viewed
		}
	}
	return ""
}

// manifest is how a document should open, without its text.
type manifest struct {
	DocumentID   int64     `json:"document_id"`
	Title        string    `json:"title"`
	DocumentType string    `json:"document_type"`
	Status       string    `json:"status"`
	Presentation string    `json:"presentation"`
	File         *viewFile `json:"file"`
}

// viewFile is the original file as a viewer fetches it.
type viewFile struct {
	FileID     int64  `json:"file_id"`
	Filename   string `json:"filename"`
	MimeType   string `json:"mime_type"`
	SizeBytes  int64  `json:"size_bytes"`
	ContentURL string `json:"content_url"`
}

// describe decides the presentation:
//
//   - "original": the document has an original the app has a viewer for.
//   - "missing_original": its title says it is such a file (a PDF still being
//     imported, say) and no original is stored yet; the app waits for one.
//   - "text": everything else opens as its text, with the original (if any)
//     offered for download.
func describe(row ManifestOfRow) manifest {
	answer := manifest{
		DocumentID: row.ID, Title: row.Title, DocumentType: row.DocumentType,
		Status: row.State, Presentation: "text",
	}
	if row.FileID == 0 {
		if viewedType(row.Title, "") != "" {
			answer.Presentation = "missing_original"
		}
		return answer
	}
	viewed := viewedType(row.OriginalFilename, row.MimeType)
	mediaType := viewed
	if mediaType == "" {
		mediaType = typeOf(row.MimeType)
	}
	answer.File = &viewFile{
		FileID: row.FileID, Filename: row.OriginalFilename, MimeType: mediaType, SizeBytes: row.SizeBytes,
		ContentURL: "/api/v1/documents/" + strconv.FormatInt(row.ID, 10) + "/files/" + strconv.FormatInt(row.FileID, 10) + "/content",
	}
	if viewed != "" {
		answer.Presentation = "original"
	}
	return answer
}

// typeOf is a row's media type, or plain bytes when the row names none.
func typeOf(stored string) string {
	if strings.TrimSpace(stored) == "" {
		return "application/octet-stream"
	}
	return stored
}
