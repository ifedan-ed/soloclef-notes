package deliverables

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/uistream"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// The stand-ins the specialist's fixture is built with (deliverables_test.go,
// ledger): an image model, a model that reads an image back, and a video
// queue. Each remembers what it was asked and answers what a test set.

// painter is an ImageModel. It answers its images in order, then picture()
// for ever.
type painter struct {
	prompts []string
	sizes   []string
	images  []GeneratedImage
	// fail is answered instead of an image; failAt, when set, only on that
	// call (counted from 1).
	fail   error
	failAt int
}

func (p *painter) Check() error { return nil }
func (p *painter) Name() string { return "test-image-model" }
func (p *painter) Generate(_ context.Context, prompt, size string) (GeneratedImage, error) {
	p.prompts = append(p.prompts, prompt)
	p.sizes = append(p.sizes, size)
	if p.fail != nil && (p.failAt == 0 || p.failAt == len(p.prompts)) {
		return GeneratedImage{}, p.fail
	}
	if n := len(p.prompts); n <= len(p.images) {
		return p.images[n-1], nil
	}
	return GeneratedImage{Data: picture(320, 256, color.NRGBA{R: 200, G: 30, B: 30, A: 255}), MimeType: "image/png", Extension: "png"}, nil
}

// proofreader is an ImageReader. It answers its readings in order, then
// "everything is present" for ever.
type proofreader struct {
	users    []string
	prompts  []string
	pictures [][]byte
	readings []string
	fail     error
}

func (p *proofreader) Check() error { return nil }
func (p *proofreader) Read(_ context.Context, userID, prompt string, picture []byte, mimeType string) (string, error) {
	p.users, p.prompts = append(p.users, userID), append(p.prompts, prompt)
	p.pictures = append(p.pictures, picture)
	if p.fail != nil {
		return "", p.fail
	}
	if mimeType != "image/jpeg" {
		return "", errors.New("the reader was sent " + mimeType)
	}
	if n := len(p.prompts); n <= len(p.readings) {
		return p.readings[n-1], nil
	}
	// Everything present: one entry for each numbered line of the prompt.
	return present(strings.Count(prompt, ". ")), nil
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

// present is a reading that finds every one of n items.
func present(n int) string {
	var items []map[string]any
	for i := 1; i <= n; i++ {
		items = append(items, map[string]any{"id": i, "status": "present", "seen": nil})
	}
	out, _ := json.Marshal(map[string]any{"items": items})
	return string(out)
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

// ---- image ----

// The specialist handed an image: it loads the instructions, the image
// model draws, the file is saved as an "image" artifact, and the result is
// the page's image card.
func TestTheSpecialistMakesAnImageEndToEnd(t *testing.T) {
	l := &ledger{}
	jpegBytes := append([]byte("\xff\xd8\xff\xe0"), []byte("not really a jpeg, and it does not need to be")...)
	l.painter.images = []GeneratedImage{{Data: jpegBytes, MimeType: "image/jpeg", Extension: "jpg", RevisedPrompt: "A red fox, watercolour, soft morning light."}}
	script := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "i1", Name: InstructionsTool, Args: `{"artifact_type":"image"}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "g0", Name: ImageTool, Args: `{"prompt":"a fox","n":4}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "g1", Name: ImageTool, Args: `{"prompt":"  A red fox in  watercolour "}`}}},
		modeltest.Reply{Text: "I made an image of a red fox in watercolour."},
	)
	s, local := specialist(t, script, l)
	owner := Owner{ThreadID: 3, UserID: "u-1"}
	results := runSpecialist(t, s, owner, ImageTool)

	requests := script.Requests()
	if told := requests[1].Messages[len(requests[1].Messages)-1].Content; !strings.Contains(told, "Call generate_image once") {
		t.Errorf("the image instructions were %q", told)
	}
	// "n" is not an argument: the model is told, and nothing is drawn for
	// that call.
	if len(results) != 2 || !results[0].IsError || !strings.Contains(results[0].Text, `unknown field "n"`) {
		t.Fatalf("results = %+v", results)
	}
	if len(l.painter.prompts) != 1 || l.painter.prompts[0] != "A red fox in  watercolour" || l.painter.sizes[0] != "" {
		t.Fatalf("the image model was asked %q at sizes %q", l.painter.prompts, l.painter.sizes)
	}
	want := `{"id":"image-artifact-41","artifact_id":41,"alt":"A red fox, watercolour, soft morning light.","title":"A red fox in watercolour","description":"A red fox, watercolour, soft morning light.","domain":"ai-generated","ratio":"auto","generated":true,"prompt":"A red fox in  watercolour","message":"` + imageMadeMessage + `"}`
	if results[1].Text != want {
		t.Fatalf("result\n got %s\nwant %s", results[1].Text, want)
	}

	// The artifact: the file as the model returned it, named and typed from
	// its bytes; the prompt as its document; what drew it in its metadata.
	if len(l.created) != 1 {
		t.Fatalf("created %d artifacts", len(l.created))
	}
	made := l.created[0]
	if made.Owner != owner || made.Format != "image" || made.ToolCallID != "g1" || made.Title != "A red fox in watercolour" ||
		made.Filename != "a-red-fox-in-watercolour.jpg" || made.MimeType != "image/jpeg" || !bytes.Equal(made.Data, jpegBytes) {
		t.Fatalf("artifact = %+v", made)
	}
	if made.Markdown != "# A red fox in watercolour\n\nPrompt: A red fox in  watercolour\n" {
		t.Errorf("document = %q", made.Markdown)
	}
	if string(made.Metadata) != `{"model":"test-image-model","prompt":"A red fox in  watercolour","revised_prompt":"A red fox, watercolour, soft morning light."}` {
		t.Errorf("metadata = %s", made.Metadata)
	}
	if !strings.HasSuffix(l.keys[0], ".jpg") {
		t.Errorf("stored at %q", l.keys[0])
	}
	if stored := storedFile(t, local, l.keys[0]); !bytes.Equal(stored, jpegBytes) {
		t.Errorf("the stored file is %d bytes, the image %d", len(stored), len(jpegBytes))
	}

	// The card is the result less the model's message.
	card, mine := ToolCard(results[1])
	out, _ := json.Marshal(card)
	if !mine || string(out) != strings.Replace(want, `,"message":"`+imageMadeMessage+`"`, "", 1) {
		t.Errorf("card = %s", out)
	}
}

// Each way the image model can fail is a sentence for the person, on the
// card, and nothing is saved.
func TestAnImageThatCannotBeMade(t *testing.T) {
	for name, c := range map[string]struct {
		fail  error
		model string
		card  string
	}{
		"no image model": {
			NoImageModel{}.mustFail(), noImageModel + refusedImageNote,
			`{"error":"This server has no image model set up, so images cannot be made here.","prompt":"a fox"}`,
		},
		"a refused prompt": {
			imageFailure(context.Background(), &proxyStatus{status: 400, body: "content policy"}), imageRequestRefused + refusedImageNote,
			`{"error":"The image model would not draw this request. Rewording it may help.","prompt":"a fox"}`,
		},
		"a failure with no sentence of its own": {
			errors.New("dial tcp 10.0.0.9:4000: connection refused"), "the image could not be made because the image model failed." + refusedImageNote,
			`{"error":"the image could not be made because the image model failed.","prompt":"a fox"}`,
		},
	} {
		l := &ledger{}
		l.painter.fail = c.fail
		s, _ := specialist(t, modeltest.NewScript(), l)
		_, err := s.imageTool(Owner{UserID: "u-1"}).Run(context.Background(), toolCall(`{"prompt":"a fox"}`))
		if err == nil || err.Error() != c.model {
			t.Errorf("%s: the model was told %v", name, err)
			continue
		}
		// An address or a provider's words never reach the model or the card.
		card, mine := ToolCard(event.Event{Tool: ImageTool, IsError: true, Text: "error: " + err.Error(), Args: `{"prompt":"a fox"}`})
		out, _ := json.Marshal(card)
		if !mine || string(out) != c.card {
			t.Errorf("%s: card = %s", name, out)
		}
		if len(l.created) != 0 {
			t.Errorf("%s: something was saved", name)
		}
	}

	// A prompt is checked before anything is spent.
	l := &ledger{}
	s, _ := specialist(t, modeltest.NewScript(), l)
	for args, says := range map[string]string{
		`{"prompt":"  "}`: "prompt is empty",
		`{"prompt":"` + strings.Repeat("p", maxImagePromptChars+1) + `"}`: "longer than 4000",
		`{"prompt":"a\u0007b"}`: "control character",
	} {
		if _, err := s.imageTool(Owner{UserID: "u-1"}).Run(context.Background(), toolCall(args)); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%.40s: %v", args, err)
		}
	}
	if len(l.painter.prompts) != 0 {
		t.Fatal("the image model was asked for a prompt that was refused")
	}

	// A drawn image that cannot be stored is said to be not saved.
	l = &ledger{failRows: errors.New("relation artifacts does not exist")}
	s, _ = specialist(t, modeltest.NewScript(), l)
	if _, err := s.imageTool(Owner{UserID: "u-1"}).Run(context.Background(), toolCall(`{"prompt":"a fox"}`)); err == nil || strings.Contains(err.Error(), "relation") || !strings.Contains(err.Error(), "could not be saved") {
		t.Fatalf("a storage failure: %v", err)
	}
}

// mustFail is the refusal a deployment with no image model answers.
func (m NoImageModel) mustFail() error {
	_, err := m.Generate(context.Background(), "", "")
	return err
}

// ---- infographic ----

const infographicPlan = "# Photosynthesis\n\n## Light reactions\n- Thylakoid: makes ATP\n- 2 photosystems\n\n## Calvin cycle\n- Fixes CO2\n- 3 phases, 95% efficient\n"

func infographicArgs(t testing.TB, title, markdown string) string {
	t.Helper()
	args, err := json.Marshal(map[string]any{"format": "infographic", "title": title, "markdown": markdown})
	if err != nil {
		t.Fatal(err)
	}
	return string(args)
}

func styleAnswer(option string) json.RawMessage {
	return json.RawMessage(`{"type":"respond","preset_id":"infographic.visual-style","preset_version":1,"tool_call_id":"d1/s1","answers":[{"question_id":"visual-style","option_ids":["` + option + `"]}]}`)
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
// the style question; the person's answer resumes it; the image is drawn in
// that style, read back, and saved. One model script serves both agents, in
// the order they call it.
func TestAnInfographicAsksForAStyleThenIsDrawnCheckedAndSaved(t *testing.T) {
	l := &ledger{}
	l.painter.images = []GeneratedImage{{Data: picture(640, 400, color.NRGBA{R: 10, G: 120, B: 200, A: 255}), MimeType: "image/png", Extension: "png"}}
	items := plannedItems(infographicPlan, maxCheckedItems)
	l.proofreader.readings = []string{"```json\n" + present(len(items)) + "\n```"}
	script := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "d1", Name: DelegateName, Args: `{"task":"Make an infographic about photosynthesis from: ..."}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "i1", Name: InstructionsTool, Args: `{"artifact_type":"infographic"}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "s1", Name: SaveTool, Args: infographicArgs(t, "Photosynthesis", infographicPlan)}}},
		modeltest.Reply{Text: "I made the infographic Photosynthesis in the Scientific style."},
		modeltest.Reply{Text: "Your infographic is ready."},
	)
	s, local := specialist(t, script, l)
	owner := Owner{ThreadID: 3, UserID: "u-1"}
	chat := mainAgent(t, s, owner, script)

	var asked, saved event.Event
	sink := func(ev event.Event) {
		switch {
		case ev.Kind == event.ApprovalNeeded:
			asked = ev
		case ev.Kind == event.ToolResult && ev.Tool == SaveTool:
			saved = ev
		}
	}
	result, err := chat.Start(context.Background(), "chat-3", []model.Message{model.User("an infographic please")}, sink)
	if err != nil || result.Outcome != agent.Paused {
		t.Fatalf("the first run: %+v, %v", result, err)
	}

	// The question reached the outermost run, addressed to the nested call,
	// in the shape the page's question card parses. Nothing was drawn.
	if asked.CallID != "d1/s1" || asked.Tool != SaveTool {
		t.Fatalf("asked by %q (%s)", asked.CallID, asked.Tool)
	}
	var payload struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
		Title   string `json:"title"`
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
	if payload.Type != "structured_question" || payload.Version != 1 || payload.Title != "Choose an infographic style" ||
		payload.Origin.Kind != "preset" || payload.Origin.PresetID != "infographic.visual-style" || payload.Origin.PresetVersion != 1 || len(payload.Questions) != 1 {
		t.Fatalf("question = %s", asked.Payload)
	}
	q := payload.Questions[0]
	if q.ID != "visual-style" || q.InputType != "single_select" || q.Presentation != "visual_cards" || !q.Required || q.MinimumSelections != 1 || q.MaximumSelections != 1 {
		t.Fatalf("question = %+v", q)
	}
	// Auto and the ten styles: within the page's limit of twelve options,
	// each within its limits for an id, a label and a description, and each
	// with the preview picture the page has a file for.
	if len(q.Options) != 11 || q.Options[0].ID != "auto" || q.Options[3].ID != "sketch-note" || q.Options[10].ID != "professional" {
		t.Fatalf("options = %+v", q.Options)
	}
	for _, option := range q.Options {
		if option.PreviewAsset != "infographic-style/"+option.ID || option.Label == "" || len(option.Label) > 80 || option.Description == "" || len(option.Description) > 600 {
			t.Errorf("option %+v", option)
		}
	}
	if len(l.painter.prompts) != 0 || len(l.created) != 0 {
		t.Fatal("something was drawn or saved before the person chose a style")
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
	waiting, err := chat.Pending(context.Background(), "chat-3")
	if err != nil || len(waiting) != 1 || waiting[0].CallID != "d1/s1" || !bytes.Equal(waiting[0].Question, asked.Payload) {
		t.Fatalf("pending = %+v, %v", waiting, err)
	}

	// The answer, as the page sends it.
	result, err = chat.Resume(context.Background(), "chat-3", []agent.Decision{{CallID: "d1/s1", Verdict: agent.Answer, Answer: styleAnswer("scientific")}}, sink)
	if err != nil || result.Outcome != agent.Finished || result.Answer != "Your infographic is ready." {
		t.Fatalf("the resumed run: %+v, %v", result, err)
	}
	if len(l.painter.prompts) != 1 {
		t.Fatalf("drawn %d times", len(l.painter.prompts))
	}
	prompt := l.painter.prompts[0]
	scientific, _ := styleNamed("scientific")
	if !strings.HasPrefix(prompt, infographicTask+"\n\nCONTENT\n# Photosynthesis") || !strings.HasSuffix(prompt, "\n\nVISUAL STYLE\n"+scientific.description) || strings.Contains(prompt, "REPAIR") {
		t.Errorf("the image prompt was %q", prompt)
	}
	// The reader was sent a JPEG copy and asked about each planned item.
	if len(l.proofreader.prompts) != 1 || !strings.Contains(l.proofreader.prompts[0], "1. Photosynthesis\n2. Light reactions\n3. Thylakoid\n4. 2 photosystems\n5. 2\n") {
		t.Fatalf("the reading prompt was %q", l.proofreader.prompts)
	}
	if _, err := jpeg.Decode(bytes.NewReader(l.proofreader.pictures[0])); err != nil {
		t.Errorf("the reader's copy is not a JPEG: %v", err)
	}

	want := `{"status":"saved","artifact_id":41,"generation":1,"title":"Photosynthesis","format":"infographic","files":[{"file_id":241,"role":"primary","filename":"photosynthesis.png","mime_type":"image/png","size_bytes":` + decimal(len(l.created[0].Data)) + `}],"style":"Scientific"}`
	if saved.Text != want {
		t.Fatalf("result\n got %s\nwant %s", saved.Text, want)
	}
	made := l.created[0]
	if made.Owner != owner || made.Format != "infographic" || made.ToolCallID != "s1" || made.Markdown != strings.TrimSpace(infographicPlan) || made.MimeType != "image/png" {
		t.Fatalf("artifact = %+v", made)
	}
	if string(made.Metadata) != `{"generation":{"attempts":1,"height":400,"provider_model":"test-image-model","question_preset_id":"infographic.visual-style","question_preset_version":1,"requested_style_id":"scientific","resolved_style_id":"scientific","text_check":{"score":1,"checked":`+decimal(len(items))+`,"missing":[],"garbled":[],"passed":true,"unavailable_reason":null},"width":640},"verification":{"reason":null,"verified":true}}` {
		t.Errorf("metadata = %s", made.Metadata)
	}
	stored := storedFile(t, local, l.keys[0])
	if config, err := png.DecodeConfig(bytes.NewReader(stored)); err != nil || config.Width != 640 || config.Height != 400 {
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
func drawWith(t *testing.T, l *ledger, answer json.RawMessage) (string, error) {
	t.Helper()
	s, _ := specialist(t, modeltest.NewScript(), l)
	call := toolCall(infographicArgs(t, "Photosynthesis", infographicPlan))
	call.Answer = answer
	return s.saveTool(Owner{ThreadID: 3, UserID: "u-1"}).Run(context.Background(), call)
}

// reading marks the first bad items of n as missing and garbled.
func reading(n, missing, garbled int) string {
	var items []map[string]any
	for i := 1; i <= n; i++ {
		switch {
		case i <= missing:
			items = append(items, map[string]any{"id": i, "status": "missing"})
		case i <= missing+garbled:
			items = append(items, map[string]any{"id": i, "status": "garbled", "seen": "Photosynthsis"})
		default:
			items = append(items, map[string]any{"id": i, "status": "present"})
		}
	}
	out, _ := json.Marshal(map[string]any{"items": items})
	return string(out)
}

// An image whose text does not read back is drawn once more with the
// problems named, and the attempt that reads better is the one kept.
func TestAnInfographicThatReadsBadlyIsDrawnOnceMore(t *testing.T) {
	n := len(plannedItems(infographicPlan, maxCheckedItems))
	first := GeneratedImage{Data: picture(300, 300, color.NRGBA{R: 255, A: 255})}
	second := GeneratedImage{Data: picture(400, 300, color.NRGBA{G: 255, A: 255})}

	// The second attempt reads perfectly: it is kept, with no warning.
	l := &ledger{}
	l.painter.images = []GeneratedImage{first, second}
	l.proofreader.readings = []string{reading(n, 2, 1), present(n)}
	out, err := drawWith(t, l, styleAnswer("auto"))
	if err != nil {
		t.Fatal(err)
	}
	// Both readings were made by the owner's vision model.
	if strings.Join(l.proofreader.users, ",") != "u-1,u-1" {
		t.Fatalf("read back for %v", l.proofreader.users)
	}
	if len(l.painter.prompts) != 2 || !strings.Contains(l.painter.prompts[1], "\n\nREPAIR ALL OF THESE FINDINGS\n- Missing from the image: \"Photosynthesis\"\n- Missing from the image: \"Light reactions\"\n- Garbled in the image: \"Thylakoid\" (reads \"Photosynthsis\")") {
		t.Fatalf("the repair prompt was %q", l.painter.prompts)
	}
	// "auto" was settled from the plan: nothing in it says otherwise, so it
	// is a sketch note.
	sketch, _ := styleNamed("sketch-note")
	if !strings.Contains(l.painter.prompts[0], sketch.description) || !strings.Contains(out, `"style":"Sketch Note"`) || strings.Contains(out, "warning") {
		t.Errorf("auto: %s", out)
	}
	if meta := string(l.created[0].Metadata); !strings.Contains(meta, `"attempts":2`) || !strings.Contains(meta, `"width":400`) || !strings.Contains(meta, `"requested_style_id":"auto","resolved_style_id":"sketch-note"`) || strings.Contains(meta, "warnings") {
		t.Errorf("metadata = %s", meta)
	}

	// The second attempt reads worse: the first is kept, and its warning is
	// saved and told to the model.
	l = &ledger{}
	l.painter.images = []GeneratedImage{first, second}
	l.proofreader.readings = []string{reading(n, 2, 1), reading(n, 5, 0)}
	out, err = drawWith(t, l, styleAnswer("clay"))
	if err != nil {
		t.Fatal(err)
	}
	score := decimal(int(float64(n-3) / float64(n) * 100))
	warning := "Only " + score + "% of the planned text reads correctly in the image after one regeneration. Check: Photosynthesis; Light reactions; \\\"Thylakoid\\\" (reads \\\"Photosynthsis\\\")"
	if !strings.Contains(out, `"warning":"`+warning+`"`) {
		t.Errorf("the model was told %s\nwant the warning %s", out, warning)
	}
	if meta := string(l.created[0].Metadata); !strings.Contains(meta, `"width":300`) || !strings.Contains(meta, `"verification":{"reason":null,"verified":true,"warnings":["`+warning+`"]}`) || !strings.Contains(meta, `"passed":false`) {
		t.Errorf("metadata = %s", meta)
	}

	// The second attempt fails outright: the first, which was paid for, is
	// still saved.
	l = &ledger{}
	l.painter.fail, l.painter.failAt = errors.New("503"), 2
	l.proofreader.readings = []string{reading(n, 4, 0)}
	if out, err = drawWith(t, l, styleAnswer("clay")); err != nil || len(l.created) != 1 || !strings.Contains(out, `"warning":"Only`) {
		t.Fatalf("after a failed repair: %s, %v", out, err)
	}

	// The first attempt fails: there is nothing to save, and the person is
	// told why.
	l = &ledger{}
	l.painter.fail = imageFailure(context.Background(), &proxyStatus{status: 429})
	if _, err = drawWith(t, l, styleAnswer("clay")); err == nil || err.Error() != imageModelBusy+refusedInfographicNote || len(l.created) != 0 {
		t.Fatalf("a busy image model: %v", err)
	}
}

// A reading that cannot be had does not cost the person their infographic:
// it is saved as not verified, with the reason.
func TestAnInfographicNobodyCouldReadBackIsSavedAsUnverified(t *testing.T) {
	for name, c := range map[string]struct {
		fail    error
		reading string
		reason  string
	}{
		"no reading model": {ErrNoImageReader, "", "no vision model configured"},
		// The account's own model that cannot be called says so in its
		// own sentence, not as a model that did not answer.
		"the chosen model removed": {&SightUnavailable{Sentence: "The model you chose from your own connection has been removed."}, "", "The model you chose from your own connection has been removed."},
		"the reading model down":   {errors.New("the model proxy answered 502: upstream"), "", "the vision model did not answer"},
		"an unreadable answer":     {nil, "I can see a lovely infographic.", "the vision model's answer was not readable"},
	} {
		l := &ledger{}
		l.proofreader.fail, l.proofreader.readings = c.fail, []string{c.reading}
		out, err := drawWith(t, l, styleAnswer("bricks"))
		if err != nil || len(l.painter.prompts) != 1 || strings.Contains(out, "warning") {
			t.Fatalf("%s: %s, %v (drawn %d times)", name, out, err, len(l.painter.prompts))
		}
		if meta := string(l.created[0].Metadata); !strings.Contains(meta, `"verification":{"reason":"`+c.reason+`","verified":false}`) || !strings.Contains(meta, `"score":null`) || strings.Contains(meta, "502") {
			t.Errorf("%s: metadata = %s", name, meta)
		}
	}
}

// What is not an answer to the style question draws nothing.
func TestAnInfographicIsNotDrawnWithoutAStyle(t *testing.T) {
	for name, c := range map[string]struct {
		answer string
		says   string
	}{
		"closed without choosing": {`{"type":"cancel","preset_id":"infographic.visual-style","preset_version":1,"tool_call_id":"d1/s1"}`, "closed the style question without choosing"},
		"another question":        {`{"type":"respond","preset_id":"something.else","preset_version":1,"answers":[{"question_id":"visual-style","option_ids":["clay"]}]}`, "not an answer to the infographic style question"},
		"an older set of styles":  {`{"type":"respond","preset_id":"infographic.visual-style","preset_version":7,"answers":[{"question_id":"visual-style","option_ids":["clay"]}]}`, "not an answer to the infographic style question"},
		"two styles":              {`{"type":"respond","preset_id":"infographic.visual-style","preset_version":1,"answers":[{"question_id":"visual-style","option_ids":["clay","anime"]}]}`, "exactly one choice"},
		"no style":                {`{"type":"respond","preset_id":"infographic.visual-style","preset_version":1,"answers":[]}`, "does not choose a style"},
		"a style there is not":    {`{"type":"respond","preset_id":"infographic.visual-style","preset_version":1,"answers":[{"question_id":"visual-style","option_ids":["vaporwave"]}]}`, `"vaporwave" is not one of the infographic styles`},
		"not JSON at all":         {`"clay"`, "not readable"},
	} {
		l := &ledger{}
		_, err := drawWith(t, l, json.RawMessage(c.answer))
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", name, err)
		}
		if len(l.painter.prompts) != 0 || len(l.created) != 0 {
			t.Errorf("%s: something was drawn or saved", name)
		}
	}

	// A plan that would be refused is refused before the person is asked.
	l := &ledger{}
	s, _ := specialist(t, modeltest.NewScript(), l)
	for name, c := range map[string]struct{ title, markdown, says string }{
		"no plan":         {"T", "  ", "markdown is empty"},
		"a caption":       {"T", "Photosynthesis is how plants eat.", "markdown is not a plan"},
		"too long":        {"T", "# T\n" + strings.Repeat("- label\n", 1200), "longer than 8000"},
		"no title":        {" ", infographicPlan, "the title is empty"},
		"several at once": {" ", "x", "the infographic was not made: the title is empty; markdown is not a plan"},
	} {
		_, err := s.saveTool(Owner{UserID: "u-1"}).Run(context.Background(), toolCall(infographicArgs(t, c.title, c.markdown)))
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestThePlannedItemsOfAPlan(t *testing.T) {
	got := plannedItems("# **Sleep** and [memory](https://x.example)\n\n## Stages ##\n- REM: 20-25% of the night\n1. `N3`: deep sleep\n* "+strings.Repeat("a very long sentence ", 6)+"\n- H2O and v1.2 are not figures; 3, 4 and 1,000.5 are\n- REM: again\nplain text with 7 in it", 30)
	// A range is its two ends; a list's own numbering, the tail of a word
	// and the tail of a version are not figures.
	want := []string{"Sleep and memory", "Stages", "REM", "20", "25%", "N3", "H2O and v1.2 are not figures; 3, 4 and 1,000.5 are", "3", "4", "1,000.5", "7"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("items\n got %q\nwant %q", got, want)
	}
	if capped := plannedItems(strings.Repeat("- a\n- b\n- c\n- d\n", 1)+"# 1 2 3 4 5 6", 5); len(capped) != 5 {
		t.Errorf("capped = %q", capped)
	}
}

func TestAutoPicksAStyleFromThePlan(t *testing.T) {
	for words, want := range map[string]string{
		"Quarterly revenue by market":           "professional",
		"A clinical trial of a new drug":        "scientific",
		"Steps to bake bread":                   "instructional",
		"Study notes on a clinical trial":       "sketch-note", // a study aid first
		"Nothing here says anything":            "sketch-note",
		"Restudying is not the word study":      "sketch-note",
		"The history of the printing press":     "editorial",
		"Key metrics":                           "bento-grid",
		"A friendly guide for children":         "kawaii",
		"Building with toy BRICKS, in capitals": "bricks",
	} {
		style, err := resolveStyle("auto", words)
		if err != nil || style.id != want {
			t.Errorf("%q: %s, %v; want %s", words, style.id, err, want)
		}
	}
	if style, err := resolveStyle("anime", "Quarterly revenue"); err != nil || style.id != "anime" {
		t.Errorf("an explicit choice: %s, %v", style.id, err)
	}
	// Every style "auto" can reach exists, and every style can be reached.
	if len(autoOrder) != len(visualStyles) {
		t.Fatalf("auto tries %d styles of %d", len(autoOrder), len(visualStyles))
	}
	for _, id := range append([]string{defaultAutoStyle}, autoOrder...) {
		if _, known := styleNamed(id); !known {
			t.Errorf("auto names a style %q that does not exist", id)
		}
	}
}

func TestAnImageIsMadeAnOpaquePNGOrRefused(t *testing.T) {
	// Half-transparent blue becomes blue on white, not blue on black.
	out, width, height, err := normalisedPNG(picture(300, 256, color.NRGBA{B: 255, A: 128}))
	if err != nil || width != 300 || height != 256 {
		t.Fatalf("%dx%d, %v", width, height, err)
	}
	flat, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, a := flat.At(10, 10).RGBA()
	if a != 0xffff || b>>8 != 255 || r>>8 < 120 || r>>8 > 135 || g>>8 != r>>8 {
		t.Errorf("pixel = %d %d %d %d", r>>8, g>>8, b>>8, a>>8)
	}
	if _, opaqueOut := flat.(*image.RGBA); !opaqueOut && flat.ColorModel() == color.NRGBAModel {
		t.Errorf("the PNG still has an alpha channel: %T", flat)
	}

	// A JPEG is accepted and stored as a PNG.
	var asJPEG bytes.Buffer
	if err := jpeg.Encode(&asJPEG, image.NewRGBA(image.Rect(0, 0, 512, 512)), nil); err != nil {
		t.Fatal(err)
	}
	if out, _, _, err = normalisedPNG(asJPEG.Bytes()); err != nil || !bytes.HasPrefix(out, []byte("\x89PNG")) {
		t.Fatalf("a JPEG: %v", err)
	}

	// Too small to be a page; a header claiming more pixels than may be
	// decoded (refused from the header alone); not an image; a WebP, which
	// is recognised and cannot be read here.
	huge := picture(256, 256, color.NRGBA{A: 255})
	copy(huge[16:24], []byte{0, 0, 0x40, 0, 0, 0, 0x40, 0}) // IHDR: 16384 x 16384
	for name, data := range map[string][]byte{
		"too small": picture(255, 300, color.NRGBA{A: 255}),
		"huge":      huge,
		"text":      []byte("<html>rate limited</html>"),
		"webp":      []byte("RIFF\x00\x00\x00\x00WEBPVP8 "),
	} {
		if _, _, _, err := normalisedPNG(data); !errors.Is(err, errUnusableImage) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// The copy sent to be read is no longer than 2048 on a side.
	big, _, _, err := normalisedPNG(picture(4100, 1000, color.NRGBA{R: 90, G: 90, B: 90, A: 255}))
	if err != nil {
		t.Fatal(err)
	}
	small, err := readingCopy(big)
	if err != nil {
		t.Fatal(err)
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(small))
	if err != nil || config.Width != 1366 || config.Height != 333 {
		t.Errorf("the reading copy is %dx%d, %v", config.Width, config.Height, err)
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
