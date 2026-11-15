package deckspec

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Schema is the deck's JSON Schema, for a tool's parameters or a reply's
// format. Which fields go with which kind is said in each field's
// description and checked by Check, not expressed as oneOf over ten
// shapes: several providers refuse a schema that does that.
func Schema(narrated bool) json.RawMessage {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	list := func(item map[string]any, description string) map[string]any {
		return map[string]any{"type": "array", "items": item, "description": description}
	}
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	kinds := make([]string, len(Kinds))
	for i, k := range Kinds {
		kinds[i] = string(k)
	}
	slide := map[string]any{
		"kind":        map[string]any{"type": "string", "enum": kinds},
		"title":       str("The slide's heading. Every kind but quote."),
		"subtitle":    str("title and section only: a second line."),
		"bullets":     list(str("One point; may lead with a **bold** phrase."), "bullets only: 2 to 6 points."),
		"left":        object(map[string]any{"heading": str(""), "bullets": list(str(""), "1 to 5 points.")}, "heading", "bullets"),
		"right":       object(map[string]any{"heading": str(""), "bullets": list(str(""), "1 to 5 points.")}, "heading", "bullets"),
		"columns":     list(str(""), "comparison only: 2 or 3 things compared."),
		"rows":        list(object(map[string]any{"aspect": str(""), "cells": list(str(""), "One per column.")}, "aspect", "cells"), "comparison only: 2 to 5 aspects."),
		"quote":       str("quote only: the quotation, without quotation marks."),
		"attribution": str("quote only: who said or wrote it."),
		"events":      list(object(map[string]any{"label": str("When: a year, a date, a stage."), "text": str("")}, "label", "text"), "timeline only: 3 to 6 events in order."),
		"stats":       list(object(map[string]any{"value": str("The figure as it reads: 72%, 1.2 million, 3x."), "label": str("What it measures.")}, "value", "label"), "stats only: 2 to 4 figures."),
		"style":       map[string]any{"type": "string", "enum": []string{DiagramFlow, DiagramCycle}, "description": "diagram only."},
		"steps":       list(object(map[string]any{"label": str(""), "text": str("Optional.")}, "label"), "diagram only: 3 to 6 stages in order."),
		"bars":        list(object(map[string]any{"label": str(""), "value": map[string]any{"type": "number"}}, "label", "value"), "chart only: 2 to 8 bars, values from the sources."),
		"unit":        str("chart only: the values' unit."),
		"caption":     str("chart only: what the chart shows and where the numbers come from."),
		"notes":       str("Speaker notes: what to say over the slide."),
		"cites":       list(map[string]any{"type": "integer"}, "Numbers of the deck's sources this slide draws on, counted from 1."),
	}
	required := []string{"kind"}
	if narrated {
		slide["narration"] = list(str(""), "What the video says over this slide: exactly one passage as it appears, then exactly one per part (bullet, column, row, event, figure, stage, the bars) as it comes in.")
		required = append(required, "narration")
	}
	ids := themeIDs()
	schema := object(map[string]any{
		"title":    str("The deck's title, one line."),
		"subtitle": str("Optional second line for the title slide."),
		"theme":    map[string]any{"type": "string", "enum": ids},
		"sources":  list(object(map[string]any{"title": str(""), "detail": str("Authors and year, publisher or address. Optional.")}, "title"), "What the slides cite, in the order they are numbered."),
		"slides":   list(object(slide, required...), ""),
	}, "title", "theme", "slides")
	out, _ := json.Marshal(schema)
	return out
}

// Instructions is what a model is told about writing a deck: the kinds,
// the limits in words, and what makes a slide good. narrated adds what a
// video's narration is.
func Instructions(narrated bool) string {
	var b strings.Builder
	b.WriteString(`A deck is structured content, and the application draws it in a fixed, designed theme: you write what each slide says, never how it looks. Text is sized to fit its place on the slide, so keep to the word limits below; a slide whose text cannot fit is sent back to you with the words to cut.

Deck fields: title (up to ` + fmt.Sprint(words(maxDeckTitle)) + ` words), optional subtitle, theme, optional sources, slides.

Themes (choose the one that suits the audience, or the one the task names):
`)
	for _, t := range Themes {
		b.WriteString("- " + t.ID + ": " + t.Suit + ".\n")
	}
	fmt.Fprintf(&b, `
Slide kinds and their fields (every slide has kind and title, except that a quote's title is optional):
- title: the opening slide. subtitle optional (up to %d words). Use it once, first.
- section: a divider before a new part of a longer deck. subtitle optional.
- bullets: 2 to 6 bullets of up to %d words each; a bullet may lead with a short **bold** phrase, like "**Light reactions:** make ATP and NADPH".
- two_column: left and right, each a heading (up to %d words) and 1 to 5 bullets of up to %d words.
- comparison: columns, 2 or 3 names; rows, 2 to 5, each an aspect (up to %d words) and one cell per column (up to %d words).
- quote: quote (up to %d words, no quotation marks), attribution.
- timeline: events, 3 to 6 in order, each a label (when: up to %d words) and text (up to %d words).
- stats: stats, 2 to 4, each a value as it should read ("72%%", "1.2 million", "3x"; up to %d characters) and a label (up to %d words). Only figures the material states.
- diagram: style "flow" (stages in order) or "cycle" (the last leads back to the first); steps, 3 to 6, each a label (up to %d words) and optional text (up to %d words).
- chart: a bar chart. bars, 2 to 8, each a label and a number from the material; unit; caption saying what is measured and where it comes from. Never invent or estimate a number.

Every slide may also have notes (speaker notes, up to %d words) and cites (the numbers of the sources it draws on, counted from 1 in the deck's sources list). When the material names its sources, list them in sources (title, and detail such as authors and year) and cite them on the slides that use them; a closing slide listing them is added for you, so do not write one.

What makes a good deck:
- One idea per slide, with a title that states it ("Insulin lowers blood glucose", not "Insulin").
- Choose the kind that shows the shape of the idea: a sequence is a diagram or a timeline, two options are a comparison, a few striking numbers are stats, measured quantities are a chart. Bullets are for what has no better shape; do not make every slide bullets.
- Short, concrete text: phrases, not paragraphs. Detail goes in the notes.
- Plain text everywhere: no Markdown except the one bold lead phrase of a bullet, no HTML, no emoji, no bullet characters (the layout draws its own).
- Only facts the material supports.
`, words(maxSubtitle), words(maxBullet), words(maxColumnHead), words(maxColumnBullet), words(maxAspect), words(maxCell),
		words(maxQuote), words(maxEventLabel), words(maxEventText), maxStatValue, words(maxStatLabel), words(maxStepLabel), words(maxStepText), words(maxNotes))
	if narrated {
		fmt.Fprintf(&b, `
Narration (a video's slides, every one): narration is a list of passages spoken over the slide, and each part of the slide comes in as its passage starts. Write exactly one passage per part, in order, never more and never fewer:
- The first passage is spoken as the slide appears: it introduces the slide.
- Then one passage for each part as it comes in: each bullet; the left column, then the right; each comparison row; each timeline event; each figure of a stats slide; each diagram stage; one for a chart's bars; one for a title's subtitle or a quote's attribution, when the slide has one.
- So a bullets slide with 4 bullets has 5 passages, a timeline with 3 events has 4, a stats slide with 2 figures has 3, a two_column slide has 3, a chart has 2, and a section slide (or a title or quote with nothing after it) has 1. Count them: a slide with another count is sent back.
- Each passage talks about its own part: the bullet, event or figure that comes in as it is spoken.
Up to %d words over one slide.
- Narration explains and connects; it does not read the slide aloud word for word.
- Write it to be heard: short sentences, numbers and symbols as they are said ("seventy-two percent", "carbon dioxide"), no lists, no parentheses, no abbreviations a listener cannot hear.
`, words(maxNarration))
	}
	return b.String()
}
