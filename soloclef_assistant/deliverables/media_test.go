package deliverables

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
	"git.danvics.com/danvics/soloclef/soloclef_agent/uistream"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_studio/posterrender"
)

// The stand-ins the specialist's fixture is built with (deliverables_test.go,
// ledger): the posters' rasteriser and a video queue. Each remembers what it
// was asked and answers what a test set.

// easel is a PosterRasteriser: it keeps every poster it is asked to draw and
// answers a small real PNG, so the save stores bytes a viewer could open.
type easel struct {
	drawn []posterrender.Drawn
	fail  error
}

func (e *easel) PNG(_ context.Context, d posterrender.Drawn) ([]byte, error) {
	e.drawn = append(e.drawn, d)
	if e.fail != nil {
		return nil, e.fail
	}
	return picture(int(d.Width)/8, int(d.Height)/8, color.NRGBA{R: 250, G: 250, B: 250, A: 255}), nil
}

// desk is a VideoQueuer.
type desk struct {
	queued []VideoRequest
	owners []Owner
	calls  []string
	fail   error
}

func (d *desk) Check() error { return nil }
func (d *desk) Queue(_ context.Context, owner Owner, toolCallID string, request VideoRequest) (int64, error) {
	if d.fail != nil {
		return 0, d.fail
	}
	d.queued = append(d.queued, request)
	d.owners = append(d.owners, owner)
	d.calls = append(d.calls, toolCallID)
	return int64(500 + len(d.queued)), nil
}

// picture is a PNG of one colour.
func picture(width, height int, fill color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = fill.R, fill.G, fill.B, fill.A
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}

// runSpecialist starts the specialist's own agent on a task and returns the results
// of the named tool, in order.
func runSpecialist(t *testing.T, s Specialist, owner Owner, toolName string) []event.Event {
	t.Helper()
	delegate, err := s.Delegate(owner)
	if err != nil {
		t.Fatal(err)
	}
	var results []event.Event
	if _, err := delegate.Agent.Start(context.Background(), "t", []model.Message{model.User("task")}, func(ev event.Event) {
		if ev.Kind == event.ToolResult && ev.Tool == toolName {
			results = append(results, ev)
		}
	}); err != nil {
		t.Fatal(err)
	}
	return results
}

// ---- infographic ----

// posterFigure is a section's figure, as the model writes it.
const posterFigure = `{"type":"bar_chart","title":"Light used by each pigment","alt":"Chlorophyll a absorbs most of the light used.","spec":{"categories":["Chlorophyll a","Chlorophyll b","Carotenoids"],"series":[{"label":"Share","values":[60,25,15]}],"value_axis":{"label":"Share","unit":"%","min":0,"max":70}}}`

// testPoster is a poster about photosynthesis: numbers, three sections, a
// figure, a takeaway and its sources.
const testPoster = `{"subtitle":"How plants make sugar from light","key_numbers":[{"value":"2","label":"stages, light and dark"},{"value":"6 CO2","label":"fixed for each glucose"}],` +
	`"sections":[{"heading":"Light reactions","points":["In the thylakoids","Make ATP and NADPH"]},` +
	`{"heading":"Calvin cycle","points":["In the stroma","Fixes carbon dioxide"],"figure":` + posterFigure + `},` +
	`{"heading":"Why it matters","points":["Feeds almost every food chain"]}],` +
	`"takeaway":"Light reactions make the energy the Calvin cycle spends.","sources":"Campbell Biology, chapter 10."}`

func infographicArgs(t testing.TB, title, poster string) string {
	t.Helper()
	args, err := json.Marshal(map[string]any{"format": "infographic", "title": title, "poster": json.RawMessage(poster)})
	if err != nil {
		t.Fatal(err)
	}
	return string(args)
}

// questionAnswer is the page's answer to the question: a style and an
// orientation, each as its own answer.
func questionAnswer(style, orientation string) json.RawMessage {
	return json.RawMessage(`{"type":"respond","preset_id":"infographic.visual-style","preset_version":2,"tool_call_id":"d1/s1","answers":[` +
		`{"question_id":"visual-style","option_ids":["` + style + `"]},{"question_id":"orientation","option_ids":["` + orientation + `"]}]}`)
}

// main is a main agent that hands every task to the specialist, as the chat
// does.
func mainAgent(t *testing.T, s Specialist, owner Owner, script *modeltest.Script) *agent.Agent {
	t.Helper()
	delegate, err := s.Delegate(owner)
	if err != nil {
		t.Fatal(err)
	}
	built, err := agent.New(agent.Config{
		Model: script, System: "main", Delegates: []agent.Delegate{delegate},
		MaxSteps: 8, MaxParallelTools: 1, Store: s.Store,
	})
	if err != nil {
		t.Fatal(err)
	}
	return built
}

// An infographic through the whole stack the chat runs: the main agent
// hands the task to the specialist, whose save_artifact pauses the turn with
// the question; the person's answer resumes it; the poster is drawn in that
// style on that page and saved as a PNG with its SVG. One model script
// serves both agents, in the order they call it.
func TestAnInfographicAsksForAStyleThenIsDrawnAndSaved(t *testing.T) {
	l := &ledger{}
	script := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "d1", Name: DelegateName, Args: `{"task":"Make an infographic about photosynthesis from: ..."}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "i1", Name: InstructionsTool, Args: `{"artifact_type":"infographic"}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "s1", Name: SaveTool, Args: infographicArgs(t, "Photosynthesis", testPoster)}}},
		modeltest.Reply{Text: "I made the infographic Photosynthesis in the Scientific style."},
		modeltest.Reply{Text: "Your infographic is ready."},
	)
	s, local := specialist(t, script, l)
	owner := Owner{ThreadID: 3, UserID: "u-1"}
	chat := mainAgent(t, s, owner, script)

	var asked, saved, instructions event.Event
	sink := func(ev event.Event) {
		switch {
		case ev.Kind == event.ApprovalNeeded:
			asked = ev
		case ev.Kind == event.ToolResult && ev.Tool == SaveTool:
			saved = ev
		case ev.Kind == event.ToolResult && ev.Tool == InstructionsTool:
			instructions = ev
		}
	}
	result, err := chat.Start(context.Background(), "chat-3", []model.Message{model.User("an infographic please")}, sink)
	if err != nil || result.Outcome != agent.Paused {
		t.Fatalf("the first run: %+v, %v", result, err)
	}
	// The instructions are written from the renderer's limits and offer
	// only the figures drawn here.
	for _, want := range []string{"poster", "at most 20 words", "### bar_chart", "### flowchart"} {
		if !strings.Contains(instructions.Text, want) {
			t.Errorf("the instructions lack %q", want)
		}
	}
	if strings.Contains(strings.ToLower(instructions.Text), "mermaid") || strings.Contains(instructions.Text, "markdown is the plan") {
		t.Error("the instructions offer what is not drawn")
	}

	// The question reached the outermost run, addressed to the nested call,
	// in the shape the page's question card parses: a style with pictures,
	// then the orientation. Nothing was drawn.
	if asked.CallID != "d1/s1" || asked.Tool != SaveTool {
		t.Fatalf("asked by %q (%s)", asked.CallID, asked.Tool)
	}
	var payload struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
		Origin  struct {
			Kind          string `json:"kind"`
			PresetID      string `json:"preset_id"`
			PresetVersion int    `json:"preset_version"`
		} `json:"origin"`
		Questions []struct {
			ID                string `json:"id"`
			InputType         string `json:"input_type"`
			Presentation      string `json:"presentation"`
			Required          bool   `json:"required"`
			MinimumSelections int    `json:"minimum_selections"`
			MaximumSelections int    `json:"maximum_selections"`
			Options           []struct {
				ID           string `json:"id"`
				Label        string `json:"label"`
				Description  string `json:"description"`
				PreviewAsset string `json:"preview_asset"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(asked.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Type != "structured_question" || payload.Version != 1 || payload.Origin.Kind != "preset" ||
		payload.Origin.PresetID != "infographic.visual-style" || payload.Origin.PresetVersion != 2 || len(payload.Questions) != 2 {
		t.Fatalf("question = %s", asked.Payload)
	}
	style, shape := payload.Questions[0], payload.Questions[1]
	if style.ID != "visual-style" || style.Presentation != "visual_cards" || shape.ID != "orientation" || shape.Presentation != "list" {
		t.Fatalf("questions = %+v", payload.Questions)
	}
	for _, q := range payload.Questions {
		if q.InputType != "single_select" || !q.Required || q.MinimumSelections != 1 || q.MaximumSelections != 1 {
			t.Errorf("question %+v", q)
		}
	}
	// Auto and the six styles, each with its preview; the three pages.
	var ids []string
	for _, option := range style.Options {
		ids = append(ids, option.ID)
		if option.PreviewAsset != "infographic-style/"+option.ID || option.Label == "" || option.Description == "" || len(option.Description) > 80 {
			t.Errorf("option %+v", option)
		}
	}
	if strings.Join(ids, ",") != "auto,sketch-note,editorial,instructional,bento-grid,scientific,professional" || len(shape.Options) != 3 || shape.Options[1].ID != "portrait" {
		t.Fatalf("options = %v and %+v", ids, shape.Options)
	}
	if len(l.easel.drawn) != 0 || len(l.created) != 0 {
		t.Fatal("something was drawn or saved before the person chose")
	}
	// The frame the page is sent is the question itself with the call's id
	// beside it: what its question card looks for, and the id it answers to.
	frame, err := uistream.InterruptData(asked, func(event.Event) uistream.ApprovalCard {
		t.Error("a question was presented as an approval card")
		return uistream.ApprovalCard{}
	})
	var sentToPage map[string]any
	if err != nil || json.Unmarshal(frame, &sentToPage) != nil || sentToPage["type"] != "structured_question" || sentToPage["tool_call_id"] != "d1/s1" || sentToPage["action_requests"] != nil {
		t.Fatalf("the interrupt frame: %s, %v", frame, err)
	}

	// The answer, as the page sends it.
	result, err = chat.Resume(context.Background(), "chat-3", []agent.Decision{{CallID: "d1/s1", Verdict: agent.Answer, Answer: questionAnswer("scientific", "portrait")}}, sink)
	if err != nil || result.Outcome != agent.Finished || result.Answer != "Your infographic is ready." {
		t.Fatalf("the resumed run: %+v, %v", result, err)
	}
	if len(l.easel.drawn) != 1 || l.easel.drawn[0].Style != "scientific" || l.easel.drawn[0].Orientation != "portrait" || l.easel.drawn[0].Height != 1536 {
		t.Fatalf("drawn = %+v", l.easel.drawn)
	}
	made := l.created[0]
	want := `{"status":"saved","artifact_id":41,"generation":1,"title":"Photosynthesis","format":"infographic","files":[` +
		`{"file_id":241,"role":"primary","filename":"photosynthesis.png","mime_type":"image/png","size_bytes":` + decimal(len(made.Data)) + `},` +
		`{"file_id":341,"role":"preview","filename":"photosynthesis.svg","mime_type":"image/svg+xml","size_bytes":` + decimal(len(made.Preview)) + `}],"style":"Scientific"}`
	if saved.Text != want {
		t.Fatalf("result\n got %s\nwant %s", saved.Text, want)
	}
	if made.Owner != owner || made.Format != "infographic" || made.ToolCallID != "s1" || made.MimeType != "image/png" ||
		!strings.HasPrefix(made.Markdown, "# Photosynthesis\n\nHow plants make sugar from light\n") || !strings.Contains(made.Markdown, "Figure: Light used by each pigment.") {
		t.Fatalf("artifact = %+v", made)
	}
	// The SVG kept is the very drawing, carrying its faces.
	if string(made.Preview) != l.easel.drawn[0].Kept || !bytes.Contains(made.Preview, []byte("@font-face")) || !bytes.Contains(made.Preview, []byte("Calvin cycle")) {
		t.Errorf("the preview is not the kept SVG (%d bytes)", len(made.Preview))
	}
	var metadata struct {
		Verification struct {
			Verified bool `json:"verified"`
		} `json:"verification"`
		Generation map[string]any `json:"generation"`
		Poster     struct {
			Title    string `json:"title"`
			Sections []struct {
				Figure json.RawMessage `json:"figure"`
			} `json:"sections"`
		} `json:"poster"`
	}
	if err := json.Unmarshal(made.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	g := metadata.Generation
	if !metadata.Verification.Verified || g["renderer"] != "poster" || g["requested_style_id"] != "scientific" || g["resolved_style_id"] != "scientific" ||
		g["orientation"] != "portrait" || g["question_preset_version"] != 2.0 || g["provider_model"] != nil || metadata.Poster.Title != "Photosynthesis" ||
		!bytes.Contains(metadata.Poster.Sections[1].Figure, []byte(`"type":"bar_chart"`)) {
		t.Errorf("metadata = %s", made.Metadata)
	}
	stored := storedFile(t, local, l.keys[0])
	if config, err := png.DecodeConfig(bytes.NewReader(stored)); err != nil || config.Width != 128 || config.Height != 192 {
		t.Errorf("the stored file: %+v, %v", config, err)
	}
	// The card is the save_artifact card: the saved fields, nothing else.
	card, mine := ToolCard(saved)
	out, _ := json.Marshal(card)
	if !mine || string(out) != strings.Replace(want, `,"style":"Scientific"`, "", 1) {
		t.Errorf("card = %s", out)
	}
}

// storedFile is a stored file's bytes.
func storedFile(t *testing.T, local *filestore.Local, key string) []byte {
	t.Helper()
	body, err := local.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("%s is not in the store: %v", key, err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decimal(n int) string {
	out, _ := json.Marshal(n)
	return string(out)
}

// drawWith runs the infographic's second run (the one with an answer)
// directly.
func drawWith(t *testing.T, l *ledger, poster string, answer json.RawMessage) (string, error) {
	t.Helper()
	s, _ := specialist(t, modeltest.NewScript(), l)
	call := toolCall(infographicArgs(t, "Photosynthesis", poster))
	call.Answer = answer
	return s.saveTool(Owner{ThreadID: 3, UserID: "u-1"}).Run(context.Background(), call)
}

// A poster the layout cannot fit is refused naming what to shorten; the
// person's choice stands for the next call of the same turn, which draws
// without asking again.
func TestAPosterThatDoesNotFitIsCorrectedWithoutAskingAgain(t *testing.T) {
	l := &ledger{}
	s, _ := specialist(t, modeltest.NewScript(), l)
	save := s.saveTool(Owner{ThreadID: 3, UserID: "u-1"})
	point := `"Salbutamol by spacer, two to ten puffs, repeated every twenty to thirty minutes as needed"`
	points := "[" + strings.Repeat(point+",", 4) + point + "]"
	var sections []string
	for i := 0; i < 6; i++ {
		sections = append(sections, `{"heading":"Section","points":`+points+`}`)
	}
	full := `{"key_numbers":[{"value":"1 in 11","label":"children"}],"sections":[` + strings.Join(sections, ",") + `],"takeaway":"Review the preventer."}`

	first := toolCall(infographicArgs(t, "Asthma", full))
	first.Answer = questionAnswer("editorial", "square")
	_, err := save.Run(context.Background(), first)
	if err == nil || !strings.Contains(err.Error(), "more height than the square poster has") || !strings.Contains(err.Error(), "will not be asked again") {
		t.Fatalf("a poster too full for its page: %v", err)
	}
	if len(l.easel.drawn) != 0 || len(l.created) != 0 {
		t.Fatal("a refused poster was drawn or saved")
	}
	// The corrected call has no answer of its own: the turn's stands.
	out, err := save.Run(context.Background(), toolCall(infographicArgs(t, "Asthma", testPoster)))
	if err != nil || !strings.Contains(out, `"style":"Editorial"`) || len(l.easel.drawn) != 1 || l.easel.drawn[0].Orientation != "square" {
		t.Fatalf("the corrected poster: %q, %v, drawn %+v", out, err, l.easel.drawn)
	}
	// A new turn asks again.
	_, err = s.saveTool(Owner{ThreadID: 3, UserID: "u-1"}).Run(context.Background(), toolCall(infographicArgs(t, "Asthma", testPoster)))
	if !isAsk(err) {
		t.Fatalf("a new turn did not ask: %v", err)
	}
}

// isAsk reports whether err is the pause that asks the person.
func isAsk(err error) bool {
	_, asked := tool.AsQuestion(err)
	return asked
}

// What is not an answer to the question draws nothing, and content that
// would be refused is refused before the person is asked.
func TestAnInfographicIsNotDrawnWithoutAnAnswer(t *testing.T) {
	for name, c := range map[string]struct {
		answer string
		says   string
	}{
		"closed without choosing":  {`{"type":"cancel","preset_id":"infographic.visual-style","preset_version":2,"tool_call_id":"d1/s1"}`, "closed the style question without choosing"},
		"another question":         {`{"type":"respond","preset_id":"something.else","preset_version":2,"answers":[{"question_id":"visual-style","option_ids":["editorial"]}]}`, "not an answer to the infographic style question"},
		"the image model's styles": {`{"type":"respond","preset_id":"infographic.visual-style","preset_version":1,"answers":[{"question_id":"visual-style","option_ids":["clay"]}]}`, "not an answer to the infographic style question"},
		"two styles":               {`{"type":"respond","preset_id":"infographic.visual-style","preset_version":2,"answers":[{"question_id":"visual-style","option_ids":["editorial","scientific"]},{"question_id":"orientation","option_ids":["square"]}]}`, "exactly one choice"},
		"no style":                 {`{"type":"respond","preset_id":"infographic.visual-style","preset_version":2,"answers":[{"question_id":"orientation","option_ids":["square"]}]}`, "does not choose a style"},
		"no orientation":           {`{"type":"respond","preset_id":"infographic.visual-style","preset_version":2,"answers":[{"question_id":"visual-style","option_ids":["editorial"]}]}`, "does not choose an orientation"},
		"a style there is not":     {string(questionAnswer("kawaii", "square")), `"kawaii" is not one of the infographic styles`},
		"a page there is not":      {string(questionAnswer("auto", "panorama")), `"panorama" is not an infographic orientation`},
		"not JSON at all":          {`"clay"`, "not readable"},
	} {
		l := &ledger{}
		_, err := drawWith(t, l, testPoster, json.RawMessage(c.answer))
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", name, err)
		}
		if len(l.easel.drawn) != 0 || len(l.created) != 0 {
			t.Errorf("%s: something was drawn or saved", name)
		}
	}

	l := &ledger{}
	s, _ := specialist(t, modeltest.NewScript(), l)
	for name, c := range map[string]struct{ title, poster, says string }{
		"no title":         {" ", testPoster, "the title is empty"},
		"not a poster":     {"T", `"a plan in Markdown"`, "the poster is not in its shape"},
		"a field it lacks": {"T", `{"sections":[],"layout":"two columns"}`, `unknown field "layout"`},
		"one section":      {"T", `{"sections":[{"heading":"Only","points":["One"]}]}`, "sections: has 1"},
		"a long point":     {"T", `{"sections":[{"heading":"A","points":["` + strings.Repeat("word ", 30) + `"]},{"heading":"B","points":["x"]}]}`, "sections[0].points[0]: is 30 words; keep it to 20"},
		"a mermaid figure": {"T", `{"sections":[{"heading":"A","figure":{"type":"mermaid","title":"x","alt":"y","spec":{"mermaid":"graph TD\nA-->B"}}},{"heading":"B","points":["x"]}]}`, `type "mermaid" is not drawn here`},
	} {
		_, err := s.saveTool(Owner{UserID: "u-1"}).Run(context.Background(), toolCall(infographicArgs(t, c.title, c.poster)))
		if err == nil || !strings.Contains(err.Error(), c.says) || isAsk(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(l.easel.drawn) != 0 || len(l.created) != 0 {
		t.Error("refused content was drawn or saved")
	}
}

// A fault in drawing is a plain sentence to the model, never resvg's own
// words, and nothing is saved.
func TestAPosterThatCannotBeRasterised(t *testing.T) {
	l := &ledger{}
	l.easel.fail = errors.New("resvg: exit status 1: /usr/local/share/figures/fonts unreadable")
	_, err := drawWith(t, l, testPoster, questionAnswer("auto", "landscape"))
	if err == nil || !strings.Contains(err.Error(), "fault in the application") || strings.Contains(err.Error(), "resvg") || len(l.created) != 0 {
		t.Fatalf("a fault: %v", err)
	}
}

// "auto" is settled from the poster's own words, the same way every time.
func TestAutoPicksAStyleFromTheContent(t *testing.T) {
	for words, want := range map[string]string{
		"Quarterly revenue by market":       "professional",
		"A clinical trial of a new drug":    "scientific",
		"Steps to bake bread":               "instructional",
		"Study notes on a clinical trial":   "sketch-note", // a study aid first
		"Nothing here says anything":        "sketch-note",
		"Restudying is not the word study":  "sketch-note",
		"The history of the printing press": "editorial",
		"Key metrics":                       "bento-grid",
	} {
		style, err := resolveStyle("auto", words)
		if err != nil || style != want {
			t.Errorf("%q: %s, %v; want %s", words, style, err, want)
		}
	}
	if style, err := resolveStyle("editorial", "Quarterly revenue"); err != nil || style != "editorial" {
		t.Errorf("an explicit choice: %s, %v", style, err)
	}
	if _, err := resolveStyle("anime", "x"); err == nil {
		t.Error("a retired style was drawn")
	}
	// Every style auto can reach exists, and every style can be reached.
	if strings.Join(autoOrder, ",") == "" || len(autoOrder) != len(posterrender.Styles()) {
		t.Fatalf("auto tries %v of %v", autoOrder, posterrender.Styles())
	}
	for _, id := range append([]string{defaultAutoStyle}, autoOrder...) {
		if posterrender.About(id) == "" || styleLabels[id] == "" || len(styleSignals[id]) == 0 {
			t.Errorf("auto names a style %q that is not complete", id)
		}
	}
}

// The answer a headless run gives must be one chosenStyle reads back as the
// same style and page: the two are written apart and would otherwise drift.
func TestAStyleAnswerIsReadBackAsItsStyle(t *testing.T) {
	for _, style := range InfographicStyles() {
		for _, orientation := range InfographicOrientations() {
			answer, err := StyleAnswer(style, orientation)
			if err != nil {
				t.Fatalf("%s %s: %v", style, orientation, err)
			}
			chosen, err := chosenStyle(answer)
			if err != nil || chosen.style != style || chosen.orientation != orientation {
				t.Errorf("%s %s was read back as %+v, %v", style, orientation, chosen, err)
			}
		}
	}
	for _, bad := range [][2]string{{"oil-painting", "square"}, {"auto", "panorama"}, {"auto", ""}} {
		if _, err := StyleAnswer(bad[0], bad[1]); err == nil {
			t.Errorf("%v was answered", bad)
		}
	}
	if InfographicStyles()[0] != "auto" || strings.Join(InfographicOrientations(), " ") != "landscape portrait square" {
		t.Errorf("styles %v, orientations %v", InfographicStyles(), InfographicOrientations())
	}
}

// ---- video ----

// The specialist handed a video: it loads the instructions and queues a job
// for the turn's owner, with the material. Nothing is saved as an artifact.
func TestTheSpecialistQueuesAVideoEndToEnd(t *testing.T) {
	l := &ledger{}
	script := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "i1", Name: InstructionsTool, Args: `{"artifact_type":"video"}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "v0", Name: VideoTool, Args: `{"title":" ","brief":""}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "v1", Name: VideoTool, Args: `{"title":" Mitosis\n explained ","brief":"A five minute  explainer\nfor first-year students.","source_content":"Mitosis has four phases.\n\nProphase comes first.\n"}`}}},
		modeltest.Reply{Text: "The video Mitosis explained is being generated; you can keep chatting."},
	)
	s, _ := specialist(t, script, l)
	owner := Owner{ThreadID: 3, UserID: "u-1"}
	results := runSpecialist(t, s, owner, VideoTool)

	requests := script.Requests()
	if told := requests[1].Messages[len(requests[1].Messages)-1].Content; !strings.Contains(told, "Call enqueue_deliverable_job once") || !strings.Contains(told, "Do not call save_artifact") {
		t.Errorf("the video instructions were %q", told)
	}
	if len(results) != 2 || !results[0].IsError || !strings.Contains(results[0].Text, "the video was not queued: title is empty; brief is empty") {
		t.Fatalf("results = %+v", results)
	}
	want := `{"status":"pending","job_id":501,"title":"Mitosis explained","message":"` + videoQueuedMessage + `"}`
	if results[1].Text != want {
		t.Fatalf("result\n got %s\nwant %s", results[1].Text, want)
	}
	// The title and the brief on one line, as the worker stores them; the
	// material with its paragraphs, which the worker cuts a long source by.
	if len(l.desk.queued) != 1 || l.desk.owners[0] != owner || l.desk.calls[0] != "v1" ||
		l.desk.queued[0] != (VideoRequest{Title: "Mitosis explained", Brief: "A five minute explainer for first-year students.", Material: "Mitosis has four phases.\n\nProphase comes first."}) {
		t.Fatalf("queued %+v for %+v by %q", l.desk.queued, l.desk.owners, l.desk.calls)
	}
	if len(l.created) != 0 {
		t.Fatal("a video was saved as an artifact")
	}
	card, mine := ToolCard(results[1])
	if out, _ := json.Marshal(card); !mine || string(out) != `{"status":"pending","job_id":501,"title":"Mitosis explained"}` {
		t.Errorf("card = %s", out)
	}
}

// videoRows is VideoRows in memory.
type videoRows struct {
	titles   []string
	requests []string
	answer   QueuedVideo
	fail     error
}

func (r *videoRows) CreateVideo(_ context.Context, _ Owner, _, title string, request []byte) (QueuedVideo, error) {
	if r.fail != nil {
		return QueuedVideo{}, r.fail
	}
	r.titles = append(r.titles, title)
	r.requests = append(r.requests, string(request))
	return r.answer, nil
}

// The job's request is the worker's shape exactly, the message goes out
// after the row under the row's task id, and a message that cannot be
// published does not undo a job that is queued.
func TestAVideoIsWrittenThenPublished(t *testing.T) {
	type published struct {
		jobID  int64
		taskID string
	}
	var sent []published
	var publishErr error
	table := &videoRows{answer: QueuedVideo{JobID: 42, TaskID: videoTaskID(42), Created: true}}
	videos := Videos{
		Rows: table, Logger: discardLogger(),
		Publish: func(_ context.Context, jobID int64, taskID string) error {
			// The row exists by the time the message goes out.
			if len(table.requests) != len(sent)+1 {
				t.Errorf("published job %d before its row was written", jobID)
			}
			sent = append(sent, published{jobID, taskID})
			return publishErr
		},
	}
	if err := videos.Check(); err != nil {
		t.Fatal(err)
	}
	owner := Owner{ThreadID: 3, UserID: "u-1"}
	id, err := videos.Queue(context.Background(), owner, "call-1", VideoRequest{Title: "T", Brief: "A brief.", Material: "Line one.\nLine <two>."})
	if err != nil || id != 42 {
		t.Fatalf("queued %d, %v", id, err)
	}
	// VideoJobRequestV1 forbids unknown fields and a null where it wants
	// text, so the column is held to the byte.
	if table.requests[0] != `{"version":1,"brief":"A brief.","source_references":[],"root_thread_id":3,"source_material":"Line one.\nLine \u003ctwo\u003e."}` {
		t.Errorf("request = %s", table.requests[0])
	}
	if len(sent) != 1 || sent[0] != (published{42, "deliverable-job:42:attempt:1"}) {
		t.Fatalf("published %+v", sent)
	}

	// No material: the field is left out, never written as "" or null.
	if _, err := videos.Queue(context.Background(), owner, "call-2", VideoRequest{Title: "T", Brief: "A brief."}); err != nil || table.requests[1] != `{"version":1,"brief":"A brief.","source_references":[],"root_thread_id":3}` {
		t.Fatalf("request = %s, %v", table.requests[1], err)
	}

	// The broker is down: the job is queued all the same, because the row
	// is, and the reconciler publishes it.
	publishErr = errors.New("valkey: connection refused")
	if id, err := videos.Queue(context.Background(), owner, "call-3", VideoRequest{Title: "T", Brief: "B"}); err != nil || id != 42 {
		t.Fatalf("with the broker down: %d, %v", id, err)
	}

	// A second run of a call whose job exists publishes nothing more.
	before := len(sent)
	table.answer.Created = false
	if id, err := videos.Queue(context.Background(), owner, "call-3", VideoRequest{Title: "T", Brief: "B"}); err != nil || id != 42 || len(sent) != before {
		t.Fatalf("a second run: %d, %v, published %d more", id, err, len(sent)-before)
	}

	// No call id, no job: it is the idempotency key.
	if _, err := videos.Queue(context.Background(), owner, "", VideoRequest{Title: "T", Brief: "B"}); err == nil {
		t.Fatal("a video was queued without a call id")
	}
}

// A video that cannot be queued says so on its card, in a sentence that
// names no table.
func TestAVideoThatCannotBeQueued(t *testing.T) {
	for name, c := range map[string]struct {
		fail error
		card string
	}{
		"a storage failure": {errors.New(`relation "deliverable_jobs" does not exist`), `{"status":"failed","error":"the video could not be queued because of a storage error."}`},
	} {
		l := &ledger{}
		l.desk.fail = c.fail
		s, _ := specialist(t, modeltest.NewScript(), l)
		_, err := s.videoTool(Owner{UserID: "u-1"}).Run(context.Background(), toolCall(`{"title":"T","brief":"B"}`))
		if err == nil || strings.Contains(err.Error(), "relation") || !strings.HasSuffix(err.Error(), refusedVideoNote) {
			t.Errorf("%s: the model was told %v", name, err)
			continue
		}
		card, mine := ToolCard(event.Event{Tool: VideoTool, IsError: true, Text: "error: " + err.Error()})
		if out, _ := json.Marshal(card); !mine || string(out) != c.card {
			t.Errorf("%s: card = %s", name, out)
		}
	}
	for args, says := range map[string]string{
		`{"title":"` + strings.Repeat("t", 501) + `","brief":"B"}`:                         "title is longer than 500",
		`{"title":"T","brief":"` + strings.Repeat("b", 16001) + `"}`:                       "brief is longer than 16000",
		`{"title":"T","brief":"B","source_content":"` + strings.Repeat("s", 180001) + `"}`: "source_content is longer than 180000",
		`{"title":"T","brief":"B","source_content":"a\u0000b"}`:                            "source_content contains a NUL",
		`{"title":"T","brief":"B","revision_artifact_id":4}`:                               "unknown field",
	} {
		l := &ledger{}
		s, _ := specialist(t, modeltest.NewScript(), l)
		if _, err := s.videoTool(Owner{UserID: "u-1"}).Run(context.Background(), toolCall(args)); err == nil || !strings.Contains(err.Error(), says) || len(l.desk.queued) != 0 {
			t.Errorf("%.30s: %v", args, err)
		}
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
