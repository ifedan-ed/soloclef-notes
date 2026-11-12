// Package documents serves the reads about documents and the restore of a
// version.
//
// Five answer about one document: the document a cited chunk belongs to
// (ByChunkRoute), a document's chunks a page at a time, its version history,
// one version, and the restore of one.
//
// Four answer about all of the caller's documents (reads.go): the status
// poll, the counts by type, the watched folders and the mention picker's
// title search.
//
// # Whose documents
//
// A document is its owner's or it does not exist. No request names an
// owner: it is the caller (internal/principal), and every statement on
// `documents`, `chunks` and `folders` names it (queries.sql). So another
// account's document, chunk or version is answered exactly as an id that
// was never used, and a document id cannot be probed for.
//
// The other document routes are other packages': the upload and the folder
// upload (internal/uploads), the re-index (internal/documentreindex), the
// delete (internal/documentdeletion) and the moves (internal/folders).
//
// # What is easy to read past
//
//   - Each list is read in an order its statement fixes: the chunks by
//     `position, id`, the versions by a number the table makes unique per
//     document, the statuses by id, the counts by the type.
//   - Five of the nine catch their own failures and four do not, so a 500
//     from the chunk routes, the status poll, the counts and the title
//     search carries the CORS headers, and a 500 from the version routes
//     and the watched folders carries none (failure.go).
//   - The four reads take their query string as starlette_query.go reads
//     it, and the status poll writes a query value back into its 400.
//   - The title search's threshold is compared in double precision, which
//     decides whether a title exactly three tenths alike is found
//     (queries.sql).
//   - `chunk_window` has no upper bound: a window of twenty digits is the
//     whole document, not a failure. The same twenty digits as a `page` are
//     a failure, but only at the statement that carries the offset -- after
//     the 404 for a document the caller does not have.
//   - The restore never overwrites a version: every restore adds exactly
//     one (restore.go says what overwriting lost).
//   - Every restore therefore runs the two clean-ups, and they can delete
//     the version being restored: one past ninety days, or the oldest of
//     twenty. The document still gets its text, which the route read before
//     the snapshot; asking for that version again is a 404.
//   - The restore's enqueue is after the commit: a broker that is down is a
//     500 with the restore already committed.
//
// # Why a citation is read from /chunks
//
// The Python tier's citation path, `/documents/by-chunk/{chunk_id}`,
// overlaps `/documents/{document_id}/chunks` and `…/versions`: Go's ServeMux
// refuses two patterns neither of which is more specific, so it could not
// be mounted at all. A citation names a chunk, so its path is
// `/chunks/{chunk_id}/document` and no pattern overlaps another.
package documents

// The paths.
const (
	ByChunkPath  = "/api/v1/chunks/{chunk_id}/document"
	ChunksPath   = "/api/v1/documents/{document_id}/chunks"
	VersionsPath = "/api/v1/documents/{document_id}/versions"
	VersionPath  = "/api/v1/documents/{document_id}/versions/{version_number}"
	RestorePath  = "/api/v1/documents/{document_id}/versions/{version_number}/restore"
)

// What a caller is told. A document or a chunk that is another account's is
// answered with the same sentence as one that was never made.
const (
	documentAbsent = "Document not found"
	versionAbsent  = "Version not found"
	// The by-chunk route's first 404 names the id it was asked for; see
	// chunkAbsent.
	chunkAbsentPrefix = "Chunk with id "
	chunkAbsentSuffix = " not found"
)

// Query(0, ge=0), Query(20, ge=1, le=100) and Query(5, ge=0): the defaults
// and the bounds FastAPI enforces before either chunk route runs.
const (
	defaultChunkWindow = 5
	defaultPage        = 0
	defaultPageSize    = 20
	smallestPageSize   = 1
	largestPageSize    = 100
)

// pydantic's message for a number above `le`. Its counterpart for `ge` is
// internal/requestbody.AtLeast; there was no route with an upper bound on a
// parameter until this one.
const atMostOneHundred = "Input should be less than or equal to 100"
