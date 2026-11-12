// Package medialinks lets the audio and video inside an interactive report
// play:
//
//	POST /api/v1/artifacts/media-links        signed links for the caller's media files
//	GET  /api/v1/artifact-media/{token}       the file a link names, with Range
//
// An interactive report is shown in a frame sandboxed to an origin of its
// own, so a player inside it sends neither the session cookie nor a bearer
// header. The viewer therefore asks for a link to each media file the
// report embeds and writes those links into the page. A link is a signed
// claim (token.go): one file of one artifact of one account, for twelve
// hours. Holding it is the whole of the authorisation, which is why the
// second route is mounted outside the gate (Public) and why every use reads
// the file again under the account the link names: a file deleted, or an
// account closed, since the link was made is no longer served.
//
// # What it does better than the Python
//
//   - **The link's path is its own** (`/api/v1/artifact-media/…`). The
//     Python put it under `/api/v1/public/`, beside the share links, where
//     a router that matches by segment cannot tell `artifact-media` from a
//     share token. The web reads the address from the answer, so nothing
//     there names the path.
//   - **A file whose artifact is being deleted is not linked and not
//     served.**
//   - **One statement finds the files** by owner, where the Python built an
//     OR of up to twenty-four pairs.
//   - **The bytes are streamed** through the artifact routes' own sender:
//     Range, a rolling write deadline for a slow player, and a validator.
package medialinks

import "net/http"

// The paths.
const (
	IssuePath = "/api/v1/artifacts/media-links"
	MediaPath = "/api/v1/artifact-media/{token}"
	// mediaPrefix is MediaPath without its token.
	mediaPrefix = "/api/v1/artifact-media/"
)

// Served is every route as a "METHOD /path" pair.
func Served() []string {
	return []string{
		http.MethodPost + " " + IssuePath,
		http.MethodGet + " " + MediaPath,
	}
}

// Public is the route that takes no session: the link is the credential.
func Public() []string {
	return []string{http.MethodGet + " " + MediaPath}
}

const (
	// maxLinks is the most files one request may name. A report embeds a
	// handful of players; two dozen is generous, and bounds the statement.
	maxLinks = 24
	// maxBodyBytes bounds the request: two dozen pairs of numbers.
	maxBodyBytes = 16 << 10

	mediaAbsent     = "Media not found"
	sessionRequired = "This route needs a signed-in session."
)
