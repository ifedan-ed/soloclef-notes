// Package posterrender draws an infographic as one SVG poster from content
// a model writes: a title, an optional line under it, a few key numbers,
// two to six sections of short points (each section may hold a figure), a
// takeaway and the sources.
//
// The model never draws. It writes the poster's content (Poster), which is
// checked here field by field and refused in words it can correct from
// (Refusal), the way figures-go refuses a figure; trusted code then lays the
// poster out on the canvas asked for, in one of the looks (styles.go), and
// draws it as SVG. A figure inside a section is a draw_figure spec, checked
// and drawn by figures-go (soloclef_studio/figurekit) in the poster's own
// colours. resvg makes the PNG (raster.go).
//
// Every text is measured in the faces it is drawn in (the deck's Carlito
// and Caladea, soloclef_studio/deckrender), so what fits here fits in the
// PNG, and in the SVG, which carries those faces with it. Text that does
// not fit is made room for first (smaller type down to a readable floor,
// then another arrangement of the sections); only then is the poster
// refused, naming the exact item and word (layout.go).
//
// This package knows nothing of models, artifacts or storage: it is the
// poster's content, its layout and its drawing.
package posterrender

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_studio/figurekit"
)

// Poster is an infographic's content, as the model writes it.
type Poster struct {
	// Title is the poster's heading. It is set from the artifact's title,
	// not written twice by the model.
	Title    string      `json:"-"`
	Subtitle string      `json:"subtitle,omitempty"`
	Numbers  []KeyNumber `json:"key_numbers,omitempty"`
	Sections []Section   `json:"sections"`
	Takeaway string      `json:"takeaway,omitempty"`
	Sources  string      `json:"sources,omitempty"`
}

// KeyNumber is one figure the poster leads with: "1 in 11" and what it
// counts.
type KeyNumber struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Section is one panel: a heading, short points, and perhaps a figure.
type Section struct {
	Heading string   `json:"heading"`
	Points  []string `json:"points,omitempty"`
	// Figure is draw_figure's arguments (type, title, alt, caption, spec),
	// kept as written; Check replaces it with the checked spec.
	Figure json.RawMessage `json:"figure,omitempty"`
}

// The limits, in characters. The model is told them in words (Words):
// a model cannot count characters, and is refused with the words to keep
// to, never cut.
const (
	MaxTitle       = 120
	MaxSubtitle    = 160
	MaxNumbers     = 4
	MaxNumberValue = 16
	MaxNumberLabel = 70
	MinSections    = 2
	MaxSections    = 6
	MaxHeading     = 50
	MaxPoints      = 5
	MaxPoint       = 140
	MaxFigures     = 3
	MaxTakeaway    = 180
	MaxSources     = 200
	// MaxBytes is the most of a poster read: six sections of five points
	// and three figures fit in a fraction of it.
	MaxBytes = 96 << 10
)

// charsPerWord is how limits are told in words: seven characters a word,
// figures-go's rule, so a poster's limits read as its figures' do.
const charsPerWord = 7

// Words is a limit of chars characters in words: a multiple of five from
// ten on, so the model reads round numbers.
func Words(chars int) int {
	w := chars / charsPerWord
	if w >= 10 {
		w -= w % 5
	}
	return max(1, w)
}

// UpTo is a limit in words for an instruction: "at most 15 words".
func UpTo(chars int) string {
	if w := Words(chars); w != 1 {
		return fmt.Sprintf("at most %d words", w)
	}
	return "at most 1 word"
}

// Refusal is a poster that was not drawn, and every reason, each naming
// where it is ("sections[2].points[1]") in words the model can act on.
type Refusal struct{ Problems []string }

func (r *Refusal) Error() string {
	if len(r.Problems) == 1 {
		return "the infographic was not drawn: " + r.Problems[0]
	}
	var b strings.Builder
	b.WriteString("the infographic was not drawn. Problems:")
	for i, p := range r.Problems {
		fmt.Fprintf(&b, "\n%d. %s", i+1, p)
	}
	return b.String()
}

// maxProblems is how many problems one refusal lists: enough to fix first.
const maxProblems = 12

type problems []string

func (p *problems) add(path, format string, args ...any) {
	if len(*p) >= maxProblems {
		return
	}
	msg := fmt.Sprintf(format, args...)
	if path != "" {
		msg = path + ": " + msg
	}
	*p = append(*p, msg)
}

func (p problems) err() error {
	if len(p) == 0 {
		return nil
	}
	return &Refusal{Problems: p}
}

// Decode reads a poster as written, refusing a field it does not have: a
// model that adds "colour" or "layout" believes it chose something.
func Decode(raw []byte) (Poster, error) {
	if len(raw) > MaxBytes {
		return Poster{}, &Refusal{Problems: []string{fmt.Sprintf("the poster is %d bytes; at most %d are read. Write fewer, shorter points", len(raw), MaxBytes)}}
	}
	var p Poster
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return Poster{}, &Refusal{Problems: []string{"the poster is not in its shape: " + strings.TrimPrefix(err.Error(), "json: ") +
			`. It is {"subtitle", "key_numbers": [{"value", "label"}], "sections": [{"heading", "points": [...], "figure": {...}}], "takeaway", "sources"}`}}
	}
	return p, nil
}

// Check holds a poster to its limits and checks its figures, refusing with
// every problem at once. It returns the poster trimmed, with each figure
// replaced by its checked spec (what is kept and drawn again later).
func Check(p Poster) (Poster, error) {
	var bad problems
	p.Title = label(&bad, "title", p.Title, true, MaxTitle)
	p.Subtitle = label(&bad, "subtitle", p.Subtitle, false, MaxSubtitle)
	if len(p.Numbers) > MaxNumbers {
		bad.add("key_numbers", "has %d numbers; keep the %d that matter most", len(p.Numbers), MaxNumbers)
	}
	for i := range p.Numbers {
		at := fmt.Sprintf("key_numbers[%d]", i)
		p.Numbers[i].Value = label(&bad, at+".value", p.Numbers[i].Value, true, MaxNumberValue)
		p.Numbers[i].Label = label(&bad, at+".label", p.Numbers[i].Label, true, MaxNumberLabel)
		if v := p.Numbers[i].Value; v != "" && !strings.ContainsFunc(v, unicode.IsDigit) {
			bad.add(at+".value", "has no number in it (%q); a key number is a figure such as \"42%%\" or \"1 in 11\", put words in a section", v)
		}
	}
	switch n := len(p.Sections); {
	case n < MinSections:
		bad.add("sections", "has %d; a poster has %d to %d sections", n, MinSections, MaxSections)
	case n > MaxSections:
		bad.add("sections", "has %d; keep it to %d by merging the smallest", n, MaxSections)
	}
	figuresSeen := 0
	for i := range p.Sections {
		s := &p.Sections[i]
		at := fmt.Sprintf("sections[%d]", i)
		s.Heading = label(&bad, at+".heading", s.Heading, true, MaxHeading)
		if len(s.Points) > MaxPoints {
			bad.add(at+".points", "has %d points; keep it to %d", len(s.Points), MaxPoints)
		}
		for j := range s.Points {
			s.Points[j] = label(&bad, fmt.Sprintf("%s.points[%d]", at, j), s.Points[j], true, MaxPoint)
		}
		hasFigure := len(bytes.TrimSpace(s.Figure)) > 0 && string(bytes.TrimSpace(s.Figure)) != "null"
		if !hasFigure {
			s.Figure = nil
			if len(s.Points) == 0 {
				bad.add(at, "has neither points nor a figure; give it at least one point")
			}
			continue
		}
		figuresSeen++
		if figuresSeen > MaxFigures {
			bad.add(at+".figure", "is figure %d; a poster holds at most %d, so keep the ones that show most", figuresSeen, MaxFigures)
			continue
		}
		spec, err := checkFigure(s.Figure)
		if err != nil {
			var r *figurekit.Refusal
			if !errors.As(err, &r) {
				return Poster{}, err
			}
			for _, problem := range r.Problems {
				bad.add(at+".figure", "%s", problem)
			}
			continue
		}
		s.Figure = spec
	}
	p.Takeaway = label(&bad, "takeaway", p.Takeaway, false, MaxTakeaway)
	p.Sources = label(&bad, "sources", p.Sources, false, MaxSources)
	if err := bad.err(); err != nil {
		return Poster{}, err
	}
	return p, nil
}

// checkFigure checks a section's figure as draw_figure's arguments and
// returns the spec figures-go keeps (canonical, compact).
func checkFigure(raw json.RawMessage) (json.RawMessage, error) {
	f, err := figurekit.Render(raw)
	if err != nil {
		return nil, err
	}
	return f.Spec, nil
}

// label checks one text: required or not, within max characters, plain
// words on one line. It returns it trimmed. A text over its limit is
// refused with its length and the words to keep to, and its start quoted
// so the model finds it; it is never cut.
func label(bad *problems, path, s string, required bool, max int) string {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	switch {
	case s == "" && required:
		bad.add(path, "is required")
	case n > max:
		words := len(strings.Fields(s))
		keep := min(Words(max), max*words/n)
		if keep < 1 {
			bad.add(path, "is one word of %d characters, and %d fit; abbreviate it: %s", n, max, excerpt(s))
			break
		}
		bad.add(path, "is %d words; keep it to %d or fewer: %s", words, keep, excerpt(s))
	case strings.ContainsAny(s, "\n\r\t"):
		bad.add(path, "has a line break; write it on one line")
	case strings.Contains(s, "</") || strings.Contains(s, "**") || strings.Contains(s, "]("):
		bad.add(path, "has markup; write plain words, the poster sets the type: %s", excerpt(s))
	case strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) }):
		bad.add(path, "has a control character")
	}
	return s
}

// excerpt is the start of a text, quoted: its first six words.
func excerpt(s string) string {
	words := strings.Fields(s)
	more := ""
	if len(words) > 6 {
		words, more = words[:6], "…"
	}
	return `"` + strings.Join(words, " ") + more + `"`
}
