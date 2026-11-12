package videojob

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/llm"
)

// Scene is one scene of the video: what is said, what is on screen as text,
// and the TSX module that draws it. Number and Filename are this worker's,
// assigned in order; a model never names them.
type Scene struct {
	Number     int
	Filename   string
	Markdown   string
	Transcript string
	Code       string
}

// Authored is the whole video as written.
type Authored struct {
	Language string
	Scenes   []Scene
}

func (a Authored) transcripts() []string {
	out := make([]string, len(a.Scenes))
	for i, scene := range a.Scenes {
		out[i] = scene.Transcript
	}
	return out
}

// The bounds of one scene, the Python worker's.
const (
	maxTranscriptRunes = 8_000
	maxMarkdownRunes   = 20_000
	maxCodeRunes       = 100_000
	maxFindingsRunes   = 16_000
)

// author asks the account's chat model to write, shorten and repair.
type author struct {
	model   model.Model
	timeout time.Duration
}

// encode is JSON for a model to read: characters as they are, not escaped
// for HTML, because a scene's TSX is full of < and > and a model shown
// < rewrites the escapes into the code it returns.
func encode(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSpace(buffer.String()), nil
}

// write is the first draft.
func (a author) write(ctx context.Context, title string, request Request) (Authored, error) {
	payload := struct {
		Title            string   `json:"title"`
		Brief            string   `json:"brief"`
		SourceReferences []string `json:"source_references"`
		SourceMaterial   string   `json:"source_material,omitempty"`
		SourceNote       string   `json:"source_note,omitempty"`
	}{Title: title, Brief: request.Brief, SourceReferences: request.SourceReferences, SourceMaterial: request.SourceMaterial}
	if payload.SourceReferences == nil {
		payload.SourceReferences = []string{}
	}
	// The material is given whole: the chat holds it to 180,000 characters
	// (60,000 tokens at three a token), which is the most the Python worker
	// ever gave a model with a large window. Only a request that says it was
	// condensed before it was queued carries the note.
	if request.SourceCondensed && request.SourceMaterial != "" {
		payload.SourceNote = condensedNote
	}
	content, err := encode(payload)
	if err != nil {
		return Authored{}, err
	}
	return llm.AskJSON(ctx, a.model, a.timeout, []model.Message{
		{Role: model.RoleSystem, Content: authorPrompt(request.limitSeconds())},
		{Role: model.RoleUser, Content: content},
	}, readDraft)
}

// shorten rewrites the transcripts only, to the target the measurement
// names.
func (a author) shorten(ctx context.Context, video Authored, measurement map[string]any) (Authored, error) {
	scenes := make([]map[string]string, len(video.Scenes))
	for i, scene := range video.Scenes {
		scenes[i] = map[string]string{"transcript": scene.Transcript, "on_screen_markdown": scene.Markdown}
	}
	payload := map[string]any{"scene_count": len(video.Scenes), "scenes": scenes}
	for key, value := range measurement {
		payload[key] = value
	}
	content, err := encode(payload)
	if err != nil {
		return Authored{}, err
	}
	transcripts, err := llm.AskJSON(ctx, a.model, a.timeout, []model.Message{
		{Role: model.RoleSystem, Content: shortenPrompt},
		{Role: model.RoleUser, Content: content},
	}, readShortened(len(video.Scenes)))
	if err != nil {
		return Authored{}, err
	}
	shortened := Authored{Language: video.Language, Scenes: append([]Scene(nil), video.Scenes...)}
	for i := range shortened.Scenes {
		shortened.Scenes[i].Transcript = transcripts[i]
	}
	return shortened, nil
}

// repair rewrites the code and on-screen text only, from what the pipeline
// found. The narration is already voiced and stays.
func (a author) repair(ctx context.Context, video Authored, findings string) (Authored, error) {
	scenes := make([]map[string]any, len(video.Scenes))
	for i, scene := range video.Scenes {
		scenes[i] = map[string]any{
			"slide_number": scene.Number, "filename": scene.Filename,
			"on_screen_markdown": scene.Markdown, "transcript": scene.Transcript, "code": scene.Code,
		}
	}
	content, err := encode(map[string]any{
		"findings":    clipRunes(findings, maxFindingsRunes),
		"scene_count": len(video.Scenes),
		"video":       map[string]any{"language": video.Language, "scenes": scenes},
	})
	if err != nil {
		return Authored{}, err
	}
	repaired, err := llm.AskJSON(ctx, a.model, a.timeout, []model.Message{
		{Role: model.RoleSystem, Content: repairPrompt},
		{Role: model.RoleUser, Content: content},
	}, readRepair(len(video.Scenes)))
	if err != nil {
		return Authored{}, err
	}
	out := Authored{Language: video.Language, Scenes: append([]Scene(nil), video.Scenes...)}
	for i := range out.Scenes {
		out.Scenes[i].Markdown, out.Scenes[i].Code = repaired[i].Markdown, repaired[i].Code
	}
	return out, nil
}

// The names a reply has been seen to use for each field. A model that
// writes "narration" for "transcript" has answered the question; refusing
// it would spend a retry on a spelling.
var (
	transcriptNames = []string{"transcript", "narration"}
	markdownNames   = []string{"on_screen_markdown", "onScreenMarkdown"}
	codeNames       = []string{"code", "tsx", "tsx_module"}
)

// sceneList reads the reply's scenes as objects, keeping unknown fields out
// of the way.
func sceneList(raw []byte) (language string, scenes []map[string]any, err error) {
	var reply struct {
		Language any              `json:"language"`
		Scenes   []map[string]any `json:"scenes"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "", nil, llm.Reject("the reply is not an object with a scenes array: %v", err)
	}
	if text, isText := reply.Language.(string); isText {
		language = strings.TrimSpace(text)
	}
	return language, reply.Scenes, nil
}

// field is the first of names the scene has as a non-empty string, checked
// against its bound.
func field(scene map[string]any, index int, names []string, limit int, problems *[]string) string {
	for _, name := range names {
		if text, isText := scene[name].(string); isText && strings.TrimSpace(text) != "" {
			if utf8.RuneCountInString(text) > limit {
				*problems = append(*problems, fmt.Sprintf("scene %d: %s is longer than %d characters", index+1, names[0], limit))
			}
			return text
		}
	}
	*problems = append(*problems, fmt.Sprintf("scene %d: %s is missing or empty", index+1, names[0]))
	return ""
}

func refuse(problems []string) error {
	return llm.Reject("%s", strings.Join(problems, "; "))
}

// readDraft reads a first draft and gives its scenes their numbers and
// filenames.
func readDraft(raw []byte) (Authored, error) {
	language, scenes, err := sceneList(raw)
	if err != nil {
		return Authored{}, err
	}
	if len(scenes) < 1 || len(scenes) > maxScenes {
		return Authored{}, llm.Reject("the video must have between 1 and %d scenes; the reply has %d", maxScenes, len(scenes))
	}
	if utf8.RuneCountInString(language) > 64 {
		language = ""
	}
	video := Authored{Language: language}
	var problems []string
	for i, scene := range scenes {
		video.Scenes = append(video.Scenes, Scene{
			Number:     i + 1,
			Filename:   fmt.Sprintf("scene-%02d.tsx", i+1),
			Transcript: strings.TrimSpace(field(scene, i, transcriptNames, maxTranscriptRunes, &problems)),
			Markdown:   field(scene, i, markdownNames, maxMarkdownRunes, &problems),
			Code:       field(scene, i, codeNames, maxCodeRunes, &problems),
		})
	}
	if len(problems) > 0 {
		return Authored{}, refuse(problems)
	}
	return video, nil
}

// sameCount refuses a reply that dropped or added scenes: the scenes are
// already narrated, one voice per scene, in order.
func sameCount(scenes []map[string]any, expected int) error {
	if len(scenes) != expected {
		return llm.Reject("expected %d scenes, got %d: return exactly one entry for each supplied scene, in the same order", expected, len(scenes))
	}
	return nil
}

func readShortened(expected int) func([]byte) ([]string, error) {
	return func(raw []byte) ([]string, error) {
		_, scenes, err := sceneList(raw)
		if err != nil {
			return nil, err
		}
		if err := sameCount(scenes, expected); err != nil {
			return nil, err
		}
		var problems []string
		out := make([]string, len(scenes))
		for i, scene := range scenes {
			out[i] = strings.TrimSpace(field(scene, i, transcriptNames, maxTranscriptRunes, &problems))
		}
		if len(problems) > 0 {
			return nil, refuse(problems)
		}
		return out, nil
	}
}

type repairedScene struct{ Markdown, Code string }

func readRepair(expected int) func([]byte) ([]repairedScene, error) {
	return func(raw []byte) ([]repairedScene, error) {
		_, scenes, err := sceneList(raw)
		if err != nil {
			return nil, err
		}
		if err := sameCount(scenes, expected); err != nil {
			return nil, err
		}
		var problems []string
		out := make([]repairedScene, len(scenes))
		for i, scene := range scenes {
			out[i] = repairedScene{
				Markdown: field(scene, i, markdownNames, maxMarkdownRunes, &problems),
				Code:     field(scene, i, codeNames, maxCodeRunes, &problems),
			}
		}
		if len(problems) > 0 {
			return nil, refuse(problems)
		}
		return out, nil
	}
}

// clipRunes cuts text to at most limit characters.
func clipRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}
