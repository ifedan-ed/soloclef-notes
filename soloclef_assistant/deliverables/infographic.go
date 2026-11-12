package deliverables

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"log/slog"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// An infographic is a plan in Markdown (a title, sections, short labels,
// key numbers) drawn as one image by the image model in a visual style the
// person picks.
//
// It is one call of save_artifact with the format "infographic", run twice
// by the loop:
//
//  1. The plan is checked, and the call pauses with the style question
//     (tool.AskUser). Nothing has been drawn and nothing has been spent. The
//     page shows the question as a row of style cards.
//  2. With the answer, the image is drawn, read back by a model that reads
//     images to see whether the planned text is really in it, drawn once
//     more with the problems named if too much is wrong, and saved with
//     what the check found.
//
// What this does better than the procedure it replaces:
//
//   - It is one tool. The old procedure was four (load the instructions,
//     which asked the question; run a sandbox command that drew the image;
//     verify; save), holding the person's choice in a signed token and the
//     image's provenance in a signed file inside the sandbox so that the
//     next tool could trust the last. Here the choice is the answer the
//     loop hands back to the same call, and there is nothing to forge.
//   - No sandbox is started to draw an image, which never needed one.
//   - Of the two attempts, the one whose text reads better is kept. The old
//     procedure kept the second whatever it looked like.
//   - A second attempt that fails does not lose the first: an image that
//     was paid for is saved, with its warning.
//   - An image with transparency is flattened onto white instead of being
//     refused after it was paid for.
//   - "Auto" is settled from the plan itself, so the model cannot steer it
//     with a separate brief.

// The style question's identity. The page sends both back with the answer,
// and an answer to some other question, or to an older set of styles, is
// not taken as an answer to this one.
const (
	styleQuestionID      = "infographic.visual-style"
	styleQuestionVersion = 1
	// styleQuestion is the one question's own id within the payload.
	styleQuestion = "visual-style"
	autoStyle     = "auto"
	// defaultAutoStyle is what "auto" becomes for a plan none of the rules
	// below recognises.
	defaultAutoStyle = "sketch-note"
)

// visualStyle is one style a person can pick: its card on the page and the
// words the image model is given for it.
type visualStyle struct {
	id    string
	label string
	// description is sent to the image model and shown under the card.
	description string
	// signals are the words in a plan that make "auto" pick this style.
	signals []string
}

// visualStyles is every style, in the order the page shows them. The ids
// are the names of the page's preview pictures
// (public/infographic-styles/<id>.webp), so an id cannot be changed here
// alone.
var visualStyles = []visualStyle{
	{"kawaii", "Kawaii", "Use a playful kawaii illustration style with rounded shapes, pastel colors, thick clean outlines, friendly expressive icons, generous spacing, and highly legible infographic labels.",
		[]string{"playful", "children", "child", "beginner", "friendly", "cute"}},
	{"clay", "Clay", "Use a tactile three-dimensional clay style with softly modeled forms, rounded edges, subtle handmade texture, gentle studio lighting, clear visual grouping, and highly legible infographic labels.",
		[]string{"tactile", "product", "craft", "object", "physical", "handmade"}},
	{"sketch-note", "Sketch Note", "Use a hand-drawn editorial sketchnote style with mostly black ink on a warm white background, one restrained accent color, simple icons, arrows, connectors, loose organic lines, generous whitespace, short hand-lettered headings, and highly legible labels. Avoid photorealism, dense paragraphs, decorative illegible handwriting, and watermarks.",
		[]string{"study", "notes", "brainstorming", "teaching", "concept", "explain"}},
	{"anime", "Anime", "Use a polished anime-inspired cel-shaded style with expressive characters or icons, clean linework, vivid controlled color, energetic composition, clear section hierarchy, and highly legible infographic labels.",
		[]string{"entertainment", "gaming", "game", "manga", "dynamic"}},
	{"editorial", "Editorial", "Use a magazine editorial style: a large bold serif headline, clean sans-serif labels, a strong typographic hierarchy on an off-white page, black text with one bold accent colour, flat spot illustrations, thin rules, pull-quote numbers and a column grid with generous margins. Highly legible labels, no photographs.",
		[]string{"news", "story", "history", "magazine", "feature"}},
	{"instructional", "Instructional", "Use a clear instructional-manual style: numbered steps or panels in an obvious reading order, simple flat line icons and diagrams, arrows that show sequence and cause, callout boxes for tips and warnings, a light neutral background with one blue accent and one orange highlight, and highly legible sans-serif labels.",
		[]string{"steps", "procedure", "instructions", "checklist", "workflow"}},
	{"bento-grid", "Bento Grid", "Use a modern bento-grid layout: the canvas divided into rounded rectangular tiles of different sizes with even gutters on a light background, one idea per tile, a large hero tile for the main point, big numbers in their own tiles, simple glyph icons, soft shadows, a cohesive muted palette with one vivid accent, and crisp sans-serif labels.",
		[]string{"dashboard", "highlights", "summary", "metrics", "stats"}},
	{"bricks", "Bricks", "Use a toy building-brick style: every object, icon and chart built from glossy interlocking plastic bricks with round studs, in bright primary red, yellow, blue and green on a clean light background, soft studio lighting, labels printed on flat brick tiles or plates, playful but tidy composition, and highly legible text. No brand logos.",
		[]string{"toy", "toys", "building", "blocks", "bricks"}},
	{"scientific", "Scientific", "Use a scientific-figure style like a journal or conference poster: white background, precise vector schematics and labelled diagrams with leader lines, charts with axes and units, panel letters (A, B, C), a restrained colour-blind-safe palette of blues, teals and greys, thin strokes, and small but crisp sans-serif labels.",
		[]string{"experiment", "research", "trial", "clinical", "scientific"}},
	{"professional", "Professional", "Use a polished corporate style like a consulting report: clean flat design on white, navy and slate grey with one teal accent, a strict grid of aligned sections, simple line icons, clean bar or donut charts, bold KPI numbers with short captions, sans-serif typography, and highly legible labels. No clip art or decoration.",
		[]string{"company", "business", "quarterly", "revenue", "market"}},
}

// autoOrder is the order "auto" tries the styles' signals in. It is not the
// display order: a plan with both "study" and "research" in it is a study
// aid first.
var autoOrder = []string{"kawaii", "clay", "sketch-note", "anime", "instructional", "scientific", "professional", "editorial", "bento-grid", "bricks"}

func styleNamed(id string) (visualStyle, bool) {
	for _, style := range visualStyles {
		if style.id == id {
			return style, true
		}
	}
	return visualStyle{}, false
}

var planWord = regexp.MustCompile(`[a-z0-9]+`)

// resolveStyle is the style an answer means. "auto" is settled from the
// words of the plan, the same way every time, so that drawing the same plan
// again gives the same style.
func resolveStyle(requested, plan string) (visualStyle, error) {
	if requested != autoStyle {
		style, known := styleNamed(requested)
		if !known {
			return visualStyle{}, fmt.Errorf("%q is not one of the infographic styles", requested)
		}
		return style, nil
	}
	words := map[string]bool{}
	for _, word := range planWord.FindAllString(strings.ToLower(plan), -1) {
		words[word] = true
	}
	for _, id := range autoOrder {
		style, _ := styleNamed(id)
		for _, signal := range style.signals {
			if words[signal] {
				return style, nil
			}
		}
	}
	style, _ := styleNamed(defaultAutoStyle)
	return style, nil
}

// The style question as the page's question card reads it
// (features/chat-messages/hitl/questions): the loop passes it through
// untouched and adds the call's id.
type questionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	// PreviewAsset names the picture on the option's card; the page maps
	// "infographic-style/<id>" to its own file.
	PreviewAsset string `json:"preview_asset,omitempty"`
}

type questionOrigin struct {
	Kind          string `json:"kind"`
	PresetID      string `json:"preset_id"`
	PresetVersion int    `json:"preset_version"`
}

type askedQuestion struct {
	ID                string           `json:"id"`
	Prompt            string           `json:"prompt"`
	InputType         string           `json:"input_type"`
	Presentation      string           `json:"presentation"`
	Required          bool             `json:"required"`
	MinimumSelections int              `json:"minimum_selections"`
	MaximumSelections int              `json:"maximum_selections"`
	Options           []questionOption `json:"options"`
}

type structuredQuestion struct {
	Type      string          `json:"type"`
	Version   int             `json:"version"`
	Title     string          `json:"title"`
	Message   string          `json:"message,omitempty"`
	Origin    questionOrigin  `json:"origin"`
	Questions []askedQuestion `json:"questions"`
}

// styleQuestionPayload is the question a paused infographic asks.
func styleQuestionPayload() structuredQuestion {
	options := []questionOption{{
		ID: autoStyle, Label: "Auto",
		Description:  "Pick the style that suits the infographic's content.",
		PreviewAsset: "infographic-style/" + autoStyle,
	}}
	for _, style := range visualStyles {
		options = append(options, questionOption{ID: style.id, Label: style.label, Description: style.description, PreviewAsset: "infographic-style/" + style.id})
	}
	return structuredQuestion{
		Type: "structured_question", Version: 1,
		Title:   "Choose an infographic style",
		Message: "Select a visual direction before the infographic is generated.",
		Origin:  questionOrigin{Kind: "preset", PresetID: styleQuestionID, PresetVersion: styleQuestionVersion},
		Questions: []askedQuestion{{
			ID: styleQuestion, Prompt: "Which visual style should be used?",
			InputType: "single_select", Presentation: "visual_cards", Required: true,
			MinimumSelections: 1, MaximumSelections: 1, Options: options,
		}},
	}
}

// errStyleCancelled is the person closing the style question without
// choosing.
var errStyleCancelled = errors.New("the user closed the style question without choosing, so no infographic was made. Tell the requester exactly that, and do not try again unless asked")

// infographicChoice is what the answer to the style question chose: the
// style's id, and the canvas to draw on.
type infographicChoice struct {
	style  string
	canvas canvas
}

// chosenStyle reads the answer to the style question: the id of the option
// picked, and the orientation when the answer names one. The page's
// question card asks only for the style; a studio run's answer
// (StyleAnswer) also carries the orientation its dialog chose.
func chosenStyle(answer json.RawMessage) (infographicChoice, error) {
	var reply struct {
		Type          string `json:"type"`
		PresetID      string `json:"preset_id"`
		PresetVersion int    `json:"preset_version"`
		Orientation   string `json:"orientation"`
		Answers       []struct {
			QuestionID string   `json:"question_id"`
			OptionIDs  []string `json:"option_ids"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(answer, &reply); err != nil {
		return infographicChoice{}, errors.New("the answer to the style question is not readable; no infographic was made")
	}
	if reply.Type == "cancel" {
		return infographicChoice{}, errStyleCancelled
	}
	if reply.Type != "respond" || reply.PresetID != styleQuestionID || reply.PresetVersion != styleQuestionVersion {
		// An answer to another question, or to a set of styles this
		// version no longer has. Drawing "something" for it would be
		// spending the person's money on a choice they did not make.
		return infographicChoice{}, errors.New("the answer is not an answer to the infographic style question; no infographic was made")
	}
	shape, err := canvasNamed(reply.Orientation)
	if err != nil {
		return infographicChoice{}, err
	}
	for _, one := range reply.Answers {
		if one.QuestionID == styleQuestion {
			if len(one.OptionIDs) != 1 {
				return infographicChoice{}, errors.New("the style question takes exactly one choice; no infographic was made")
			}
			return infographicChoice{style: one.OptionIDs[0], canvas: shape}, nil
		}
	}
	return infographicChoice{}, errors.New("the answer does not choose a style; no infographic was made")
}

// The bounds of a plan.
const (
	maxPlanChars = 8000
	// minPlanItems is the least a plan must hold to be an infographic and
	// not a caption.
	minPlanItems = 3
)

const infographicInstructions = `Infographic: the material as one designed image.

Pass the plan to save_artifact as: format "infographic", a title, and markdown.

markdown is the plan of what the image must say, and nothing else:

# The title on the image

## First section
- Short label: one key fact
- 42% of something

## Second section
- Another short label

- One level-one heading, the title. Then 3 to 6 level-two sections, each with 1 to 4 bullets.
- Every heading and bullet is printed on the image, so keep each to a few words; put the numbers that matter in, exactly as the material gives them. No paragraphs.
- Up to 8000 characters. Facts only, from the task's material: do not describe colours, layout, style or canvas size. The user picks the style.
- Do not call any other tool for an infographic. save_artifact asks the user to choose a visual style, then draws, checks and saves the image; that can take a few minutes.
- If it answers a warning, pass the warning on in your answer. If the user closed the style question, say so and stop.`

// Infographic is a checked plan, ready to draw.
type Infographic struct {
	Title    string
	Markdown string
	// Items are the headings, labels and numbers a reader should find on
	// the image, in order.
	Items []string
}

// NewInfographic checks a plan. Like every other kind, all of what is wrong
// is reported at once.
func NewInfographic(title, markdown string) (Infographic, error) {
	var problems []string
	title = checkTitle(&problems, title)
	markdown = strings.TrimSpace(strings.ReplaceAll(markdown, "\r\n", "\n"))
	switch {
	case markdown == "":
		problems = append(problems, "markdown is empty")
	case utf8.RuneCountInString(markdown) > maxPlanChars:
		problems = append(problems, fmt.Sprintf("markdown is longer than %d characters", maxPlanChars))
	}
	if bad := badCharacter(markdown); bad != "" {
		problems = append(problems, "markdown contains "+bad)
	}
	items := plannedItems(markdown, maxCheckedItems)
	if markdown != "" && len(items) < minPlanItems {
		problems = append(problems, "markdown is not a plan: it needs a # title, ## sections and short bullets")
	}
	if len(problems) > 0 {
		return Infographic{}, refusal("the infographic was not made", problems)
	}
	return Infographic{Title: title, Markdown: markdown, Items: items}, nil
}

// maxCheckedItems is how many planned items are read back from the image.
const maxCheckedItems = 30

// maxItemLabelChars is the longest bullet taken as a label. A longer one is
// a sentence, which an image model will not print word for word and which
// would count against a good image.
const maxItemLabelChars = 80

var (
	planHeading  = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.+?)\s*#*\s*$`)
	planListItem = regexp.MustCompile(`^\s*(?:[-*+]|\d{1,3}[.)])\s+(.+)$`)
	// No sign: in "20-25%" the hyphen is a range, and a figure read back as
	// "5" where the plan said "-5" is still the figure.
	planNumber   = regexp.MustCompile(`\d[\d,]*(?:\.\d+)?\s?%?`)
	planLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	planEmphasis = regexp.MustCompile("[*_`]+")
)

// plainLabel is a heading or bullet without its Markdown.
func plainLabel(text string) string {
	text = planLink.ReplaceAllString(text, "$1")
	return strings.Join(strings.Fields(planEmphasis.ReplaceAllString(text, "")), " ")
}

// plannedItems is what a reader should find on the image: each heading,
// each short bullet's label (the part before a colon), and each number, in
// the plan's order and once each.
func plannedItems(markdown string, limit int) []string {
	var items []string
	seen := map[string]bool{}
	add := func(item string) {
		if item != "" && !seen[item] {
			seen[item] = true
			items = append(items, item)
		}
	}
	for _, line := range strings.Split(markdown, "\n") {
		// text is where the line's figures are looked for: the whole line,
		// or for a heading or a bullet what follows its marker, so that a
		// numbered list's "1." is not taken for a figure on the page.
		text := line
		if heading := planHeading.FindStringSubmatch(line); heading != nil {
			text = heading[1]
			add(plainLabel(text))
		} else if listed := planListItem.FindStringSubmatch(line); listed != nil {
			text = listed[1]
			label, _, _ := strings.Cut(plainLabel(text), ":")
			if label = strings.TrimSpace(label); utf8.RuneCountInString(label) <= maxItemLabelChars {
				add(label)
			}
		}
		for _, at := range planNumber.FindAllStringIndex(text, -1) {
			// A number that is the tail of a word or of another number
			// ("H2O", "v1.2") is not a figure on its own.
			if at[0] > 0 {
				before, _ := utf8.DecodeLastRuneInString(text[:at[0]])
				if before == '.' || before == '_' || unicode.IsLetter(before) || unicode.IsDigit(before) {
					continue
				}
			}
			// "3, 4 and 5" is three numbers, not "3," and "4".
			add(strings.TrimRight(strings.TrimSpace(text[at[0]:at[1]]), ","))
		}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

// infographicTask opens every infographic prompt.
const infographicTask = "Create a polished, complete infographic from the supplied factual source. Choose the clearest visual hierarchy and composition. Summarize or omit secondary detail when necessary for readability. Keep all important content fully visible within the canvas, preserve factual accuracy, and follow the selected visual style."

// infographicPrompt is what the image model is given: the task, the plan,
// the style's words, the canvas's shape when one was asked for, and on a
// second attempt what was wrong with the first.
func infographicPrompt(plan string, style visualStyle, shape canvas, findings []string) string {
	var b strings.Builder
	b.WriteString(infographicTask + "\n\nCONTENT\n" + plan + "\n\nVISUAL STYLE\n" + style.description)
	if shape.words != "" {
		b.WriteString("\n\n" + shape.words)
	}
	if len(findings) > 0 {
		b.WriteString("\n\nREPAIR ALL OF THESE FINDINGS")
		for _, finding := range findings {
			b.WriteString("\n- " + finding)
		}
	}
	return b.String()
}

// TextCheck is what reading an infographic back found. It is saved with the
// artifact (metadata.generation.text_check), in these spellings.
type TextCheck struct {
	// Score is the share of planned items the image shows correctly, nil
	// when the image could not be read back.
	Score   *float64 `json:"score"`
	Checked int      `json:"checked"`
	Missing []string `json:"missing"`
	Garbled []string `json:"garbled"`
	Passed  bool     `json:"passed"`
	// UnavailableReason says why there is no score, or is null.
	UnavailableReason *string `json:"unavailable_reason"`
}

// passThreshold is the share of planned items that must read correctly.
const passThreshold = 0.8

// uncheckedText is a check that could not be made. It passes, because an
// image nobody could read back is not known to be wrong; the artifact
// records that it was not verified and why.
func uncheckedText(reason string) TextCheck {
	return TextCheck{Missing: []string{}, Garbled: []string{}, Passed: true, UnavailableReason: &reason}
}

// findings are a failed check's problems as lines for the repair prompt.
func (c TextCheck) findings() []string {
	var out []string
	for _, item := range c.Missing {
		out = append(out, `Missing from the image: "`+item+`"`)
	}
	for _, item := range c.Garbled {
		out = append(out, "Garbled in the image: "+item)
	}
	return out
}

// warning is the sentence saved with an infographic whose text did not read
// back well enough, or "".
func (c TextCheck) warning(attempts int) string {
	if c.Passed || c.Score == nil {
		return ""
	}
	problems := append(append([]string{}, c.Missing...), c.Garbled...)
	if len(problems) > 8 {
		problems = problems[:8]
	}
	after := "."
	if attempts > 1 {
		after = " after one regeneration."
	}
	return fmt.Sprintf("Only %d%% of the planned text reads correctly in the image%s Check: %s", int(math.Round(*c.Score*100)), after, strings.Join(problems, "; "))
}

// readingPrompt asks the reading model about each planned item.
func readingPrompt(items []string) string {
	var b strings.Builder
	b.WriteString(`This infographic was generated from a plan. Read the image and, for each planned item below, say whether it appears legibly and correctly ("present"), does not appear ("missing"), or appears misspelled, distorted or with a different number ("garbled"; give what you see). Wording may differ slightly; numbers must match exactly.` + "\n\nPlanned items:\n")
	for i, item := range items {
		b.WriteString(strconv.Itoa(i+1) + ". " + item + "\n")
	}
	b.WriteString("\n" + `Reply with only JSON: {"items": [{"id": 1, "status": "present", "seen": null}, ...]} with one entry per planned item.`)
	return b.String()
}

// maxSeenChars bounds what the reading model says it saw in place of an
// item; it is saved and shown.
const maxSeenChars = 200

// checkText reads the image back and compares it with the plan. It never
// fails: a reading that cannot be had is a check that could not be made,
// which is recorded, and the infographic is still the person's.
func (s Specialist) checkText(ctx context.Context, userID string, picture []byte, items []string) TextCheck {
	if len(items) == 0 {
		return uncheckedText("no planned text to compare")
	}
	small, err := readingCopy(picture)
	if err != nil {
		return uncheckedText("the image could not be prepared for reading")
	}
	answer, err := s.Reader.Read(ctx, userID, readingPrompt(items), small, "image/jpeg")
	var unavailable *SightUnavailable
	switch {
	case errors.Is(err, ErrNoImageReader):
		return uncheckedText("no vision model configured")
	case errors.As(err, &unavailable):
		// The account's chosen model cannot be called: its own sentence,
		// which says what to do, rather than "did not answer".
		return uncheckedText(unavailable.Sentence)
	}
	if err != nil {
		s.Saver.Logger.LogAttrs(ctx, slog.LevelWarn, "an infographic's text could not be read back", slog.String("error", err.Error()))
		return uncheckedText("the vision model did not answer")
	}
	var reading struct {
		Items []struct {
			ID     int     `json:"id"`
			Status string  `json:"status"`
			Seen   *string `json:"seen"`
		} `json:"items"`
	}
	// Models wrap JSON in a code fence or a sentence however they are
	// asked not to; the object is what lies between the outer braces.
	open, shut := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if open < 0 || shut < open || json.Unmarshal([]byte(answer[open:shut+1]), &reading) != nil {
		return uncheckedText("the vision model's answer was not readable")
	}
	status := map[int]int{}
	for i, read := range reading.Items {
		status[read.ID] = i
	}
	check := TextCheck{Checked: len(items), Missing: []string{}, Garbled: []string{}}
	for i, item := range items {
		at, read := status[i+1]
		switch {
		// An item the model said nothing about was not seen.
		case !read || reading.Items[at].Status == "missing":
			check.Missing = append(check.Missing, item)
		case reading.Items[at].Status == "garbled":
			entry := `"` + item + `"`
			if seen := reading.Items[at].Seen; seen != nil && strings.TrimSpace(*seen) != "" {
				entry += ` (reads "` + oneLineOf(*seen, maxSeenChars) + `")`
			}
			check.Garbled = append(check.Garbled, entry)
		}
	}
	score := math.Round(float64(len(items)-len(check.Missing)-len(check.Garbled))/float64(len(items))*100) / 100
	check.Score, check.Passed = &score, score >= passThreshold
	return check
}

// oneLineOf is text on one line, cut to max characters.
func oneLineOf(text string, max int) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > max {
		text = string(runes[:max])
	}
	return text
}

// The bounds of an image accepted as an infographic. Past them the file is
// either not a page anyone can read or not safe to decode.
const (
	minInfographicEdge   = 256
	maxInfographicEdge   = 8192
	maxInfographicPixels = 40_000_000
	// maxReadingEdge is the longest edge of the copy sent to be read.
	maxReadingEdge = 2048
)

// normalisedPNG is the image as an opaque PNG, with its size. The size is
// read from the header and checked before the pixels are decoded, so an
// image that claims a billion pixels costs nothing.
func normalisedPNG(data []byte) (out []byte, width, height int, err error) {
	header, kind, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		// A WebP is recognised by imageType and not decodable here: the
		// standard library reads PNG and JPEG.
		return nil, 0, 0, fmt.Errorf("%w: it is not a PNG or a JPEG this server can read", errUnusableImage)
	}
	width, height = header.Width, header.Height
	if width < minInfographicEdge || height < minInfographicEdge || width > maxInfographicEdge || height > maxInfographicEdge || width*height > maxInfographicPixels {
		return nil, 0, 0, fmt.Errorf("%w: a %s of %dx%d pixels is outside what an infographic may be", errUnusableImage, kind, width, height)
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("%w: it could not be decoded", errUnusableImage)
	}
	// Onto white: the viewer and a printed page are both white, and a
	// transparent infographic on the app's dark theme is unreadable.
	flat := image.NewRGBA(decoded.Bounds())
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), decoded, decoded.Bounds().Min, draw.Over)
	var buffer bytes.Buffer
	// flat is passed as the *image.RGBA it is: the encoder has a fast path
	// for that type and, finding every pixel opaque, writes three channels
	// instead of four. (Wrapped in another type it was read a pixel at a
	// time: 100 ms and 1.57 million allocations for a 1536x1024 page,
	// against 51 ms and 58.)
	if err := png.Encode(&buffer, flat); err != nil {
		return nil, 0, 0, err
	}
	return buffer.Bytes(), width, height, nil
}

// readingCopy is the image as a JPEG no longer than maxReadingEdge on a
// side, which is what the reading model is sent: a smaller request, and no
// more detail than it uses.
func readingCopy(pngData []byte) ([]byte, error) {
	decoded, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, err
	}
	bounds := decoded.Bounds()
	longest := max(bounds.Dx(), bounds.Dy())
	var picture image.Image = decoded
	if longest > maxReadingEdge {
		// Shrink by a whole factor, averaging each block: text stays
		// legible, which taking every nth pixel would not keep.
		picture = shrunk(decoded, (longest+maxReadingEdge-1)/maxReadingEdge)
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, picture, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// shrunk is the image reduced by factor on each side, each pixel the mean
// of the block it replaces.
func shrunk(source image.Image, factor int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx()/factor, bounds.Dy()/factor
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	area := uint32(factor * factor)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var r, g, b uint32
			for dy := 0; dy < factor; dy++ {
				for dx := 0; dx < factor; dx++ {
					pr, pg, pb, _ := source.At(bounds.Min.X+x*factor+dx, bounds.Min.Y+y*factor+dy).RGBA()
					r, g, b = r+pr>>8, g+pg>>8, b+pb>>8
				}
			}
			out.SetRGBA(x, y, color.RGBA{R: uint8(r / area), G: uint8(g / area), B: uint8(b / area), A: 255})
		}
	}
	return out
}

// drawn is one attempt at an infographic.
type drawn struct {
	png           []byte
	width, height int
	check         TextCheck
}

// better reports whether attempt b reads better than attempt a. An attempt
// that could not be read back is not known to be better than one that was.
func better(a, b drawn) bool {
	return b.check.Score != nil && (a.check.Score == nil || *b.check.Score > *a.check.Score)
}

// draw makes one attempt on the canvas asked for: the image, normalised and
// read back.
func (s Specialist) draw(ctx context.Context, owner Owner, plan Infographic, style visualStyle, shape canvas, findings []string) (drawn, error) {
	picture, err := s.Images.Generate(ctx, infographicPrompt(plan.Markdown, style, shape, findings), shape.size)
	if err != nil {
		return drawn{}, err
	}
	data, width, height, err := normalisedPNG(picture.Data)
	if err != nil {
		return drawn{}, imageFailure(ctx, err)
	}
	return drawn{png: data, width: width, height: height, check: s.checkText(ctx, owner.UserID, data, plan.Items)}, nil
}

// maxInfographicAttempts is how many times one infographic is drawn: once,
// and once more if the first one's text does not read back.
const maxInfographicAttempts = 2

// refusedInfographicNote follows a refusal's sentence in what the model
// reads.
const refusedInfographicNote = " The infographic was not made; tell the requester exactly that."

// InfographicSaved is what save_artifact answers for an infographic: the
// card's fields (Saved), and for the model the style used and any warning.
type InfographicSaved struct {
	Saved
	Style   string `json:"style"`
	Warning string `json:"warning,omitempty"`
}

// infographic is save_artifact for the format "infographic". It is run
// twice for one call: the first run asks the style question and the second,
// which has the answer, makes the image.
func (s Specialist) infographic(ctx context.Context, owner Owner, call tool.Call, title string, markdown *string) (string, error) {
	if markdown == nil {
		return "", errors.New("the infographic was not made: markdown is missing")
	}
	// Checked on both runs, and before the question: a person is not asked
	// to choose a style for a plan that will then be refused.
	plan, err := NewInfographic(title, *markdown)
	if err != nil {
		return "", err
	}
	if call.Answer == nil {
		return "", tool.AskUser(styleQuestionPayload())
	}
	choice, err := chosenStyle(call.Answer)
	if err != nil {
		return "", err
	}
	requested := choice.style
	style, err := resolveStyle(requested, plan.Title+"\n"+plan.Markdown)
	if err != nil {
		return "", fmt.Errorf("%w; no infographic was made", err)
	}

	best, err := s.draw(ctx, owner, plan, style, choice.canvas, nil)
	if err != nil {
		return "", s.imageError(ctx, owner, err, "the infographic", refusedInfographicNote)
	}
	attempts := 1
	if !best.check.Passed {
		attempts = maxInfographicAttempts
		second, err := s.draw(ctx, owner, plan, style, choice.canvas, best.check.findings())
		switch {
		case ctx.Err() != nil:
			return "", ctx.Err()
		case err != nil:
			// The first image exists and was paid for. It is saved with
			// its warning; the failed repair is the operator's to read.
			s.Saver.Logger.LogAttrs(ctx, slog.LevelWarn, "an infographic's second attempt failed; the first is kept",
				slog.String("error", err.Error()), slog.String("user_id", owner.UserID))
		case better(best, second):
			best = second
		}
	}

	saved, err := s.Saver.SaveInfographic(ctx, owner, call.ID, plan, InfographicRecord{
		RequestedStyle: requested, Style: style.id, Orientation: choice.canvas.id, Model: s.Images.Name(),
		Width: best.width, Height: best.height, Attempts: attempts, Check: best.check,
	}, best.png)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		s.Saver.Logger.ErrorContext(ctx, "an infographic could not be saved", "error", err.Error(), "user_id", owner.UserID)
		return "", errors.New("the infographic was drawn but could not be saved because of a storage error; it was not saved")
	}
	out, err := json.Marshal(InfographicSaved{Saved: saved, Style: style.label, Warning: best.check.warning(attempts)})
	return string(out), err
}

// InfographicRecord is what is kept about how an infographic was made.
type InfographicRecord struct {
	// RequestedStyle is what the person picked ("auto" or a style); Style
	// is the style drawn.
	RequestedStyle string
	Style          string
	// Orientation is the canvas asked for (landscape, portrait, square),
	// or "" when none was and the model drew its own.
	Orientation string
	// Model is the proxy's name for the image model.
	Model         string
	Width, Height int
	Attempts      int
	Check         TextCheck
}

// SaveInfographic stores a drawn infographic as a new artifact: the PNG as
// its file, the plan as its document, and in its metadata whether the text
// was verified and how the image was made.
func (s Saver) SaveInfographic(ctx context.Context, owner Owner, toolCallID string, plan Infographic, record InfographicRecord, pngData []byte) (Saved, error) {
	verification := map[string]any{
		// "verified" is "the text was read back", whatever it found; what
		// it found is the warning and the text_check below.
		"verified": record.Check.UnavailableReason == nil,
		"reason":   record.Check.UnavailableReason,
	}
	if warning := record.Check.warning(record.Attempts); warning != "" {
		verification["warnings"] = []string{warning}
	}
	generation := map[string]any{
		"question_preset_id": styleQuestionID, "question_preset_version": styleQuestionVersion,
		"requested_style_id": record.RequestedStyle, "resolved_style_id": record.Style,
		"provider_model": record.Model, "width": record.Width, "height": record.Height,
		"attempts": record.Attempts, "text_check": record.Check,
	}
	// Only an orientation that was asked for is recorded: without one the
	// width and height say what the model chose.
	if record.Orientation != "" {
		generation["orientation"] = record.Orientation
	}
	metadata, err := json.Marshal(map[string]any{"verification": verification, "generation": generation})
	if err != nil {
		return Saved{}, err
	}
	return s.save(ctx, NewArtifact{
		Owner: owner, ToolCallID: toolCallID, Title: plan.Title, Format: FormatInfographic,
		Markdown: plan.Markdown, Metadata: metadata,
		Filename: slug(plan.Title, FormatInfographic) + ".png", MimeType: "image/png", Data: pngData,
	})
}
