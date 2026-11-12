package artifacts

import (
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_filestore/objecthttp"
)

// This file is the seam through which another package sends a stored file
// the way the artifact routes do. A document's original file is streamed by
// internal/documentfiles with the same Range handling, the same rolling
// write deadline and the same account of how a stream ended, because it
// calls the same code: a second copy of stream.go would be a second place
// for the cut-at-sixty-seconds defect to come back.

// StoredObject is one stored file to send and how.
type StoredObject struct {
	// Backend is the store the file's row says holds the bytes.
	Backend string
	Key     string
	Headers objecthttp.Headers
	// Absent is what the caller is told if the bytes are not there.
	Absent string
}

// StreamStored sends one stored object, or the refusal for one that cannot
// be sent. stall is how long the client may accept nothing before it is
// dropped, and must be greater than zero.
func (g Gate) StreamStored(ctx huma.Context, response http.ResponseWriter, requestID string,
	stores *filestore.Stores, stall time.Duration, object StoredObject) {
	requireStallTimeout("StreamStored", stall)
	g.stream(ctx, response, requestID, stores, stall, storedObject{
		backend: object.Backend, key: object.Key, headers: object.Headers, absent: object.Absent,
	})
}

// ContentDisposition is the header that names a sent file (contentDisposition).
func ContentDisposition(filename string, inline bool) string {
	return contentDisposition(filename, inline)
}

// ShownInline reports whether a media type may be displayed from this
// origin rather than saved (inlineTypes, which says why the list is short).
func ShownInline(mediaType string) bool { return inlineTypes[mediaType] }
