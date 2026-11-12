package videojob

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The artifact a video is kept as, in the shapes its readers read:
//
//   - The MP4 is the primary file, which the page's video card plays
//     (Mp4ArtifactCard reads the manifest's primary file) and the download
//     sends.
//   - metadata.slides and metadata.scene_codes are what the video route
//     serves (soloclef_api/internal/artifacts/video.go): each slide with its
//     narration's storage key, which the route turns into an audio_url and
//     the slide audio route streams. The Python worker saved neither, so the
//     route answered 404 for every video it made.
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
// screen reader is given. It is titled with the video's own title (the
// Python worker's said "Video deck" for every one) and carries no bold.
func markdown(title string, video Authored) string {
	var b strings.Builder
	b.WriteString("# " + title + "\n")
	for _, scene := range video.Scenes {
		b.WriteString("\n## Scene " + strconv.Itoa(scene.Number) + "\n\n" + strings.TrimSpace(scene.Markdown) + "\n\nNarration: " + scene.Transcript + "\n")
	}
	return b.String()
}

var headingLine = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.+?)\s*#*\s*$`)

// slideTitle is a scene's title for the player's list: its on-screen
// heading, else its first line, else its number.
func slideTitle(scene Scene) string {
	first := ""
	for _, line := range strings.Split(scene.Markdown, "\n") {
		if match := headingLine.FindStringSubmatch(line); match != nil {
			first = match[1]
			break
		}
		if first == "" && strings.TrimSpace(line) != "" {
			first = strings.TrimSpace(line)
		}
	}
	first = strings.TrimSpace(strings.Trim(first, "*_`"))
	if first == "" {
		return "Scene " + strconv.Itoa(scene.Number)
	}
	return clipRunes(first, 120)
}

// slideAudio is where one scene's narration was stored.
type slideAudio struct {
	Number int
	Key    string
}

// metadata is the artifact's metadata column.
func metadata(video Authored, voices []voiced, audio []slideAudio, backend string, seconds float64) ([]byte, error) {
	keys := map[int]string{}
	for _, stored := range audio {
		keys[stored.Number] = stored.Key
	}
	lengths := map[int]float64{}
	for _, voice := range voices {
		lengths[voice.Number] = voice.Seconds
	}
	slides := make([]map[string]any, 0, len(video.Scenes))
	codes := make([]map[string]any, 0, len(video.Scenes))
	for _, scene := range video.Scenes {
		frames, _ := sceneFrames(lengths[scene.Number], fps, minFrames)
		title := slideTitle(scene)
		slides = append(slides, map[string]any{
			"slide_number":        scene.Number,
			"title":               title,
			"content_in_markdown": scene.Markdown,
			"speaker_transcripts": []string{scene.Transcript},
			"duration_seconds":    math.Round(lengths[scene.Number]*1000) / 1000,
			"duration_in_frames":  frames,
			"audio_storage_key":   keys[scene.Number],
			"storage_backend":     backend,
		})
		codes = append(codes, map[string]any{"slide_number": scene.Number, "code": scene.Code, "title": title})
	}
	return json.Marshal(map[string]any{
		// The MP4 passed every structural check, and its scenes the layout
		// check; nothing is saved that did not, so there is no "not
		// checked" to record.
		"verification":     map[string]any{"verified": true, "reason": nil},
		"duration_seconds": math.Round(seconds*10) / 10,
		"slide_count":      len(slides),
		"scene_code_count": len(codes),
		"slides":           slides,
		"scene_codes":      codes,
	})
}
