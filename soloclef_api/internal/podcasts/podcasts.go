// Package podcasts serves the podcast routes: the voices and languages a
// brief may choose from, an audible sample of a voice, creating a podcast,
// editing and approving its brief, the lifecycle transitions, the detail and
// the delete. That is all eleven routes of app/podcasts/api/routes.py; the
// audio itself is the artifact's and is served elsewhere.
//
// A podcast is a row in `podcast_runs` that moves through a small state
// machine: a brief is proposed and reviewed (`awaiting_brief`), approved
// (`drafting`), voiced (`rendering`) and delivered (`ready`). A finished
// episode can be sent back to the brief gate for a new take, and that can be
// undone while the old audio is still there. The frontend watches the status
// through Zero; these routes are the actions, and the one read that carries
// what Zero does not -- the brief and the transcript.
//
// Which voices exist is soloclef_assistant/voices' business. This package asks it for
// the roster and never looks past it.
//
// # What this does better than the routes it replaces
//
//   - A transition is written only from the status it was decided on
//     (queries.sql, MovePodcast). Two approvals that race used to both write
//     `drafting` and both enqueue a draft; now the second is a 409.
//   - An approval whose draft cannot be queued is undone and answered 503.
//     It used to be a 500 about a podcast left `drafting` with nothing
//     queued to draft it, which only a cancel could get out of.
//   - A brief that is stored but cannot be read -- `{}`, or one a later
//     version of the app no longer accepts -- is refused by the approval and
//     the regeneration with a 422, before anything is written. It used to be
//     approved, drafted from, and then answered with a 500.
//   - The detail of such a podcast is still answered, with `"spec": null`,
//     instead of a 500. The same goes for a transcript that cannot be read.
//   - The three transitions answer the episode's `artifact_id`. They used to
//     answer null beside `"has_audio": true`.
//   - A podcast is its owner's alone: every statement names the caller as
//     the owner, and another account's podcast is "Podcast not found", the
//     same answer as one that does not exist, so an id cannot be probed for.
//     There is no membership or permission to read first.
//   - An id no row can have -- past the column's range -- is a 404, not a 500.
//   - A 500 keeps its CORS headers, so a browser can read the envelope.
//
// And, of the six routes that came over with the voice catalogue:
//
//   - The voices and the languages need a signed-in caller. They took no
//     credential at all.
//   - A voice sample cannot be turned into a speech service: the caller
//     picks a voice and nothing else, and each voice is synthesised once for
//     the deployment (voice_routes.go, soloclef_assistant/voices/samples.go). Each API
//     container used to keep its own samples in its own temporary directory.
//   - An edited brief's voices are checked against the roster: each must
//     exist and speak the brief's language (brief_route.go). An unknown
//     voice used to be stored, approved and drafted from, and failed in the
//     render after the transcript had been paid for.
//   - An edit is written only if the brief is still at the version and the
//     status it was checked against. Two edits that raced both used to win.
//   - An edit that does not mention a speaker's delivery note keeps it. The
//     web app's form does not know the field, so every save erased the notes
//     voice casting had written.
//   - A new podcast's thread must be one of the caller's own. Any thread id
//     was accepted, somebody else's included, and one that did not exist was
//     a 500.
//   - A new podcast whose minimum length exceeds its maximum is a 422. It
//     was a 500, raised while building the brief.
//   - A last brief that cannot be read costs a new podcast its defaults, not
//     its creation. It was a 500.
//   - A body's numbers must be numbers and its keys must be known: `"2"`,
//     `true` and a misspelt `speaker_count` are refused instead of being
//     coerced or ignored.
//   - Cancelling a podcast that is already cancelled is answered as done.
//   - Nothing here touches billing. A cancel used to release a hold on the
//     owner's balance; there is no such hold any more (action_route.go).
package podcasts

// The paths, as app/routes/__init__.py mounts the router under /api/v1.
const (
	VoicesPath     = "/api/v1/podcasts/voices"
	LanguagesPath  = "/api/v1/podcasts/languages"
	PreviewPath    = "/api/v1/podcasts/voices/{voice_id}/preview"
	CreatePath     = "/api/v1/podcasts"
	DetailPath     = "/api/v1/podcasts/{podcast_id}"
	BriefPath      = "/api/v1/podcasts/{podcast_id}/spec"
	ApprovePath    = "/api/v1/podcasts/{podcast_id}/brief/approve"
	RegeneratePath = "/api/v1/podcasts/{podcast_id}/transcript/regenerate"
	RevertPath     = "/api/v1/podcasts/{podcast_id}/regenerate/revert"
	CancelPath     = "/api/v1/podcasts/{podcast_id}/cancel"
	DeletePath     = "/api/v1/podcasts/{podcast_id}"
)

// DraftTask is the podcast job's name (soloclef_worker/internal/podcast),
// which drafts the transcript and then voices it, in one job.
// soloclef_celery/routing sends it to the heavy queue.
const DraftTask = "podcast.draft_transcript"

// What a caller is told. Most are the Python routes' own sentences, which the
// web app shows as they are.
const (
	podcastAbsent    = "Podcast not found"
	deleted          = "Podcast deleted successfully"
	noBriefToDraft   = "cannot draft without a brief"
	noBriefToRedo    = "cannot regenerate without a brief"
	noEpisode        = "no finished episode to fall back to"
	nothingToRedo    = "nothing to regenerate from "
	episodeExists    = "a finished episode exists; revert the regeneration instead"
	notAtTheGate     = "the brief can only be edited while awaiting_brief, not "
	briefUnreadable  = "the stored brief cannot be read; regenerate the podcast or edit its brief"
	changedMeanwhile = "the podcast changed while this was being done; refresh and try again"
	queueFailed      = "Failed to queue the transcript draft. Please try again."

	// The voice catalogue's three ways of not answering, and the sample's.
	noSpeechService     = "No TTS provider configured"
	voicesUnavailable   = "The voices could not be loaded just now. Please try again."
	voicesMisconfigured = "Podcast voices are not set up correctly on this server."
	sampleUnavailable   = "The voice sample could not be played just now. Please try again."
	voiceAbsent         = "Unknown voice"
	threadAbsent        = "Thread not found"
)
