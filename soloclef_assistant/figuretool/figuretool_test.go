package figuretool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	figures "github.com/ifedan-ed/figures-go"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// A figure is drawn by code: the model reads figures-go's short answer, and
// the card, drawn again from the call's own arguments, carries the light
// and dark SVGs and nothing of the spec.
func TestAFigureIsDrawnAndItsCardCarriesTheSVGs(t *testing.T) {
	args, _ := figures.Example("bar_chart")
	tl := Tool()
	if err := tl.Validate(); err != nil || tl.Approval != tool.Auto || !tl.Parallel {
		t.Fatalf("the tool: %v, %+v", err, tl)
	}
	out, err := tl.Run(context.Background(), tool.Call{Name: ToolName, Args: json.RawMessage(args)})
	if err != nil || !strings.Contains(out, `"shown_to_user":true`) || strings.Contains(out, "<svg") {
		t.Fatalf("the model read %q, %v", out, err)
	}
	card, mine := Card(event.Event{Kind: event.ToolResult, Tool: ToolName, Args: args, Text: out})
	figure, ok := card.(FigureCard)
	if !mine || !ok || figure.Type != "bar_chart" || figure.Title != "WHO fast-breathing thresholds by age" || figure.Caption == "" ||
		figure.Width != 400 || figure.Height <= 0 || !strings.HasPrefix(figure.SVG, "<svg") || !strings.HasPrefix(figure.SVGDark, "<svg") || figure.SVG == figure.SVGDark {
		t.Fatalf("card = %+v", card)
	}
	encoded, _ := json.Marshal(card)
	if strings.Contains(string(encoded), `"spec"`) {
		t.Error("the card carries the spec")
	}
}

// A refusal goes back to the model in figures-go's words; its card says
// only that nothing was drawn. Another tool's result is not this card's.
func TestARefusalAndAnotherTool(t *testing.T) {
	_, err := Tool().Run(context.Background(), tool.Call{Name: ToolName, Args: json.RawMessage(`{"type":"mermaid","title":"x","alt":"y","spec":{"mermaid":"graph TD"}}`)})
	if err == nil || !strings.Contains(err.Error(), `"shown_to_user":false`) || !strings.Contains(err.Error(), "is not drawn here") {
		t.Fatalf("a mermaid figure: %v", err)
	}
	card, mine := Card(event.Event{Kind: event.ToolResult, Tool: ToolName, IsError: true, Text: "error: " + err.Error()})
	if failed, ok := card.(FigureFailed); !mine || !ok || failed.Error == "" {
		t.Errorf("the failed card = %+v", card)
	}
	if _, mine := Card(event.Event{Kind: event.ToolResult, Tool: "generate_image"}); mine {
		t.Error("another tool's result was taken for a figure")
	}
}
