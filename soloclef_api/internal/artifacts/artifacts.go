// Package artifacts serves the thirteen routes of
// app/routes/artifacts_routes.py: everything the Studio does with something
// it has made.
//
// An artifact is anything the Studio made -- a flashcard deck, a quiz, a
// video, a podcast episode, a document -- stored as an `artifacts` row beside
// a `documents` row, with its bytes in file storage.
//
// # Whose artifact
//
// An artifact has one owner, the account that made it, and every statement
// here names that owner beside the id (`WHERE a.id = $1 AND a.user_id =
// $2`). That predicate is the whole of the access check: there is no
// membership, role or permission to read first, so a route costs two fewer
// queries than it did, and another account's artifact is the same `404
// Artifact not found` as an id that was never issued.
//
// # The routes, in three groups
//
// Three answer from rows alone: the list, the payload a video is played
// from, and the delete.
//
// Three stream bytes out of the file store (internal/filestore): the
// download, one file's content, and one video slide's narration. All three go
// through internal/filestore/objecthttp, so each has a Content-Length, honours
// Range with a 206, and never holds a file in memory.
//
// Seven read a *study file* -- a deck of flashcards or a quiz, stored as
// JSON -- to learn how many cards or questions it has and which answers are
// right: the manifest, and the six study-state writes (a flashcard mark, the
// reset, the card order; a quiz answer, a skip, a retake). They stayed with
// the Python tier for as long as this tier had no reader for the store.
//
// # What these do better than the routes they replace
//
// The row-only three:
//
//   - One artifact whose status or metadata column holds something unexpected
//     no longer fails the whole list (python_value.go).
//   - A legacy reference whose id is not positive is left out, in the list
//     and the manifest alike: the web app's schema refuses the whole answer
//     over one.
//   - `indexing_status` is always a word; it used to be whatever the column
//     held, null and numbers included.
//   - A slide never carries a server path. `audio_file` used to be sent
//     whenever the slide also had a storage key (video.go).
//   - `slide_count` is the number of slides sent.
//   - The delete removes the artifact itself before it answers: its
//     document's vectors, chunks, stored files and row, with the document
//     delete's own removal. It used to queue a Python task, so 204 meant
//     "queued" and the vectors stayed behind (delete_route.go).
//   - The delete refuses a document that is still being generated or
//     indexed, and claims it only over the status it read. One already
//     marked "deleting" is finished rather than refused.
//   - A 500 keeps its CORS headers, so a browser can read the envelope.
//
// The three that stream (stream.go, and objecthttp for the HTTP itself):
//
//   - A file that is missing from the store is found *before* the status line
//     is written and answered as a 404 in the usual envelope. It used to be a
//     `200 OK` with headers and then a connection cut short.
//   - The download and a slide's audio carry a Content-Length. Neither did.
//   - A slide's audio honours Range. The route advertised
//     `Accept-Ranges: bytes` and then ignored the header, so scrubbing a
//     narration re-downloaded it from the start.
//   - The length sent is the stored object's, not the number in the row.
//   - A Range the server cannot parse, or several at once, is answered with
//     the whole file (as RFC 9110 says) and not with a 416 that stops a
//     player.
//   - If-None-Match is compared as HTTP defines it, so a weakened tag or a
//     list of tags earns its 304.
//   - A download's filename is never just an extension when the title has no
//     ASCII letters, and the UTF-8 name is encoded for the header it is in
//     (content_names.go).
//   - A stream may take as long as its client needs. The server's write
//     timeout bounds a whole response, which cut any stream longer than it
//     mid-body; these three hold a client to *taking bytes* instead, so a
//     slow one finishes and one that stops reading is still dropped
//     (internal/rollingdeadline).
//   - A file off a local disk is sent by the kernel, through every response
//     wrapper, where the store hands over the file in a shape that allows it.
//   - How a stream ended is logged by whose doing it was: a client leaving is
//     information, a client dropped for stalling a warning, a store failing
//     mid-file an error. A request abandoned before its answer is not a
//     failed route and is not answered with a 500 nobody reads.
//
// The seven that read a study file (study_file.go, flashcard_state.go,
// quiz_state.go):
//
//   - The file is read as a stream and only for what the route needs: a
//     count, and for a quiz each right answer. It used to be loaded whole and
//     run through a full validating model -- text lengths, LaTeX delimiters,
//     NFKC-and-casefold duplicate detection -- on every card flip. Those
//     checks decide whether a deck may be *saved*, and it was saved; repeating
//     them on a study write protects nobody and could only turn a rule
//     tightened later into "you can no longer study the deck you made".
//   - A deck or quiz is read once per process and remembered
//     (study_cache.go). Its key names an immutable file, so what it said
//     stays true; the manifest the web app refetches on every focus, and each
//     card flip, no longer re-read and re-parse the file.
//   - A study write changes one key of the row's metadata in the database
//     (`jsonb_set`). The whole column used to be read into Python and written
//     back.
//   - An id too large to be an id is a 422 that says so, not a 500.
//   - The manifest's ETag is taken over the bytes it sends, so nothing in it
//     can change without the tag changing. It used to cover the document's
//     content hash, the generation and the study state -- not the title, the
//     file list or the legacy reference.
//   - A manifest whose deck or quiz file could not be read still carries the
//     person's stored progress. It used to carry the state of somebody who
//     had never started, and the viewer showed the progress as lost
//     (studyFileUnread).
package artifacts

// The paths. An artifact is its owner's and is addressed by its id alone;
// whose it is comes from the signed-in caller, never from the address.
const (
	ListPath   = "/api/v1/artifacts"
	VideoPath  = "/api/v1/artifacts/{artifact_id}/video"
	DeletePath = "/api/v1/artifacts/{artifact_id}"

	ManifestPath    = "/api/v1/artifacts/{artifact_id}/manifest"
	DownloadPath    = "/api/v1/artifacts/{artifact_id}/download"
	FileContentPath = "/api/v1/artifacts/{artifact_id}/files/{file_id}/content"
	SlideAudioPath  = "/api/v1/artifacts/{artifact_id}/slides/{slide_number}/audio"

	// FlashcardProgressPath takes a PATCH (mark one card) and a DELETE
	// (forget every mark).
	FlashcardProgressPath = "/api/v1/artifacts/{artifact_id}/flashcard-progress"
	FlashcardOrderPath    = "/api/v1/artifacts/{artifact_id}/flashcard-order"
	QuizAnswerPath        = "/api/v1/artifacts/{artifact_id}/quiz-answer"
	QuizSkipPath          = "/api/v1/artifacts/{artifact_id}/quiz-skip"
	QuizRetakePath        = "/api/v1/artifacts/{artifact_id}/quiz-retake"
)

// What a caller is told.
const (
	artifactAbsent = "Artifact not found"
	notAVideo      = "Artifact is not a video"
	payloadAbsent  = "Video Remotion payload not available"
	// beingGenerated is the delete's refusal of an artifact whose document
	// is pending or being processed.
	beingGenerated   = "This artifact is still being generated. Delete it once it has finished."
	changedMeanwhile = "The artifact changed while it was being deleted. Please try again."
	privateNoStore   = "private, no-store"
	videoFormat      = "video"
	defaultIndexing  = "ready"
	slideAudioSuffix = "/audio"
)
