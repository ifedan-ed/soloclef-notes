package deliverables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_studio/figurekit"
	"git.danvics.com/danvics/soloclef/soloclef_studio/posterrender"
)

// An infographic is a poster drawn by code (soloclef_studio/posterrender):
// the model writes its content as structured data (a title, a line under
// it, key numbers, sections of short points, figures as draw_figure specs,
// a takeaway, the sources), which is checked and refused in words like
// every other artifact, and trusted code lays it out in the look and on the
// page the person picks, draws it as one SVG, and makes its PNG with resvg.
// No picture model is called: an infographic costs the tokens of its
// content, its text is exactly what was written, and its figures place
// every number where the data says.
//
// It is one call of save_artifact with the format "infographic":
//
//  1. The content is checked, and the call pauses with one question card
//     asking for a style and an orientation (tool.AskUser). Nothing has
//     been drawn.
//  2. With the answer the poster is laid out. Content that cannot be made
//     to fit (after smaller type and another arrangement were tried) is
//     refused naming the item and the word; the model shortens it and calls
//     save_artifact again, and the person's choice stands for the rest of
//     the turn, so they are not asked twice.
//  3. The poster is saved: the PNG as the artifact's file, the SVG it was
//     drawn from beside it (carrying its faces, so it draws exactly in any
//     browser), and the checked content in its metadata, so it can be
//     drawn again.
//
// What this does better than the image model it replaces: it cannot fail
// to return a picture (the image route answered none about one call in
// four), it never misspells a word or a number, so nothing needs reading
// back by a second model, and the same content always draws the same
// poster.

// The question's identity. The page sends both back with the answer, and an
// answer to some other question, or to an older set of styles, is not taken
// as an answer to this one. Version 2: the styles of the drawn poster, and
// the orientation asked in the same card.
const (
	styleQuestionID      = "infographic.visual-style"
	styleQuestionVersion = 2
	// styleQuestion and orientationQuestion are the two questions' own
	// ids within the payload.
	styleQuestion       = "visual-style"
	orientationQuestion = "orientation"
	autoStyle           = "auto"
	// defaultAutoStyle is what "auto" becomes for content none of the
	// signals below recognises.
	defaultAutoStyle = "sketch-note"
)

// styleLabels are the styles as a person reads them, in the site's sentence
// case (the web's own style picker says "Sketch note" too).
var styleLabels = map[string]string{
	"sketch-note": "Sketch note", "editorial": "Editorial", "instructional": "Instructional",
	"bento-grid": "Bento grid", "scientific": "Scientific", "professional": "Professional",
}

// styleSignals are the words in a poster that make "auto" pick a style,
// tried in autoOrder: content with both "study" and "research" in it is a
// study aid first.
var (
	styleSignals = map[string][]string{
		"sketch-note":   {"study", "notes", "brainstorming", "teaching", "concept", "explain", "revision"},
		"instructional": {"steps", "procedure", "instructions", "checklist", "workflow"},
		"scientific":    {"experiment", "research", "trial", "clinical", "scientific", "mechanism"},
		"professional":  {"company", "business", "quarterly", "revenue", "market"},
		"editorial":     {"news", "story", "history", "magazine", "feature", "mission"},
		"bento-grid":    {"dashboard", "highlights", "summary", "metrics", "stats"},
	}
	autoOrder = []string{"sketch-note", "instructional", "scientific", "professional", "editorial", "bento-grid"}
)

var posterWord = regexp.MustCompile(`[a-z0-9]+`)

// resolveStyle is the style an answer means. "auto" is settled from the
// poster's own words, the same way every time, so that drawing the same
// content again gives the same poster.
func resolveStyle(requested, words string) (string, error) {
	if requested != autoStyle {
		if posterrender.About(requested) == "" {
			return "", fmt.Errorf("%q is not one of the infographic styles", requested)
		}
		return requested, nil
	}
	found := map[string]bool{}
	for _, word := range posterWord.FindAllString(strings.ToLower(words), -1) {
		found[word] = true
	}
	for _, id := range autoOrder {
		for _, signal := range styleSignals[id] {
			if found[signal] {
				return id, nil
			}
		}
	}
	return defaultAutoStyle, nil
}

// InfographicStyles is every style id an infographic may be asked for,
// "auto" first: what a dialog that asks the question itself may send.
func InfographicStyles() []string { return append([]string{autoStyle}, posterrender.Styles()...) }

// InfographicOrientations is every orientation an infographic may be
// asked for, the default first.
func InfographicOrientations() []string {
	ids := make([]string, len(posterrender.Canvases))
	for i, c := range posterrender.Canvases {
		ids[i] = c.ID
	}
	return ids
}

// orientationLabels are the orientations as a person reads them.
var orientationLabels = map[string]string{"landscape": "Landscape", "portrait": "Portrait", "square": "Square"}

// The question as the page's question card reads it
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
	Origin    questionOrigin  `json:"origin"`
	Questions []askedQuestion `json:"questions"`
}

// styleQuestionPayload is the question a paused infographic asks: the
// style, with a picture of each, and the page's orientation. Its text is
// labels and nothing else.
func styleQuestionPayload() structuredQuestion {
	styles := []questionOption{{ID: autoStyle, Label: "Auto", Description: "Chosen from the content", PreviewAsset: "infographic-style/" + autoStyle}}
	for _, id := range posterrender.Styles() {
		styles = append(styles, questionOption{ID: id, Label: styleLabels[id], Description: posterrender.About(id), PreviewAsset: "infographic-style/" + id})
	}
	var orientations []questionOption
	for _, id := range InfographicOrientations() {
		orientations = append(orientations, questionOption{ID: id, Label: orientationLabels[id]})
	}
	one := func(id, prompt, presentation string, options []questionOption) askedQuestion {
		return askedQuestion{ID: id, Prompt: prompt, InputType: "single_select", Presentation: presentation,
			Required: true, MinimumSelections: 1, MaximumSelections: 1, Options: options}
	}
	return structuredQuestion{
		Type: "structured_question", Version: 1, Title: "Infographic",
		Origin: questionOrigin{Kind: "preset", PresetID: styleQuestionID, PresetVersion: styleQuestionVersion},
		Questions: []askedQuestion{
			one(styleQuestion, "Style", "visual_cards", styles),
			one(orientationQuestion, "Orientation", "list", orientations),
		},
	}
}

// errStyleCancelled is the person closing the question without choosing.
var errStyleCancelled = errors.New("the user closed the style question without choosing, so no infographic was made. Tell the requester exactly that, and do not try again unless asked")

// infographicChoice is what the answer chose: a style id (or "auto") and
// an orientation.
type infographicChoice struct {
	style       string
	orientation string
}

// chosenStyle reads the answer: the option picked for each question. A
// studio run's answer (StyleAnswer) carries the orientation its dialog
// chose beside the style instead of as a second answer.
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
		// version no longer has: drawing "something" for it would be a
		// choice the person did not make.
		return infographicChoice{}, errors.New("the answer is not an answer to the infographic style question; no infographic was made")
	}
	var choice infographicChoice
	for _, one := range reply.Answers {
		var into *string
		switch one.QuestionID {
		case styleQuestion:
			into = &choice.style
		case orientationQuestion:
			into = &choice.orientation
		default:
			continue
		}
		if len(one.OptionIDs) != 1 {
			return infographicChoice{}, fmt.Errorf("the %s question takes exactly one choice; no infographic was made", one.QuestionID)
		}
		*into = one.OptionIDs[0]
	}
	if reply.Orientation != "" {
		if choice.orientation != "" && choice.orientation != reply.Orientation {
			return infographicChoice{}, errors.New("the answer names two orientations; no infographic was made")
		}
		choice.orientation = reply.Orientation
	}
	switch {
	case choice.style == "":
		return infographicChoice{}, errors.New("the answer does not choose a style; no infographic was made")
	case choice.orientation == "":
		return infographicChoice{}, errors.New("the answer does not choose an orientation; no infographic was made")
	}
	if _, known := posterrender.CanvasNamed(choice.orientation); !known {
		return infographicChoice{}, fmt.Errorf("%q is not an infographic orientation; no infographic was made", choice.orientation)
	}
	if choice.style != autoStyle && posterrender.About(choice.style) == "" {
		return infographicChoice{}, fmt.Errorf("%q is not one of the infographic styles; no infographic was made", choice.style)
	}
	return choice, nil
}

// StyleAnswer is the reply to the question that chooses the given style
// and orientation, in the shape the page's question card sends, with the
// orientation beside it.
//
// It exists for a run with nobody watching: a notebook's studio asks for
// the style and the orientation in its own dialog, before the run, so when
// the infographic pauses to ask, the runner answers with what the person
// already chose. Writing the answer here, beside chosenStyle which reads it,
// keeps the two in one place; a style or an orientation that does not
// exist is refused instead of drawn.
func StyleAnswer(style, orientation string) (json.RawMessage, error) {
	type answer struct {
		QuestionID string   `json:"question_id"`
		OptionIDs  []string `json:"option_ids"`
	}
	raw, err := json.Marshal(struct {
		Type          string   `json:"type"`
		PresetID      string   `json:"preset_id"`
		PresetVersion int      `json:"preset_version"`
		Orientation   string   `json:"orientation"`
		Answers       []answer `json:"answers"`
	}{"respond", styleQuestionID, styleQuestionVersion, orientation, []answer{{styleQuestion, []string{style}}}})
	if err != nil {
		return nil, err
	}
	if _, err := chosenStyle(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// The instructions, written from posterrender's limits (told in words) and
// figurekit's catalogue of figures, so they can never ask for what the
// renderer refuses.
var infographicInstructions = `Infographic: the material as one poster, drawn by the application.

Pass it to save_artifact as: format "infographic", a title (the poster's heading, ` + posterrender.UpTo(posterrender.MaxTitle) + `), and poster.

poster is the content and nothing else; the user picks the look and the page shape, and the application lays it out:
{"subtitle": "...", "key_numbers": [{"value": "1 in 11", "label": "..."}], "sections": [{"heading": "...", "points": ["...", "..."], "figure": {...}}], "takeaway": "...", "sources": "..."}

- subtitle (optional): one line under the title, ` + posterrender.UpTo(posterrender.MaxSubtitle) + `.
- key_numbers (optional, at most ` + strconv.Itoa(posterrender.MaxNumbers) + `): the figures that matter most. value is the number itself as the material gives it ("42%", "1 in 11", "3 days"), ` + posterrender.UpTo(posterrender.MaxNumberValue) + `; label says what it counts, ` + posterrender.UpTo(posterrender.MaxNumberLabel) + `.
- sections: ` + strconv.Itoa(posterrender.MinSections) + ` to ` + strconv.Itoa(posterrender.MaxSections) + `, in reading order. heading ` + posterrender.UpTo(posterrender.MaxHeading) + `; points: up to ` + strconv.Itoa(posterrender.MaxPoints) + ` short points, each ` + posterrender.UpTo(posterrender.MaxPoint) + `; figure (optional): a chart or a diagram, as below. A section has at least one point or a figure.
- takeaway (optional): the one thing to remember, ` + posterrender.UpTo(posterrender.MaxTakeaway) + `. sources (optional): where the facts come from, ` + posterrender.UpTo(posterrender.MaxSources) + `.
- Plain words on one line each: no Markdown, no HTML. Facts only, from the task's material; put the numbers that matter in exactly as the material gives them, and never invent one.
- How much fits: a landscape or portrait page holds about four sections of three short points with one or two figures and three key numbers; a square page about two thirds of that. Fewer, shorter points read better than many. If the poster is refused because it does not fit, shorten exactly what the refusal names and call save_artifact again: the user's choice of look and page stands, and they are not asked again.
- Figures: at most ` + strconv.Itoa(posterrender.MaxFigures) + `, only where one shows something the points cannot: numbers or a trend (a chart), a process, a cycle, a timeline or a comparison (a diagram). Keep each one small, as a poster's figure is: a flowchart, pathway or timeline of at most five steps, a chart of at most six bars or points, a table of at most four rows. A figure takes the room of about four points, so its section needs few. Each is {"type", "title", "alt", "caption"?, "spec"}, exactly as described below.
- Do not call any other tool for an infographic. save_artifact asks the user to choose a look and a page shape, then draws and saves the poster.
- If the user closed the question, say so and stop.

` + figurekit.Catalogue()

// Infographic is checked content, ready to draw.
type Infographic struct {
	Poster posterrender.Poster
}

// NewInfographic checks the content: the title as every artifact's title
// is checked, then the poster against its own limits and its figures.
// Everything wrong with the poster is reported at once.
func NewInfographic(title string, raw json.RawMessage) (Infographic, error) {
	var problems []string
	title = checkTitle(&problems, title)
	if len(problems) > 0 {
		return Infographic{}, refusal("the infographic was not made", problems)
	}
	poster, err := posterrender.Decode(raw)
	if err != nil {
		return Infographic{}, err
	}
	poster.Title = title
	if poster, err = posterrender.Check(poster); err != nil {
		return Infographic{}, err
	}
	return Infographic{Poster: poster}, nil
}

// PosterRasteriser makes a drawn poster's PNG (posterrender.Rasteriser).
type PosterRasteriser interface {
	PNG(ctx context.Context, d posterrender.Drawn) ([]byte, error)
}

// refusedInfographicNote follows a refusal of the layout in what the model
// reads: the person's choice stands, so it corrects and calls again.
const refusedInfographicNote = " The user's choice of style and orientation stands: correct exactly this and call save_artifact again with the whole poster; they will not be asked again."

// InfographicSaved is what save_artifact answers for an infographic: the
// card's fields (Saved), and for the model the style it was drawn in.
type InfographicSaved struct {
	Saved
	Style string `json:"style"`
}

// infographic is save_artifact for the format "infographic". It runs twice
// for the call that asks: once to ask, once with the answer. chosen is the
// turn's answer, kept for a later call of the same turn that corrects a
// poster the layout refused.
func (s Specialist) infographic(ctx context.Context, owner Owner, call tool.Call, title string, raw json.RawMessage, chosen *infographicChoice) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("the infographic was not made: poster is missing")
	}
	// Checked on every run, and before the question: a person is not
	// asked to choose for content that will then be refused.
	plan, err := NewInfographic(title, raw)
	if err != nil {
		return "", err
	}
	choice := *chosen
	switch {
	case call.Answer != nil:
		if choice, err = chosenStyle(call.Answer); err != nil {
			return "", err
		}
		*chosen = choice
	case choice.style == "":
		return "", tool.AskUser(styleQuestionPayload())
	}
	style, err := resolveStyle(choice.style, posterrender.Markdown(plan.Poster))
	if err != nil {
		return "", fmt.Errorf("%w; no infographic was made", err)
	}
	started := time.Now()
	drawn, err := posterrender.Draw(plan.Poster, style, choice.orientation)
	var refused *posterrender.Refusal
	if errors.As(err, &refused) {
		return "", errors.New(refused.Error() + "." + refusedInfographicNote)
	}
	if err != nil {
		return "", s.posterFault(ctx, owner, err)
	}
	png, err := s.Posters.PNG(ctx, drawn)
	if err != nil {
		return "", s.posterFault(ctx, owner, err)
	}
	// Speed is measured where it is spent, for the operator.
	s.Saver.Logger.InfoContext(ctx, "an infographic was drawn", "style", style, "orientation", choice.orientation,
		"milliseconds", time.Since(started).Milliseconds(), "png_bytes", len(png), "svg_bytes", len(drawn.Kept))
	saved, err := s.Saver.SaveInfographic(ctx, owner, call.ID, plan, InfographicRecord{
		RequestedStyle: choice.style, Style: style, Orientation: choice.orientation,
	}, drawn, png)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		s.Saver.Logger.ErrorContext(ctx, "an infographic could not be saved", "error", err.Error(), "user_id", owner.UserID)
		return "", errors.New("the infographic was drawn but could not be saved because of a storage error; it was not saved")
	}
	out, err := json.Marshal(InfographicSaved{Saved: saved, Style: styleLabels[style]})
	return string(out), err
}

// posterFault is a poster that could not be drawn for a reason that is not
// the content's: logged with its cause, a plain sentence to the model.
func (s Specialist) posterFault(ctx context.Context, owner Owner, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.Saver.Logger.ErrorContext(ctx, "an infographic could not be drawn", "error", err.Error(), "user_id", owner.UserID)
	return errors.New("the infographic could not be drawn because of a fault in the application; it was not made. Tell the requester exactly that")
}

// InfographicRecord is what is kept about how an infographic was drawn.
type InfographicRecord struct {
	// RequestedStyle is what the person picked ("auto" or a style); Style
	// is the style drawn.
	RequestedStyle string
	Style          string
	Orientation    string
}

// svgMime is the type of an infographic's SVG.
const svgMime = "image/svg+xml"

// SaveInfographic stores a drawn poster as a new artifact: the PNG as its
// file (what every viewer and every installed phone build shows), the SVG
// as its preview (what the website shows, sharp at any zoom), the poster
// as Markdown as its document, and the checked content in its metadata.
func (s Saver) SaveInfographic(ctx context.Context, owner Owner, toolCallID string, plan Infographic, record InfographicRecord, drawn posterrender.Drawn, pngData []byte) (Saved, error) {
	content, err := posterrender.Content(plan.Poster)
	if err != nil {
		return Saved{}, err
	}
	metadata, err := json.Marshal(map[string]any{
		// Every word on the poster is the checked content, set by code:
		// there is nothing to read back.
		"verification": map[string]any{"verified": true, "reason": nil},
		"generation": map[string]any{
			"renderer":           "poster",
			"question_preset_id": styleQuestionID, "question_preset_version": styleQuestionVersion,
			"requested_style_id": record.RequestedStyle, "resolved_style_id": record.Style,
			"orientation": record.Orientation, "width": drawn.Width, "height": drawn.Height,
		},
		"poster": json.RawMessage(content),
	})
	if err != nil {
		return Saved{}, err
	}
	name := slug(plan.Poster.Title, FormatInfographic)
	return s.save(ctx, NewArtifact{
		Owner: owner, ToolCallID: toolCallID, Title: plan.Poster.Title, Format: FormatInfographic,
		Markdown: posterrender.Markdown(plan.Poster), Metadata: metadata,
		Filename: name + ".png", MimeType: "image/png", Data: pngData,
		Preview: []byte(drawn.Kept), PreviewFilename: name + ".svg", PreviewMimeType: svgMime,
		PreviewKey: func(artifactID int64) string {
			return "artifacts/" + owner.UserID + "/" + strconv.FormatInt(artifactID, 10) + "/preview/" + s.NewID() + ".svg"
		},
	})
}
