package deckspec

import (
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The limits, in characters. The model is told them in words (Instructions
// divides by charsPerWord): a model cannot count characters, and a limit it
// can keep is one that is not refused. They are generous on purpose. What
// decides whether text fits is the renderer, which measures it in the
// theme's font and says by how much it is over (deckrender's fit
// problems); these only keep out what no layout could hold.
const (
	maxDeckTitle    = 90
	maxDeckSubtitle = 140
	maxSlideTitle   = 90
	maxSubtitle     = 160
	maxBullet       = 170
	maxColumnHead   = 50
	maxColumnBullet = 120
	maxCompareHead  = 32
	maxAspect       = 36
	maxCell         = 100
	maxQuote        = 260
	maxAttribution  = 90
	maxEventLabel   = 18
	maxEventText    = 110
	// maxStatValue is in characters, and told in characters: a figure is
	// a number with its unit, which a model counts as written. 24 holds a
	// change or a rate as clinical sources give it ("11.2 → 4.6 per
	// 1000"); the layout gives a long value a wider card (deckrender's
	// figureLayouts) rather than refusing it.
	maxStatValue    = 24
	maxStatLabel    = 80
	maxStepLabel    = 32
	maxStepText     = 100
	maxBarLabel     = 28
	maxUnit         = 14
	maxCaption      = 140
	maxNotes        = 2000
	maxSourceTitle  = 180
	maxSourceDetail = 220
	maxPassage      = 700
	maxNarration    = 1600

	maxSources = 15

	// charsPerWord is how characters are told to a model as words: seven a
	// word, figures-go's measure (core.Words), which keeps a model that
	// writes long words inside the limit.
	charsPerWord = 7
)

// The counts of a slide's parts, each a range a layout draws well.
var counts = map[string][2]int{
	"bullets":        {2, 6},
	"column bullets": {1, 5},
	"columns":        {2, 3},
	"rows":           {2, 5},
	"events":         {3, 6},
	"stats":          {2, 4},
	"steps":          {3, 6},
	"bars":           {2, 8},
}

// Options is what the caller asks of a deck beyond the schema.
type Options struct {
	// MinSlides and MaxSlides bound the slides the model writes (the
	// sources slide is drawn, not written, and not counted).
	MinSlides, MaxSlides int
	// Narrated is a video's deck: every slide has narration.
	Narrated bool
}

// Problems is everything wrong with a deck, worded for the model that
// wrote it, which fixes them all and sends the deck again.
type Problems []string

func (p Problems) Error() string { return strings.Join(p, "; ") }

func (p *Problems) add(format string, args ...any) { *p = append(*p, fmt.Sprintf(format, args...)) }

// Check reads a deck, puts its text in one canonical form (spaces folded, a
// bullet glyph a model put before a point taken off) and returns it, or
// every problem found. All at once: the reader is a model, and one problem
// a round trip would spend a call on each.
func Check(deck Deck, options Options) (Deck, error) {
	var p Problems
	deck.Title = text(&p, "the deck's title", deck.Title, maxDeckTitle, true)
	deck.Subtitle = text(&p, "the deck's subtitle", deck.Subtitle, maxDeckSubtitle, false)
	if !knownTheme(deck.Theme) {
		p.add("theme %q is not a theme; it must be one of %s", deck.Theme, strings.Join(themeIDs(), ", "))
	}
	if options.Narrated && len(deck.Sources) > 0 {
		// A video has no sources slide to number them on: where a fact
		// comes from is said in the narration.
		p.add("a video's deck lists no sources; when where a fact comes from matters, say it in the narration")
	}
	if len(deck.Sources) > maxSources {
		p.add("a deck cites at most %d sources; this one lists %d", maxSources, len(deck.Sources))
	}
	for i := range deck.Sources {
		which := fmt.Sprintf("source %d", i+1)
		deck.Sources[i].Title = text(&p, which+"'s title", deck.Sources[i].Title, maxSourceTitle, true)
		deck.Sources[i].Detail = text(&p, which+"'s detail", deck.Sources[i].Detail, maxSourceDetail, false)
	}
	if n := len(deck.Slides); n < options.MinSlides || n > options.MaxSlides {
		p.add("the deck must have %d to %d slides; it has %d", options.MinSlides, options.MaxSlides, n)
	}
	for i := range deck.Slides {
		checkSlide(&p, &deck.Slides[i], i+1, len(deck.Sources), options)
	}
	if len(p) > 0 {
		return Deck{}, p
	}
	return deck, nil
}

// checkSlide checks one slide by its kind: the fields it must have, the
// ones it must not, the counts and the lengths.
func checkSlide(p *Problems, s *Slide, number, sources int, options Options) {
	at := fmt.Sprintf("slide %d (%s)", number, s.Kind)
	field := func(name string) string { return at + "'s " + name }
	// unused names the fields a kind does not have but the model filled:
	// text that would silently not be drawn.
	unused := func(allowed ...string) {
		has := map[string]bool{
			"subtitle": s.Subtitle != "", "bullets": len(s.Bullets) > 0, "left": s.Left != nil, "right": s.Right != nil,
			"columns": len(s.Columns) > 0, "rows": len(s.Rows) > 0, "quote": s.Quote != "", "attribution": s.Attribution != "",
			"events": len(s.Events) > 0, "stats": len(s.Stats) > 0, "style": s.Style != "", "steps": len(s.Steps) > 0,
			"bars": len(s.Bars) > 0, "unit": s.Unit != "", "caption": s.Caption != "",
		}
		for _, name := range allowed {
			delete(has, name)
		}
		for _, name := range []string{"subtitle", "bullets", "left", "right", "columns", "rows", "quote", "attribution", "events", "stats", "style", "steps", "bars", "unit", "caption"} {
			if has[name] {
				p.add("%s has %s, which a %s slide does not have; put it in a slide of the kind that has it", at, name, s.Kind)
			}
		}
	}
	count := func(name string, n int) bool {
		r := counts[name]
		if n < r[0] || n > r[1] {
			p.add("%s needs %d to %d %s; it has %d", at, r[0], r[1], name, n)
			return false
		}
		return true
	}
	// Every kind but a quote has a title (a quote's is optional).
	titled := s.Kind != KindQuote
	switch s.Kind {
	case KindTitle:
		unused("subtitle")
		s.Subtitle = text(p, field("subtitle"), s.Subtitle, maxSubtitle, false)
	case KindSection:
		unused("subtitle")
		s.Subtitle = text(p, field("subtitle"), s.Subtitle, maxSubtitle, false)
	case KindBullets:
		unused("bullets")
		if count("bullets", len(s.Bullets)) {
			for i := range s.Bullets {
				s.Bullets[i] = point(p, fmt.Sprintf("%s bullet %d", at, i+1), s.Bullets[i], maxBullet)
			}
		}
	case KindTwoColumn:
		unused("left", "right")
		for _, side := range []struct {
			name string
			col  *Column
		}{{"left", s.Left}, {"right", s.Right}} {
			if side.col == nil {
				p.add("%s needs a %s column", at, side.name)
				continue
			}
			side.col.Heading = text(p, field(side.name+" column's heading"), side.col.Heading, maxColumnHead, true)
			if count("column bullets", len(side.col.Bullets)) {
				for i := range side.col.Bullets {
					side.col.Bullets[i] = point(p, fmt.Sprintf("%s %s column bullet %d", at, side.name, i+1), side.col.Bullets[i], maxColumnBullet)
				}
			}
		}
	case KindComparison:
		unused("columns", "rows")
		if count("columns", len(s.Columns)) {
			for i := range s.Columns {
				s.Columns[i] = text(p, fmt.Sprintf("%s column %d", at, i+1), s.Columns[i], maxCompareHead, true)
			}
		}
		if count("rows", len(s.Rows)) {
			for i := range s.Rows {
				row := &s.Rows[i]
				which := fmt.Sprintf("%s row %d", at, i+1)
				row.Aspect = text(p, which+"'s aspect", row.Aspect, maxAspect, true)
				if len(row.Cells) != len(s.Columns) {
					p.add("%s has %d cells and the comparison %d columns; give one cell per column", which, len(row.Cells), len(s.Columns))
				}
				for j := range row.Cells {
					row.Cells[j] = text(p, fmt.Sprintf("%s cell %d", which, j+1), row.Cells[j], maxCell, true)
				}
			}
		}
	case KindQuote:
		unused("quote", "attribution")
		s.Title = text(p, field("title"), s.Title, maxSlideTitle, false)
		s.Quote = strings.Trim(text(p, field("quote"), s.Quote, maxQuote, true), `"“”`)
		s.Attribution = strings.TrimLeft(text(p, field("attribution"), s.Attribution, maxAttribution, false), "—–- ")
	case KindTimeline:
		unused("events")
		if count("events", len(s.Events)) {
			for i := range s.Events {
				which := fmt.Sprintf("%s event %d", at, i+1)
				s.Events[i].Label = text(p, which+"'s label", s.Events[i].Label, maxEventLabel, true)
				s.Events[i].Text = text(p, which+"'s text", s.Events[i].Text, maxEventText, true)
			}
		}
	case KindStats:
		unused("stats")
		if count("stats", len(s.Stats)) {
			for i := range s.Stats {
				which := fmt.Sprintf("%s stat %d", at, i+1)
				s.Stats[i].Value = characters(p, which+"'s value", s.Stats[i].Value, maxStatValue, true)
				s.Stats[i].Label = text(p, which+"'s label", s.Stats[i].Label, maxStatLabel, true)
			}
		}
	case KindDiagram:
		unused("style", "steps")
		if s.Style != DiagramFlow && s.Style != DiagramCycle {
			p.add("%s style must be %q or %q; it is %q", at, DiagramFlow, DiagramCycle, s.Style)
		}
		if count("steps", len(s.Steps)) {
			for i := range s.Steps {
				which := fmt.Sprintf("%s step %d", at, i+1)
				s.Steps[i].Label = text(p, which+"'s label", s.Steps[i].Label, maxStepLabel, true)
				s.Steps[i].Text = text(p, which+"'s text", s.Steps[i].Text, maxStepText, false)
			}
		}
	case KindChart:
		unused("bars", "unit", "caption")
		s.Unit = text(p, field("unit"), s.Unit, maxUnit, false)
		s.Caption = text(p, field("caption"), s.Caption, maxCaption, false)
		if count("bars", len(s.Bars)) {
			largest := 0.0
			for i := range s.Bars {
				which := fmt.Sprintf("%s bar %d", at, i+1)
				s.Bars[i].Label = text(p, which+"'s label", s.Bars[i].Label, maxBarLabel, true)
				v := s.Bars[i].Value
				if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
					p.add("%s's value must be a number of zero or more; it is %v", which, v)
				}
				largest = math.Max(largest, v)
			}
			if largest <= 0 {
				p.add("%s has no bar above zero; a chart of nothing says nothing", at)
			}
		}
	default:
		p.add("%s: kind %q is not a slide kind; it must be one of %s", fmt.Sprintf("slide %d", number), s.Kind, kindList())
		return
	}
	if titled {
		s.Title = text(p, field("title"), s.Title, maxSlideTitle, true)
	}
	s.Notes = notes(p, field("notes"), s.Notes)
	seen := map[int]bool{}
	for _, cite := range s.Cites {
		switch {
		case cite < 1 || cite > sources:
			p.add("%s cites source %d, and the deck lists %d sources (numbered from 1)", at, cite, sources)
		case seen[cite]:
			p.add("%s cites source %d twice", at, cite)
		}
		seen[cite] = true
	}
	checkNarration(p, s, at, options.Narrated)
}

// checkNarration holds a video slide's passages to the slide's parts:
// exactly one per part (Steps). Each passage is voiced on its own and its
// part comes in as that passage starts, so a passage short or over means a
// part with nothing said as it comes in, or words with no part to show;
// the word-count guess at where a passage started inside a slide's single
// recording, which this replaced, drifted by seconds over numbers and
// abbreviations. A deck file's slides have no narration at all.
func checkNarration(p *Problems, s *Slide, at string, narrated bool) {
	if !narrated {
		if len(s.Narration) > 0 {
			p.add("%s has narration, which only a video's slides have; put what the presenter says in notes", at)
		}
		return
	}
	if steps := Steps(*s); len(s.Narration) != steps {
		want := "1 narration passage, spoken as it appears"
		if parts := partsNamed(*s); parts != "" {
			want = fmt.Sprintf("%d narration passages, one as it appears and then %s", steps, parts)
		}
		p.add("%s has %d narration passages and %d parts; give exactly %s", at, len(s.Narration), steps, want)
	}
	total := 0
	for i := range s.Narration {
		s.Narration[i] = text(p, fmt.Sprintf("%s narration passage %d", at, i+1), s.Narration[i], maxPassage, true)
		total += utf8.RuneCountInString(s.Narration[i])
	}
	if total > maxNarration {
		p.add("%s's narration is %d words; at most %d words are spoken over one slide, so split it into two slides", at, total/charsPerWord, maxNarration/charsPerWord)
	}
}

// text checks one line of plain text and returns it with its spaces
// folded. A required field must not be empty. Its length is told in words,
// as the instructions give its limit.
func text(p *Problems, what, value string, limit int, required bool) string {
	return line(p, what, value, limit, required, func(n int) string {
		return fmt.Sprintf("%s is %d words; keep it to %d", what, words(n), words(limit))
	})
}

// characters is text for a field whose limit the instructions give in
// characters (a figure's value), and which is refused in characters too:
// "is 3 words; keep it to 2" said of "11.2 → 4.6 per 1000", five words
// to a reader, gave the model nothing it could check.
func characters(p *Problems, what, value string, limit int, required bool) string {
	return line(p, what, value, limit, required, func(n int) string {
		return fmt.Sprintf("%s is %d characters; keep it to %d", what, n, limit)
	})
}

// line is text and characters: tooLong words the problem of a value of n
// characters, over the limit.
func line(p *Problems, what, value string, limit int, required bool, tooLong func(n int) string) string {
	value = strings.Join(strings.Fields(value), " ")
	switch n := utf8.RuneCountInString(value); {
	case value == "" && required:
		p.add("%s is empty", what)
	case n > limit:
		p.add("%s", tooLong(n))
	}
	if bad := badCharacter(value); bad != "" {
		p.add("%s contains %s", what, bad)
	}
	if strings.Contains(value, "**") {
		p.add("%s has ** in it; bold is only for the lead phrase of a bullet", what)
	}
	return value
}

// point checks a bullet: one line, optionally led by one **bold** phrase.
// A bullet glyph or number the model wrote before it is taken off, since
// the layout draws its own marker and a second would read "• • Point".
func point(p *Problems, what, value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	for _, glyph := range []string{"- ", "• ", "* ", "– ", "· "} {
		value = strings.TrimPrefix(value, glyph)
	}
	plain := strings.ReplaceAll(value, "**", "")
	switch n := utf8.RuneCountInString(plain); {
	case plain == "":
		p.add("%s is empty", what)
	case n > limit:
		p.add("%s is %d words; keep it to %d", what, words(n), words(limit))
	}
	if bad := badCharacter(value); bad != "" {
		p.add("%s contains %s", what, bad)
	}
	switch strings.Count(value, "**") {
	case 0:
	case 2:
		if !strings.HasPrefix(value, "**") {
			p.add("%s: only the lead phrase at its start may be **bold**", what)
		}
	default:
		p.add("%s: bold is one lead phrase in a pair of **, like **Light reactions:** make ATP", what)
	}
	return value
}

// notes checks speaker notes, which may run to several lines.
func notes(p *Problems, what, value string) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(value), "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.Join(strings.Fields(lines[i]), " ")
	}
	value = strings.TrimSpace(strings.Join(lines, "\n"))
	if n := utf8.RuneCountInString(value); n > maxNotes {
		p.add("%s are %d words; keep them to %d", what, words(n), words(maxNotes))
	}
	if bad := badCharacter(strings.ReplaceAll(value, "\n", " ")); bad != "" {
		p.add("%s contain %s", what, bad)
	}
	return value
}

// words is a character count as the words a model is told.
func words(chars int) int { return (chars + charsPerWord - 1) / charsPerWord }

// badCharacter names a character that cannot go into a slide: a control
// character, which neither a deck's XML nor a drawn frame can hold, and the
// replacement character, which means the text was already broken.
func badCharacter(value string) string {
	for _, r := range value {
		switch {
		case r == utf8.RuneError:
			return "a broken character (U+FFFD)"
		case unicode.IsControl(r):
			return fmt.Sprintf("a control character (U+%04X)", r)
		}
	}
	return ""
}

func kindList() string {
	names := make([]string, len(Kinds))
	for i, k := range Kinds {
		names[i] = string(k)
	}
	return strings.Join(names, ", ")
}
