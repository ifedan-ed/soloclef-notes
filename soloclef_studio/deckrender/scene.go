package deckrender

// A slide is laid out once, as a scene: every shape, line and block of text
// at its place in points, with the text already wrapped and sized, and with
// the build step each comes in on. The deck file (topptx.go) and the
// video's frames (tosvg.go) are two drawings of the same scene, so the
// video shows the deck's slides, not a look-alike.

// The slide, in points: 13.333 by 7.5 inches, PowerPoint's widescreen.
const (
	SlideW = 960.0
	SlideH = 540.0
)

// Motion is how something comes in during a video. A deck file has no
// builds (it is presented at the speaker's pace, and a reader wants every
// part on the page), so it ignores motion.
type Motion int

const (
	// Rise fades in while moving up a little: text and cards.
	Rise Motion = iota
	// Fade only fades in: backgrounds and decoration.
	Fade
	// Pop fades in while growing from a little smaller: markers, dots,
	// diagram nodes.
	Pop
	// GrowX grows from its left edge to its full width: a chart's bars.
	GrowX
	// Draw draws a line from its start to its end: axes, arrows.
	Draw
)

// Reveal is when and how an element comes in: on which build step, and
// how long after that step begins (a stagger, in seconds, so parts that
// come in together arrive one after another).
type Reveal struct {
	Step   int
	Motion Motion
	Delay  float64
}

// Box is a shape: a filled or outlined rectangle, rounded rectangle or
// ellipse.
type Box struct {
	Name       string
	X, Y, W, H float64
	// Preset is "rect", "roundRect" or "ellipse".
	Preset string
	Fill   string
	Line   string
	LineW  float64
	// Radius is a rounded rectangle's corner, in points.
	Radius float64
	Reveal Reveal
}

// Stroke is a straight line, with an arrowhead at its end when Arrow.
type Stroke struct {
	Name           string
	X1, Y1, X2, Y2 float64
	Color          string
	Width          float64
	Arrow          bool
	Reveal         Reveal
}

// Text is a block of text in a box, fitted.
type Text struct {
	Name       string
	X, Y, W, H float64
	Paras      []para
	Family     family
	Fit        fitted
	Color      string
	// Align is "l", "ctr" or "r"; Anchor "t", "ctr" or "b".
	Align, Anchor string
	// Spacing is the line spacing the block was fitted at (1 is single).
	Spacing float64
	Reveal  Reveal
	// Count makes a number count up to its value as it comes in (a
	// statistic): the frames draw it rising; the deck shows the value.
	Count bool
}

// Scene is one slide laid out.
type Scene struct {
	Name       string
	Background string
	// Elements are *Box, *Stroke and *Text, in the order they are drawn.
	Elements   []any
	Notes      string
	Transition string
	// Steps is how many build steps the slide has (deckspec.Steps).
	Steps int
}

func (s *Scene) add(elements ...any) { s.Elements = append(s.Elements, elements...) }
