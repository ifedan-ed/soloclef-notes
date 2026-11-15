// Package figuretool is the chat's draw_figure tool: a chart or a diagram
// in an answer, drawn by trusted code (soloclef_studio/figurekit, over
// figures-go), as PedsHub's AI Mode and Tools draw theirs.
//
// The model asks for a figure of a known type with its content; the
// arguments are checked and refused in words the model corrects from, and
// the figure is drawn as two small sanitised SVGs, light and dark. No
// picture model is called and nothing is stored apart from the message:
//
//   - The model reads only figures-go's short answer ("shown to the user",
//     the type and the title), never the drawing, so a figure costs the
//     turn the tokens of its spec.
//   - The page's card (Card) is drawn again from the call's own arguments
//     when the call ends: drawing is deterministic and takes milliseconds,
//     and so the SVGs travel only in the card, which is saved with the
//     message and is what the page and the phone show, in an old chat as
//     in a new one.
package figuretool

import (
	"context"
	"encoding/json"
	"errors"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_studio/figurekit"
)

// ToolName is the tool's name, keyed to the page's and the phone's figure
// card.
const ToolName = figurekit.ToolName

// Guidance is the line the system prompt offers the tool with.
const Guidance = figurekit.Guidance

// Tool is draw_figure. It runs unasked (it draws in the answer and sends
// nothing anywhere) and beside other calls (it reads nothing and writes
// nothing).
func Tool() tool.Tool {
	return tool.Tool{
		Name:        ToolName,
		Description: figurekit.Description(),
		Parameters:  figurekit.Parameters,
		Approval:    tool.Auto,
		Parallel:    true,
		Run: func(_ context.Context, call tool.Call) (string, error) {
			f, err := figurekit.Render(call.Args)
			var refused *figurekit.Refusal
			switch {
			case errors.As(err, &refused):
				// The model's to correct: figures-go's own words.
				return "", errors.New(figurekit.RefusalForModel(err))
			case err != nil:
				return "", errors.New("the figure could not be drawn because of a fault in the application; answer without it")
			}
			return figurekit.ForModel(f), nil
		},
	}
}

// FigureCard is what the page's and the phone's figure card draw: the
// figure's words and size, and its light and dark SVGs. It is an allowlist,
// as every card is (it is saved with the message and synced to every open
// page): the checked spec is not in it.
type FigureCard struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Alt     string `json:"alt"`
	Caption string `json:"caption,omitempty"`
	// Width and Height are the figure's size in its own units: the box a
	// reader keeps for it, so nothing moves when it is drawn.
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	SVG     string  `json:"svg"`
	SVGDark string  `json:"svg_dark"`
}

// FigureFailed is the card of a call that drew nothing: the page shows
// nothing for it (the model corrects and calls again, or answers in words).
type FigureFailed struct {
	Error string `json:"error"`
}

// Card is the card for a finished draw_figure call, and whether the result
// is one this package has a card for.
func Card(result event.Event) (any, bool) {
	if result.Tool != ToolName {
		return nil, false
	}
	if result.IsError {
		return FigureFailed{Error: "The figure was not drawn."}, true
	}
	f, err := figurekit.Render([]byte(result.Args))
	if err != nil {
		// The call succeeded with these very arguments a moment ago.
		return FigureFailed{Error: "The figure was not drawn."}, true
	}
	var a struct {
		Caption string `json:"caption"`
	}
	_ = json.Unmarshal([]byte(result.Args), &a)
	return FigureCard{Type: f.Type, Title: f.Title, Alt: f.Alt, Caption: a.Caption, Width: f.Width, Height: f.Height, SVG: f.SVG, SVGDark: f.SVGDark}, true
}
