// Package figurekit is Soloclef's set of figures: the figures-go library
// (github.com/ifedan-ed/figures-go, PedsHub Tools' and AI Mode's figures)
// with the figures Soloclef can draw, offered to a model in words.
//
// A figure is never a picture a model paints. The model asks for a figure
// of a known type with its content (a bar chart's bars, a flowchart's
// steps, or its own SVG checked as strictly), figures-go checks every
// field and refuses in words the model can correct from, and trusted code
// lays the figure out and draws it as a small, sanitised SVG. A figure
// costs the tokens of its spec, and the same spec always draws the same
// figure.
//
// # Why only the SVG figures
//
// figures-go has two free-form types drawn by a browser, html and mermaid:
// their pictures exist only once a Chromium (a Gotenberg service) has drawn
// them as PNGs. Soloclef offers neither. Every figure here is an SVG, so
//
//   - the chat sends it as text in its card, sharp at any size, light and
//     dark, with no picture to store, link or fetch;
//   - an infographic poster nests it as vector drawing (posterrender);
//   - one renderer (resvg, already in the worker's image) makes every PNG,
//     and no deployment runs a browser for two rarely needed types.
//
// What html and mermaid covered is covered by the typed diagrams
// (flowchart, pathway, timeline, cycle, concept map, comparison table,
// Venn, schematic) and by the model's own svg. A call naming either is
// refused in words, like any other mistake.
//
// The tool's description is figures-go's own, with the two types' sections
// and the sentences that point to them taken out (offered, below). Each of
// those edits must apply exactly once: a figures-go version that words them
// differently fails this package's tests and its start, never a prompt that
// quietly still offers mermaid.
package figurekit

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	figures "github.com/ifedan-ed/figures-go"
)

// ToolName is the tool the model calls, figures-go's own name.
const ToolName = figures.ToolName

// browserDrawn are the figures-go types Soloclef does not offer (package
// comment).
var browserDrawn = []string{"html", "mermaid"}

// Types is every figure type offered, sorted.
func Types() []string {
	var out []string
	for _, t := range figures.Types() {
		if !slices.Contains(browserDrawn, t) {
			out = append(out, t)
		}
	}
	return out
}

// edits are the changes to figures-go's description that take out the two
// browser-drawn types: each old text must occur exactly once.
var edits = []struct{ old, new string }{
	{"three free-form types (svg, html, mermaid) take a drawing you write yourself",
		"one free-form type (svg) takes a drawing you write yourself"},
	{"never draw data in svg, html or mermaid.", "never draw data in svg."},
	{"- Only when nothing above fits: mermaid for other diagram shapes (sequence, state, mind map, Gantt), svg for a mechanism or a sketch you must draw yourself, html for a styled layout of boxes and text.",
		"- Only when nothing above fits: svg for a mechanism, a sketch, or another diagram shape (a sequence, a state diagram) you must draw yourself."},
	{", for a sequence or state diagram use mermaid.", ", for a sequence or state diagram draw your own svg."},
}

// offered is the description with the browser-drawn types taken out, made
// once when the package loads.
var offered = func() string {
	d, err := withoutBrowserTypes(figures.Description())
	if err != nil {
		// A figures-go upgrade reworded what is edited here: the tests
		// fail on this before any build ships.
		panic("figurekit: " + err.Error())
	}
	return d
}()

// withoutBrowserTypes is a description without the browser-drawn types:
// the edits applied, then each such type's own section removed.
func withoutBrowserTypes(d string) (string, error) {
	for _, e := range edits {
		if n := strings.Count(d, e.old); n != 1 {
			return "", fmt.Errorf("figures-go's description has %q %d times, not once", e.old, n)
		}
		d = strings.Replace(d, e.old, e.new, 1)
	}
	for _, t := range browserDrawn {
		// A type's section runs from its "### name" heading to the next
		// heading of either level, or to the end.
		head := "\n\n### " + t + "\n"
		at := strings.Index(d, head)
		if at < 0 || strings.Count(d, head) != 1 {
			return "", fmt.Errorf("figures-go's description has no single section %q", strings.TrimSpace(head))
		}
		rest := d[at+len(head):]
		end := len(rest)
		if next := strings.Index(rest, "\n\n#"); next >= 0 {
			end = next
		}
		d = d[:at] + rest[end:]
	}
	return d, nil
}

// Description is the draw_figure tool's description: when to draw, the
// rules, and every offered type's fields and worked example.
func Description() string { return offered }

// Catalogue is the part of the description from the type list on: every
// offered type's fields and example, for instructions that offer figures
// inside something else (an infographic's sections) rather than as a tool.
func Catalogue() string {
	const from = "Which type to use:"
	at := strings.Index(offered, from)
	if at < 0 {
		// Held by the tests, like the edits.
		panic("figurekit: the description has no type list")
	}
	return offered[at:]
}

// Guidance is the line for a chat's system prompt that offers the tool:
// figures-go's own rule of when to draw (always when the person asks for a
// figure, otherwise when it makes the answer clearer), naming only the
// figures offered here.
const Guidance = "You can draw a figure with the draw_figure tool: a chart, a clinical chart, a function plot, a diagram, or your own SVG when none of those fits. " +
	"When the person asks for a drawing, diagram, chart, graph, table, flowchart, illustration, picture or image, draw it. " +
	"Otherwise draw when a figure makes your answer clearer: numbers or a trend worth seeing, a process or algorithm with steps and branches, a pathway or mechanism, anatomy, a comparison of several things on several points. " +
	"Do not draw for decoration or to repeat a short list. Draw at most one or two per answer. Your written answer must stand on its own; the figure supports it."

// Parameters is draw_figure's JSON Schema: figures-go's, with the type's
// enum narrowed to the offered types.
var Parameters = func() json.RawMessage {
	fn := figures.Tool()["function"].(map[string]any)
	params := fn["parameters"].(map[string]any)
	props := map[string]any{}
	for k, v := range params["properties"].(map[string]any) {
		props[k] = v
	}
	typ := map[string]any{}
	for k, v := range props["type"].(map[string]any) {
		typ[k] = v
	}
	enum := make([]any, 0)
	for _, t := range Types() {
		enum = append(enum, t)
	}
	typ["enum"] = enum
	props["type"] = typ
	out := map[string]any{}
	for k, v := range params {
		out[k] = v
	}
	out["properties"] = props
	b, err := json.Marshal(out)
	if err != nil {
		panic("figurekit: " + err.Error())
	}
	return b
}()

// Refusal is a figure that was not drawn and why, in words for the model.
type Refusal = figures.Refusal

// Figure is a drawn figure (figures-go's): its type, title, alt text,
// size, light and dark SVGs, and the checked spec it was drawn from.
type Figure = figures.Figure

// Render checks draw_figure's arguments and draws the figure in light and
// dark. A *Refusal is the arguments' fault and goes back to the model; any
// other error is a fault here.
func Render(raw []byte) (*Figure, error) {
	if err := offeredType(raw); err != nil {
		return nil, err
	}
	return figures.Render(raw)
}

// offeredType refuses a type figures-go draws and Soloclef does not offer,
// before figures-go would accept it. Any other problem with the arguments
// is left to figures-go, which words it.
func offeredType(raw []byte) error {
	var a struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &a) != nil {
		return nil
	}
	if slices.Contains(browserDrawn, a.Type) {
		return &Refusal{Type: a.Type, Problems: []string{fmt.Sprintf("type %q is not drawn here; use a diagram type (flowchart, pathway, timeline, cycle, concept_map, comparison_table, venn, schematic) or your own svg; the types are %s",
			a.Type, strings.Join(Types(), ", "))}}
	}
	return nil
}

// Palette is a host's colours for a figure drawn in its look
// (figures-go's Palette: background and text required, each "#rrggbb").
type Palette = figures.Palette

// DrawSVG checks draw_figure's arguments and draws the figure as one SVG
// in palette, without its title when the host names it already.
func DrawSVG(raw []byte, palette *Palette, untitled bool) (string, error) {
	if err := offeredType(raw); err != nil {
		return "", err
	}
	return figures.DrawSVG(raw, figures.Look{Palette: palette, Untitled: untitled})
}

// ForModel is the tool's answer for a figure that was drawn.
func ForModel(f *Figure) string { return f.ForModel() }

// RefusalForModel is the tool's answer for arguments that were refused.
func RefusalForModel(err error) string { return figures.RefusalForModel(err) }

// MaxArgsBytes is the largest draw_figure arguments figures-go reads.
const MaxArgsBytes = figures.MaxArgsBytes
