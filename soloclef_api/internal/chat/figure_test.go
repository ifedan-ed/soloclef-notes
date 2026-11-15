package chat

import (
	"encoding/json"
	"strings"
	"testing"

	figures "github.com/ifedan-ed/figures-go"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/figuretool"
)

// A figure the model asks for is drawn by code and kept with the message:
// the saved card carries its light and dark SVGs (which the model never
// read), its step is "Drew a figure", and an old chat shows it from the
// message alone.
func TestAFigureIsDrawnAndKeptWithTheMessage(t *testing.T) {
	args, _ := figures.Example("timeline")
	script := modeltest.NewScript(
		modeltest.Reply{Calls: call("f1", figuretool.ToolName, args)},
		modeltest.Reply{Text: "The timeline shows the course."},
	)
	a := newApp(t, script, nil, func(s *Service, sc *scripted) {
		assembly, _, _, _ := production(t, script, script, sc.rows, sc.store, connected{})
		s.Assembler = assembly
	})
	frames, done := a.ask(ada, `{"chat_id":1,"user_query":"Show me the course of Kawasaki disease"}`)
	page := readAll(t, frames)
	if !done || page.failure != nil {
		t.Fatalf("done %v, failure %v", done, page.failure)
	}
	if steps, _ := page.steps(); len(steps) != 1 || steps[0]["title"] != "Drew a figure" || steps[0]["kind"] != "figure.draw" {
		t.Errorf("steps %v", steps)
	}
	// The model was told it was shown, and never given the drawing.
	second := script.Requests()[1]
	last := second.Messages[len(second.Messages)-1]
	if !strings.Contains(last.Content, `"shown_to_user":true`) || strings.Contains(last.Content, "<svg") {
		t.Errorf("the model read %.200q", last.Content)
	}
	var card map[string]any
	for _, part := range contentOf(t, a.rows.assistant(1)[0]) {
		if part["toolName"] == figuretool.ToolName {
			card = part
		}
	}
	if card == nil {
		t.Fatal("the figure's card was not saved")
	}
	result, _ := card["result"].(map[string]any)
	svg, _ := result["svg"].(string)
	dark, _ := result["svg_dark"].(string)
	if result["type"] != "timeline" || result["title"] != "Course of Kawasaki disease" || !strings.HasPrefix(svg, "<svg") || !strings.HasPrefix(dark, "<svg") || result["width"] != 400.0 {
		encoded, _ := json.Marshal(result)
		t.Errorf("the saved card = %.300s", encoded)
	}
}
