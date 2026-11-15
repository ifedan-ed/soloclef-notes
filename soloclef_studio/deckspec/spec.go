// Package deckspec is what a model writes when it makes a slide deck or a
// narrated video: the deck's content as structured data, never code and
// never a picture. Go draws the deck from it (package deckrender), so a
// deck is one model call and a few milliseconds of drawing, not a program
// written, run, measured and repaired in a sandbox.
//
// The idea is PedsHub Tools' (tools-go internal/deck and pptxrender): the
// model writes an outline of typed slides, trusted code lays every slide
// out in a fixed theme, and the text is sized to its box while it is
// drawn. Soloclef's version is its own, written for Soloclef: typed Go
// structs instead of Python's dicts, slide kinds a study deck needs
// (timelines, numbers, processes, comparisons), citations to the
// notebook's sources, and narration per slide for the video that is drawn
// from the same deck.
//
// The package decides what a valid deck is (Check), how many parts of each
// slide appear one after another (Steps), what a deck says as text
// (Markdown), and what a model is told (Instructions, Schema). It draws
// nothing.
package deckspec

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Kind is a slide's layout: what the slide is for, which decides which of
// its fields it has and how it is drawn.
type Kind string

// The kinds. Each is a layout a study deck needs often enough to be drawn
// well every time; anything else is said with bullets.
const (
	KindTitle      Kind = "title"
	KindSection    Kind = "section"
	KindBullets    Kind = "bullets"
	KindTwoColumn  Kind = "two_column"
	KindComparison Kind = "comparison"
	KindQuote      Kind = "quote"
	KindTimeline   Kind = "timeline"
	KindStats      Kind = "stats"
	KindDiagram    Kind = "diagram"
	KindChart      Kind = "chart"
)

// Kinds is every kind, in the order the instructions describe them.
var Kinds = []Kind{KindTitle, KindSection, KindBullets, KindTwoColumn, KindComparison, KindQuote, KindTimeline, KindStats, KindDiagram, KindChart}

// The two diagram styles.
const (
	DiagramFlow  = "flow"
	DiagramCycle = "cycle"
)

// Deck is a whole deck as the model writes it.
type Deck struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	// Theme is one of Themes' ids: the look, chosen by the model from the
	// catalogue (or named by the person's request).
	Theme string `json:"theme"`
	// Sources are what the slides cite, numbered from 1 in this order. A
	// deck with sources ends with a slide listing them (drawn, not
	// written).
	Sources []Source `json:"sources,omitempty"`
	Slides  []Slide  `json:"slides"`
}

// Source is one cited source: a document of the notebook, a paper, a page.
type Source struct {
	Title string `json:"title"`
	// Detail is what finds it again: authors and year, a publisher, an
	// address. Optional.
	Detail string `json:"detail,omitempty"`
}

// Slide is one slide. Which fields a slide has is decided by its kind
// (Check says so field by field); the rest stay empty.
type Slide struct {
	Kind  Kind   `json:"kind"`
	Title string `json:"title,omitempty"`
	// Subtitle is a title or section slide's second line.
	Subtitle string `json:"subtitle,omitempty"`
	// Bullets are a bullets slide's points. A point may start with a lead
	// phrase in **bold**.
	Bullets []string `json:"bullets,omitempty"`
	// Left and Right are a two-column slide's columns.
	Left  *Column `json:"left,omitempty"`
	Right *Column `json:"right,omitempty"`
	// Columns and Rows are a comparison: two or three things compared
	// aspect by aspect.
	Columns []string `json:"columns,omitempty"`
	Rows    []Row    `json:"rows,omitempty"`
	// Quote and Attribution are a quote slide's.
	Quote       string `json:"quote,omitempty"`
	Attribution string `json:"attribution,omitempty"`
	// Events are a timeline's, in order.
	Events []Event `json:"events,omitempty"`
	// Stats are a stats slide's figures.
	Stats []Stat `json:"stats,omitempty"`
	// Style and Steps are a diagram's: a flow left to right, or a cycle.
	Style string `json:"style,omitempty"`
	Steps []Step `json:"steps,omitempty"`
	// Bars, Unit and Caption are a bar chart's. Every bar is a number the
	// sources give; the chart is drawn from them, never estimated.
	Bars    []Bar  `json:"bars,omitempty"`
	Unit    string `json:"unit,omitempty"`
	Caption string `json:"caption,omitempty"`
	// Notes are the speaker notes.
	Notes string `json:"notes,omitempty"`
	// Cites are the numbers of the deck's sources this slide draws on.
	Cites []int `json:"cites,omitempty"`
	// Narration is what a video says over this slide, as passages: the
	// first is spoken as the slide appears, and each next one as the next
	// part of the slide comes in, exactly one per part (Steps). Only a
	// video's deck has it.
	Narration []string `json:"narration,omitempty"`
}

// Column is one column of a two-column slide.
type Column struct {
	Heading string   `json:"heading"`
	Bullets []string `json:"bullets"`
}

// Row is one aspect of a comparison: its name and one cell per column.
type Row struct {
	Aspect string   `json:"aspect"`
	Cells  []string `json:"cells"`
}

// Event is one point on a timeline.
type Event struct {
	// Label is when: "1969", "Week 2", "Stage 1".
	Label string `json:"label"`
	Text  string `json:"text"`
}

// Stat is one figure: a value with its unit as it should read ("72%",
// "1.2 million", "3x") and what it measures.
type Stat struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Step is one stage of a diagram.
type Step struct {
	Label string `json:"label"`
	Text  string `json:"text,omitempty"`
}

// Bar is one bar of a chart.
type Bar struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

// Decode reads a deck from JSON. A field the schema does not have is
// refused by name: a model that invents one ("image", "layout") believes
// it asked for something that would not be drawn, and should be told.
func Decode(raw []byte) (Deck, error) {
	var deck Deck
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&deck); err != nil {
		return Deck{}, fmt.Errorf("the deck does not fit the schema: %w", err)
	}
	if decoder.More() {
		return Deck{}, fmt.Errorf("the deck does not fit the schema: there is more after the deck's object")
	}
	return deck, nil
}
