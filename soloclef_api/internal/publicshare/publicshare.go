// Package publicshare is a Studio output's public link: making one, and
// everything a stranger holding one may read.
//
//	GET  /api/v1/public/{share_token}                                                 the page: the output as shared
//	GET  /api/v1/public/{share_token}/artifacts/{artifact_id}/content                 the output's main file
//	GET  /api/v1/public/{share_token}/artifacts/{artifact_id}/files/{file_id}/content any one of its files
//	POST /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/public-link       share a finished output (its owner)
//
// The list of an account's links and the revoke are internal/publiclinks'.
//
// # Only outputs
//
// The owner's decision (2026-11-13): "only artifacts should be shareable".
// A link reaches one Studio output (a mind map, a report, a deck,
// flashcards, a quiz, audio, a video, an infographic, a data table) and
// nothing around it: not the chat it was made in, not the notebook, not the
// sources. Chats and notebooks had links of their own until then; migration
// 00019 revoked them and dropped their tables.
//
// # Who may ask
//
// The three GETs take no credential: the share token in the path is the
// whole of the authorisation, so cmd/api mounts them outside the gate
// (Public). Each reads through statements that start at the token, and the
// file statements refuse a link that has expired, an artifact the link's
// own list (`artifact_ids`) does not name, and an artifact that is not the
// link owner's. A revoked link has no row. All of those, and an id that
// never existed, are one 404 with one sentence, so a stranger trying numbers
// learns nothing from the answers. The page alone tells an expired link
// (410) from one that is gone (404), because its web page says which.
//
// The link create is its owner's: the output is read by its id, its
// notebook's id and the caller's id together.
//
// # What the page sends
//
// Exactly the frozen copy (the output's title, kind, format and text) and
// the artifact's files as they are now, at this link's own addresses. Never
// the notebook's id, the output's id, the account, a storage key or a
// checksum: the page's statements select none of them (page.go).
//
// # Kept from earlier designs
//
//   - A shared file belongs to the account that shared it: every statement
//     requires the artifact to be the link owner's, so a list that named
//     somebody else's artifact reaches nothing.
//   - A revoked or expired link stops serving at once. The file routes
//     answer `private, no-cache` with the file's checksum as the validator:
//     a repeat view is a 304, and it asks every time.
//   - Every stream honours Range, with a length, through objecthttp, and is
//     held to a stall limit rather than a whole-response timeout.
//   - Sharing an output twice at the same moment gives one link: the
//     output's row is locked while its link is looked for and written.
package publicshare

import "net/http"

// The paths.
const (
	PagePath       = "/api/v1/public/{share_token}"
	ContentPath    = "/api/v1/public/{share_token}/artifacts/{artifact_id}/content"
	FilePath       = "/api/v1/public/{share_token}/artifacts/{artifact_id}/files/{file_id}/content"
	OutputLinkPath = "/api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/public-link"
)

// Public is the routes that take no credential, as "METHOD /path" pairs:
// what cmd/api mounts outside the gate.
func Public() []string {
	return []string{
		http.MethodGet + " " + PagePath,
		http.MethodGet + " " + ContentPath,
		http.MethodGet + " " + FilePath,
	}
}

// Served is every route as a "METHOD /path" pair.
func Served() []string {
	return append(Public(), http.MethodPost+" "+OutputLinkPath)
}

// What a caller is told. The public routes have one sentence each for
// everything that is not there, whatever the reason.
const (
	pageAbsent        = "Not found"
	pageExpired       = "This link has expired"
	artifactAbsent    = "Artifact not found"
	artifactBytesGone = "Artifact is no longer available"
	outputAbsent      = "Output not found"
	outputUnfinished  = "Only a finished output can be shared"
	badExpiry         = "expires_in_days must be 7, 30 or null"
)
