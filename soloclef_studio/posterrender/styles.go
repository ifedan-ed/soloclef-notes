package posterrender

import (
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender"
)

// A style is a look a person picks for a poster: one of the deck's themes
// (colours and faces, so a poster and a deck from one notebook belong
// together) and a few choices of shape. Every style lays the poster out
// the same way; a style changes how it looks, never what fits, so a poster
// that fits in one fits in all.
//
// The ids are the ones the image-model infographic had for the looks that
// still mean something when the poster is drawn by code. "kawaii", "clay",
// "anime" and "bricks" were illustration styles of the image model and
// have no drawn counterpart, so they are gone (the server answers the
// installed phone builds that still send them; see deliverables).
type style struct {
	// theme is the deck theme it is drawn in (deckspec.Themes).
	theme string
	cards cardKind
	// marker numbers or letters the sections.
	marker markerKind
	// header is how the title is set off.
	header headerKind
	// numbers is how the key numbers are drawn.
	numbers numberKind
	// about is the style in a few words, for a person choosing one.
	about string
}

type cardKind int

const (
	// cardFill: a tinted panel with rounded corners.
	cardFill cardKind = iota + 1
	// cardOutline: an ink outline on the paper (marker or hairline).
	cardOutline
	// cardRuled: no panel, an accent rule over the heading (editorial).
	cardRuled
)

type markerKind int

const (
	markerNone markerKind = iota
	// markerNumber: 1, 2, 3 in an accent badge, for steps.
	markerNumber
	// markerLetter: A, B, C, as a scientific figure's panels.
	markerLetter
)

type headerKind int

const (
	// headerRule: the title on the paper, a short accent rule under it.
	headerRule headerKind = iota + 1
	// headerBand: the title on a band of the theme's ink.
	headerBand
)

type numberKind int

const (
	// numbersTiles: each number in a panel like the sections'.
	numbersTiles numberKind = iota + 1
	// numbersAccent: each number in an accent-filled tile, white type.
	numbersAccent
	// numbersBand: the numbers side by side on one accent band.
	numbersBand
	// numbersRuled: the numbers between thin rules, no panels.
	numbersRuled
)

// styles is every style, by id, in the order a person is offered them.
var styles = []struct {
	id string
	style
}{
	{"sketch-note", style{theme: "whiteboard", cards: cardOutline, header: headerRule, numbers: numbersTiles,
		about: "Marker outlines on white, one blue accent"}},
	{"editorial", style{theme: "classic", cards: cardRuled, header: headerRule, numbers: numbersRuled,
		about: "Serif headline on warm paper, ruled columns"}},
	{"instructional", style{theme: "amber", cards: cardFill, marker: markerNumber, header: headerRule, numbers: numbersTiles,
		about: "Numbered steps, amber accent"}},
	{"bento-grid", style{theme: "teal", cards: cardFill, header: headerRule, numbers: numbersAccent,
		about: "Tiles of different sizes, big numbers in their own tiles"}},
	{"scientific", style{theme: "clinical", cards: cardOutline, marker: markerLetter, header: headerRule, numbers: numbersTiles,
		about: "Panels lettered A, B, C, blue on white"}},
	{"professional", style{theme: "swiss", cards: cardFill, header: headerBand, numbers: numbersBand,
		about: "Slate and white, a band of key numbers"}},
}

// Styles is every style id, in the order a person is offered them.
func Styles() []string {
	ids := make([]string, len(styles))
	for i, s := range styles {
		ids[i] = s.id
	}
	return ids
}

// About is a style in a few words, for its card in a picker; "" for an id
// that is no style.
func About(id string) string {
	if s, ok := styleNamed(id); ok {
		return s.about
	}
	return ""
}

func styleNamed(id string) (style, bool) {
	for _, s := range styles {
		if s.id == id {
			return s.style, true
		}
	}
	return style{}, false
}

// look is a style with its theme's colours read.
type look struct {
	style
	deckrender.Look
}

func lookOf(id string) (look, error) {
	s, ok := styleNamed(id)
	if !ok {
		return look{}, fmt.Errorf("posterrender: %q is not a style", id)
	}
	l, ok := deckrender.LookOf(s.theme)
	if !ok {
		// The table above names deck themes; a missing one is a broken
		// build, which the tests catch.
		return look{}, fmt.Errorf("posterrender: style %q names the deck theme %q, which does not exist", id, s.theme)
	}
	return look{style: s, Look: l}, nil
}

// Canvas is one orientation's page, in the poster's own units (pixels of
// the PNG at scale 1).
type Canvas struct {
	ID            string
	Width, Height float64
}

// Canvases is every orientation, landscape first: the studio dialog's
// default. The sizes are the image model's old pages, so a new poster opens
// where an old infographic did, in the same frame.
var Canvases = []Canvas{
	{"landscape", 1536, 1024},
	{"portrait", 1024, 1536},
	{"square", 1024, 1024},
}

// CanvasNamed is an orientation's canvas.
func CanvasNamed(id string) (Canvas, bool) {
	for _, c := range Canvases {
		if c.ID == id {
			return c, true
		}
	}
	return Canvas{}, false
}
