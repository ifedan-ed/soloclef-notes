package deliverables

import (
	"image/color"
	"strings"
	"testing"
)

var blue = color.NRGBA{R: 10, G: 120, B: 200, A: 255}

// The orientation a studio dialog chose is the size the image model is
// asked for, and the shape the prompt describes, on every attempt; and it
// is kept in the artifact's record.
func TestAnInfographicIsDrawnInTheOrientationAsked(t *testing.T) {
	for orientation, want := range map[string]struct{ size, words string }{
		"landscape": {"1536x1024", "Canvas: landscape, 3:2"},
		"portrait":  {"1024x1536", "Canvas: portrait, 2:3"},
		"square":    {"1024x1024", "Canvas: square, 1:1"},
	} {
		l := &ledger{}
		l.painter.images = []GeneratedImage{
			{Data: picture(384, 256, blue)},
			{Data: picture(384, 256, blue)},
		}
		items := plannedItems(infographicPlan, maxCheckedItems)
		// The first reads badly, so it is drawn twice: both on the canvas.
		l.proofreader.readings = []string{reading(len(items), 3, 0), present(len(items))}
		answer, err := StyleAnswer("scientific", orientation)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := drawWith(t, l, answer); err != nil {
			t.Fatalf("%s: %v", orientation, err)
		}
		if len(l.painter.sizes) != 2 || l.painter.sizes[0] != want.size || l.painter.sizes[1] != want.size {
			t.Errorf("%s: the image model was asked for %v", orientation, l.painter.sizes)
		}
		for _, prompt := range l.painter.prompts {
			if !strings.Contains(prompt, "\n\n"+want.words) {
				t.Errorf("%s: the prompt does not describe the canvas: %q", orientation, prompt)
			}
		}
		if !strings.Contains(string(l.created[0].Metadata), `"orientation":"`+orientation+`"`) {
			t.Errorf("%s: metadata = %s", orientation, l.created[0].Metadata)
		}
	}

	// A chat's answer names none: no size, and nothing said of the shape.
	l := &ledger{}
	l.painter.images = []GeneratedImage{{Data: picture(384, 256, blue)}}
	l.proofreader.readings = []string{present(len(plannedItems(infographicPlan, maxCheckedItems)))}
	if _, err := drawWith(t, l, styleAnswer("scientific")); err != nil {
		t.Fatal(err)
	}
	if l.painter.sizes[0] != "" || strings.Contains(l.painter.prompts[0], "Canvas:") || strings.Contains(string(l.created[0].Metadata), "orientation") {
		t.Errorf("an answer with no orientation: size %q, prompt %q, metadata %s", l.painter.sizes[0], l.painter.prompts[0], l.created[0].Metadata)
	}

	// An orientation this version does not draw is refused, and nothing is
	// drawn for it.
	l = &ledger{}
	bad := []byte(strings.Replace(string(styleAnswer("scientific")), `"answers"`, `"orientation":"panorama","answers"`, 1))
	if _, err := drawWith(t, l, bad); err == nil || len(l.painter.prompts) != 0 {
		t.Errorf("an unknown orientation: %v, drawn %d times", err, len(l.painter.prompts))
	}
}
