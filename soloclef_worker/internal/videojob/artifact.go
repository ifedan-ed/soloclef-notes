package videojob

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckspec"
)

// The artifact a video is kept as, in the shapes its readers read:
//
//   - The MP4 is the primary file, which the page's video card plays
//     (Mp4ArtifactCard reads the manifest's primary file) and the download
//     sends.
//   - metadata.slides and metadata.scene_codes are what the video route
//     serves (soloclef_api/internal/artifacts/video.go), as metadata says.
//   - metadata.verification and metadata.duration_seconds are the keys
//     every artifact of a verified format carries.

// unsafeInFilename is what a file system or a header would choke on.
var unsafeInFilename = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]+`)

// videoFilename is the name a downloaded video is saved under: its title,
// letters of every script kept (the download sends them as RFC 6266
// filename*), and the job's id when nothing of the title is left. The
// render's own path ("deliverable-job-62-attempt-1.mp4") used to reach
// people as the file they downloaded.
func videoFilename(title string, jobID int64) string {
	cleaned := strings.Join(strings.Fields(unsafeInFilename.ReplaceAllString(title, " ")), " ")
	cleaned = strings.Trim(cleaned, " .")
	if utf8.RuneCountInString(cleaned) > 120 {
		cleaned = strings.TrimRight(string([]rune(cleaned)[:120]), " .")
	}
	if cleaned == "" {
		cleaned = "video-" + strconv.FormatInt(jobID, 10)
	}
	return cleaned + ".mp4"
}

// markdown is the video as text: its document, which search indexes and a
// screen reader is given. It is the deck's own text (deckspec.Markdown),
// every slide with what is spoken over it.
func markdown(video Authored) string { return deckspec.Markdown(video.Deck) }

// slideAudio is where one slide's narration was stored.
type slideAudio struct {
	Number int
	Key    string
}

// metadata is the artifact's metadata column.
//
// metadata.slides is what the video route serves (soloclef_api/internal/
// artifacts/video.go): each slide with its narration's storage key, which
// the route turns into an audio_url. metadata.scene_codes was the Remotion
// code of each scene, which an old player compiled in the browser; a video
// now has no code, and the list is kept, empty, because the route reads it
// as a list.
func metadata(video Authored, voices []voiced, shots []shot, audio []slideAudio, backend string, seconds float64) ([]byte, error) {
	keys := map[int]string{}
	for _, stored := range audio {
		keys[stored.Number] = stored.Key
	}
	slides := make([]map[string]any, 0, len(video.Deck.Slides))
	for i, s := range video.Deck.Slides {
		title := s.Title
		if title == "" {
			title = "Slide " + strconv.Itoa(i+1)
		}
		slides = append(slides, map[string]any{
			"slide_number":        i + 1,
			"title":               clipRunes(title, 120),
			"content_in_markdown": slideMarkdown(s),
			"speaker_transcripts": []string{deckspec.Spoken(s)},
			"duration_seconds":    math.Round(voices[i].Seconds*1000) / 1000,
			// When each passage starts in the slide's audio, measured: a
			// caption or a player can follow the narration part by part.
			"passage_starts_seconds": rounded(voices[i].Starts),
			"duration_in_frames":     shots[i].Frames,
			"audio_storage_key":      keys[i+1],
			"storage_backend":        backend,
		})
	}
	return json.Marshal(map[string]any{
		// The MP4 passed every check; nothing is saved that did not.
		"verification":     map[string]any{"verified": true, "reason": nil},
		"duration_seconds": math.Round(seconds*10) / 10,
		"slide_count":      len(slides),
		"theme":            video.Deck.Theme,
		"slides":           slides,
		"scene_codes":      []any{},
	})
}

// rounded is seconds to the millisecond, as the slide's duration is kept.
func rounded(seconds []float64) []float64 {
	out := make([]float64, len(seconds))
	for i, s := range seconds {
		out[i] = math.Round(s*1000) / 1000
	}
	return out
}

// slideMarkdown is one slide's text as Markdown, without its narration:
// the deck's own rendering of the slide, less the heading.
func slideMarkdown(s deckspec.Slide) string {
	one := deckspec.Deck{Title: s.Title, Slides: []deckspec.Slide{s}}
	one.Slides[0].Narration = nil
	text := deckspec.Markdown(one)
	// Markdown starts with the deck's title and the slide's numbered
	// heading; a slide's content is what follows them.
	if _, rest, found := strings.Cut(text, "\n## 1. "); found {
		if _, body, found := strings.Cut(rest, "\n"); found {
			return strings.TrimSpace(body)
		}
	}
	return strings.TrimSpace(text)
}
