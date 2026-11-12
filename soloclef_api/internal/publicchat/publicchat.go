// Package publicchat serves three of the ten routes of
// app/routes/public_chat_routes.py: what a public share link shows to whoever
// holds it.
//
//	GET /api/v1/public/{share_token}                               the page
//	GET /api/v1/public/{share_token}/artifacts/{artifact_id}/video a shared video's payload
//	GET /api/v1/public/{share_token}/podcasts/{podcast_id}         a shared podcast's details
//
// # These routes have no caller
//
// None of the three takes `get_auth_context`, so none resolves a credential:
// the share token in the path is the whole of the authorisation. cmd/api
// therefore mounts them beside GET /api/v1/invites/{invite_code}/info --
// outside principal.Gate, which would answer 401, and inside the rate limiter,
// because none of them is `@limiter.exempt` in app.py and SlowAPI counts them.
// A request that *does* carry a credential is answered exactly as one that
// does not: it is never looked at, so a bad token is not a 401 here and a PAT
// records no `pat_api_request` event, on either tier.
//
// # Which seven are not here
//
// The five that send bytes, the copy of a shared chat (POST .../clone) and
// the presentation frozen into an older link
// (GET .../video-presentations/{id}) are internal/publicshare's, which also
// makes the link to a Studio output. They were written after this tier
// stopped mirroring the Python and are tested as ordinary routes.
//
// # What is easy to read past
//
// Each of these is in the recorded table (testdata/publicchat_python.json,
// recorded from the Python routes' own answers) and each is replayed by this
// package's tests.
//
//   - Everything these routes answer comes out of jsonb -- a snapshot's
//     `snapshot_data`, an artifact's `metadata` -- and no Python route asks
//     what type a value is before using it. So the answers follow Python's
//     rules for a value of any JSON type: `True == 1`, so an allowlist
//     holding `true` shares artifact 1; a list of pairs is a slide, because
//     the video route calls dict() on each entry; a string where a list
//     should be is iterated character by character. python_value.go is the
//     one place those rules live.
//   - The page has a `response_model`, so pydantic validates what the route
//     built and pydantic-core writes it: `"31"` is an artifact id, a
//     timestamp is a datetime, a float too large to hold is written `null`,
//     and anything the model refuses is a 500. The other two routes have
//     none, so FastAPI's jsonable_encoder runs over the answer first -- and
//     that drops every object key starting with "_sa", at any depth.
//   - An unknown link, a revoked one and an expired one are one 404 on the
//     video and podcast routes. The page alone tells an expired link (410)
//     from one that never existed (404), on purpose: its web page says so.
//   - A path the Python *router* would not have matched -- a parameter
//     holding an encoded "/" -- is answered as Starlette's router answers it,
//     because Go's ServeMux matches it and Starlette's does not.
//
// # What these routes disclose
//
// disclosure_test.go pins, one test each, what a stranger holding a link is
// and is not sent. Changing any of it is changing a test.
package publicchat

// The paths, as app/routes/__init__.py mounts the router under /api/v1.
const (
	PagePath    = "/api/v1/public/{share_token}"
	VideoPath   = "/api/v1/public/{share_token}/artifacts/{artifact_id}/video"
	PodcastPath = "/api/v1/public/{share_token}/podcasts/{podcast_id}"
)

// The Python routes' own wording, word for word.
const (
	// get_public_chat: a revoked link is simply gone.
	pageAbsent = "Not found"
	// get_public_chat: an expired one says so, with a 410.
	pageExpired = "This link has expired"
	// get_public_artifact_video. The wording is the route's -- it says
	// "presentation" for an artifact -- and it is the same sentence for a
	// link that names nothing, an artifact the link does not share, and an
	// artifact that is not a video.
	videoAbsent   = "Video presentation not found"
	payloadAbsent = "Video Remotion payload not available"
	// get_public_podcast.
	podcastAbsent = "Podcast not found"
)

// publicPrefix is where the URLs this package writes into its answers begin.
const publicPrefix = "/api/v1/public/"
