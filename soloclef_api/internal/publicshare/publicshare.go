// Package publicshare serves what a public share link (a row of
// public_chat_snapshots) reaches beyond its page: the bytes of the files it
// shares, copying a shared chat into the caller's own account, and making the
// link to a Studio output in the first place.
//
//	GET  /api/v1/public/{share_token}/artifacts/{artifact_id}/content                         an artifact's main file
//	GET  /api/v1/public/{share_token}/artifacts/{artifact_id}/files/{file_id}/content         any one of its files
//	GET  /api/v1/public/{share_token}/artifacts/{artifact_id}/slides/{slide_number}/audio     a video slide's narration
//	GET  /api/v1/public/{share_token}/podcasts/{podcast_id}/stream                            a shared podcast's audio
//	GET  /api/v1/public/{share_token}/video-presentations/{video_presentation_id}             a presentation frozen into an older link
//	GET  /api/v1/public/{share_token}/video-presentations/{video_presentation_id}/slides/{slide_number}/audio
//	POST /api/v1/public/{share_token}/clone                                                   copy a shared chat (signed in)
//	POST /api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/public-link               share a finished output (its owner)
//
// The page itself, a shared video's payload and a shared podcast's details
// are internal/publicchat's; the list of an account's links and the revoke
// are internal/publiclinks'.
//
// # Who may ask
//
// The six GETs take no credential: the share token in the path is the whole
// of the authorisation, so cmd/api mounts them outside the gate (Public).
// Every one of them reads through a single statement that starts at the
// token, and that statement refuses a link that has expired and an artifact
// the link's own list (`artifact_ids`) does not name. A revoked link has no
// row. All of those, and an id that never existed, are one 404 with one
// sentence, so a stranger trying numbers learns nothing from the answers.
//
// The clone is behind a session and writes only rows that carry the
// caller's id. The link create is its owner's: the output is read by its id,
// its notebook's id and the caller's id together.
//
// # What it does better than the Python
//
//   - A shared file belongs to the account that shared it. The Python
//     looked the artifact up by its id alone once the snapshot listed it.
//     Every statement here also requires the artifact to be the snapshot
//     owner's, so a list that named somebody else's artifact reaches nothing.
//   - A revoked or expired link stops serving at once. The main-file
//     route answered `Cache-Control: public, max-age=86400`, so a browser or
//     a proxy went on showing a revoked link's file for a day. Both file
//     routes now answer `private, no-cache` with the file's checksum as the
//     validator: a repeat view is a 304, and it asks every time.
//   - Every stream honours Range, with a length, through objecthttp, and
//     is held to a stall limit and not to a whole-response timeout
//     (internal/rollingdeadline). The Python's main-file, podcast and slide
//     routes sent an unsized stream with `Accept-Ranges: bytes` and no way
//     to seek, and a long podcast on a slow link was cut by the server.
//   - No server path is read or sent. A link frozen before audio moved
//     into the file store names a path on the machine that made it. The
//     Python opened that path and sent its file name in a header; here such
//     a link's audio is simply not available.
//   - A clone is one transaction and one INSERT for its messages, where
//     the Python issued one INSERT per message, and two clones racing for an
//     account's first copy can no longer make two "Earlier chats" notebooks.
//   - Sharing an output twice at the same moment gives one link. The
//     output's row is locked while its link is looked for and written.
package publicshare

import "net/http"

// The paths.
const (
	ContentPath           = "/api/v1/public/{share_token}/artifacts/{artifact_id}/content"
	FilePath              = "/api/v1/public/{share_token}/artifacts/{artifact_id}/files/{file_id}/content"
	SlideAudioPath        = "/api/v1/public/{share_token}/artifacts/{artifact_id}/slides/{slide_number}/audio"
	PodcastStreamPath     = "/api/v1/public/{share_token}/podcasts/{podcast_id}/stream"
	PresentationPath      = "/api/v1/public/{share_token}/video-presentations/{video_presentation_id}"
	PresentationAudioPath = "/api/v1/public/{share_token}/video-presentations/{video_presentation_id}/slides/{slide_number}/audio"
	ClonePath             = "/api/v1/public/{share_token}/clone"
	OutputLinkPath        = "/api/v1/notebooks/{notebook_id}/studio/outputs/{output_id}/public-link"
)

// Public is the routes that take no credential, as "METHOD /path" pairs:
// what cmd/api mounts outside the gate.
func Public() []string {
	return []string{
		http.MethodGet + " " + ContentPath,
		http.MethodGet + " " + FilePath,
		http.MethodGet + " " + SlideAudioPath,
		http.MethodGet + " " + PodcastStreamPath,
		http.MethodGet + " " + PresentationPath,
		http.MethodGet + " " + PresentationAudioPath,
	}
}

// Served is every route as a "METHOD /path" pair.
func Served() []string {
	return append(Public(),
		http.MethodPost+" "+ClonePath,
		http.MethodPost+" "+OutputLinkPath,
	)
}

// What a caller is told. The public routes have one sentence each for
// everything that is not there, whatever the reason.
const (
	artifactAbsent     = "Artifact not found"
	artifactBytesGone  = "Artifact is no longer available"
	podcastAbsent      = "Podcast not found"
	podcastBytesGone   = "Podcast audio is no longer available"
	presentationAbsent = "Video presentation not found"
	slideAudioAbsent   = "Slide audio file not found"
	linkAbsent         = "Chat not found or no longer public"
	notAChat           = "Only a shared chat can be copied"
	sessionRequired    = "This action requires a signed-in session"
	outputAbsent       = "Output not found"
	outputUnfinished   = "Only a finished output can be shared"
	badExpiry          = "expires_in_days must be 7, 30 or null"
)
