package publicshare

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strconv"
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
	if !inline {
		headers.Set("Content-Security-Policy", "sandbox")
	}
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

// content is an artifact's main file: the picture in a shared chat's image
// card, a shared podcast's audio, a shared output's file.
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

// slideAudio is the narration of one slide of a shared video: the address
// the shared video's payload gives each slide (internal/publicchat).
func (r Routes) slideAudio(x *exchange) {
	ids, ok := x.ids("artifact_id", "slide_number")
	if !ok {
		return
	}
	slides, err := New(r.Pool).SharedVideoSlides(x.ctx.Context(), SharedVideoSlidesParams{
		ShareToken: x.ctx.Param("share_token"), Now: r.now(), ArtifactID: ids[0]})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, presentationAbsent)
		return
	}
	if err != nil {
		x.fail("could not read a shared video's slides", err)
		return
	}
	x.sendSlideAudio(entries(slides), ids[1])
}

// sendSlideAudio streams the narration of the slide with this number, out
// of the file store.
//
// A slide rendered before narration moved into the store names only a path
// on the machine that rendered it (`audio_file`). That path is never
// opened: it is not in the store, and a route with no caller does not read
// the server's disk on the word of a stored string.
func (x *exchange) sendSlideAudio(slides []map[string]any, number int64) {
	slide := numbered(slides, "slide_number", number)
	if slide == nil {
		x.refuse(http.StatusNotFound, "Slide "+strconv.FormatInt(number, 10)+" not found")
		return
	}
	key, _ := slide["audio_storage_key"].(string)
	if strings.TrimSpace(key) == "" {
		x.refuse(http.StatusNotFound, slideAudioAbsent)
		return
	}
	// A slide records its backend beside its key, and that record is
	// followed. One written before the backend was recorded names none, and
	// its bytes are where the writer then put them: the configured backend.
	// That is what an absent record means, as for the owner's own stream
	// (internal/artifacts); a slide that names a backend is read from it
	// and from no other.
	backend := x.routes.Files.Configured()
	if recorded, _ := slide["storage_backend"].(string); recorded != "" {
		backend = recorded
	}
	// The writer stores WAV or MP3 and names the key to match.
	extension, mediaType := ".mp3", "audio/mpeg"
	if strings.EqualFold(path.Ext(key), ".wav") {
		extension, mediaType = ".wav", "audio/wav"
	}
	x.response.Header().Set("X-Content-Type-Options", "nosniff")
	x.stream(artifacts.StoredObject{
		Backend: backend,
		Key:     key,
		Absent:  slideAudioAbsent,
		Headers: objecthttp.Headers{
			ContentType: mediaType,
			// No checksum is kept for a slide's audio; objecthttp derives
			// the validator from the stored object.
			CacheControl: revalidate,
			// Named for what it is, never for where it is stored.
			Disposition: artifacts.ContentDisposition("slide-"+strconv.FormatInt(number, 10)+extension, true),
		},
	})
}

// podcastStream is a shared podcast's audio by the podcast's id, which is
// how the podcast card of a shared chat asks for it: the card's result
// names the podcast, and the link's frozen entry for that podcast says
// which artifact holds the audio.
func (r Routes) podcastStream(x *exchange) {
	ids, ok := x.ids("podcast_id")
	if !ok {
		return
	}
	token := x.ctx.Param("share_token")
	frozen, err := New(r.Pool).FrozenMedia(x.ctx.Context(), FrozenMediaParams{ShareToken: token, Now: r.now()})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, podcastAbsent)
		return
	}
	if err != nil {
		x.fail("could not read a link's podcasts", err)
		return
	}
	podcast := numbered(entries(frozen.Podcasts), "original_id", ids[0])
	if podcast == nil {
		x.refuse(http.StatusNotFound, podcastAbsent)
		return
	}
	if artifactID, isArtifact := wholeNumber(podcast["artifact_id"]); isArtifact {
		// The audio is the artifact's main file, reached through the same
		// statement as any shared file: the link must list the artifact
		// and its owner must own it.
		row, err := New(r.Pool).SharedPrimaryFile(x.ctx.Context(), SharedPrimaryFileParams{
			ShareToken: token, Now: r.now(), ArtifactID: artifactID})
		if errors.Is(err, pgx.ErrNoRows) {
			x.refuse(http.StatusNotFound, podcastBytesGone)
			return
		}
		if err != nil {
			x.fail("could not read a shared podcast's audio file", err)
			return
		}
		x.sendFile(linkFile{backend: row.StorageBackend, key: row.StorageKey, filename: row.OriginalFilename,
			mimeType: row.MimeType, checksum: row.ChecksumSha256}, podcastBytesGone)
		return
	}
	// A link frozen between the file store and artifacts: its entry holds
	// the key the audio was stored under and no backend, because there was
	// one backend then and the writer used the configured one. A link older
	// still names a path on the server, which is not read (sendSlideAudio).
	key, _ := podcast["storage_key"].(string)
	if strings.TrimSpace(key) == "" {
		x.refuse(http.StatusNotFound, podcastBytesGone)
		return
	}
	x.response.Header().Set("X-Content-Type-Options", "nosniff")
	x.stream(artifacts.StoredObject{
		Backend: r.Files.Configured(),
		Key:     key,
		Absent:  podcastBytesGone,
		Headers: objecthttp.Headers{
			ContentType:  "audio/mpeg",
			CacheControl: revalidate,
			Disposition:  artifacts.ContentDisposition("podcast-"+strconv.FormatInt(ids[0], 10)+".mp3", true),
		},
	})
}

// entries reads a stored JSON list of objects; anything else in the column,
// and anything in the list that is not an object, is no entry. Numbers keep
// their digits (json.Number), so an id is compared exactly.
func entries(column []byte) []map[string]any {
	if len(column) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(column))
	decoder.UseNumber()
	var list []any
	if err := decoder.Decode(&list); err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if entry, isObject := item.(map[string]any); isObject {
			out = append(out, entry)
		}
	}
	return out
}

// wholeNumber reads a stored JSON integer; text, a fraction and a truth
// are not ids.
func wholeNumber(value any) (int64, bool) {
	number, isNumber := value.(json.Number)
	if !isNumber {
		return 0, false
	}
	n, err := strconv.ParseInt(number.String(), 10, 64)
	return n, err == nil
}

// numbered is the first entry whose key holds this whole number, or nil.
func numbered(list []map[string]any, key string, number int64) map[string]any {
	for _, entry := range list {
		if stored, ok := wholeNumber(entry[key]); ok && stored == number {
			return entry
		}
	}
	return nil
}
