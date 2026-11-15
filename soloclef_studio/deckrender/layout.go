package deckrender

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckspec"
)

// The grid every content slide shares, in points.
const (
	marginX     = 64.0
	contentW    = SlideW - 2*marginX
	headingY    = 34.0
	headingH    = 84.0
	contentTop  = 146.0
	contentBot  = 486.0
	contentH    = contentBot - contentTop
	footerY     = 502.0
	footerH     = 20.0
	footerSize  = 12.0
	headingMost = 36.0
	headingLess = 24.0
	// The least size of any body text: what a slide read across a room
	// needs, and the layout rules' floor for a body of text (16 pt).
	bodyLeast = 16.0
)

// FitProblem is text that does not fit its place even at the least size
// its layout allows, in the widest arrangement the slide has, worded for
// the model that wrote it: which slide, which part of it (named as the
// deck's fields are, "stat 3's value"), and either the word that is too
// wide, with the room there is, or roughly how much to cut. The model
// fixes what it is told precisely; "a word in the figures' values is too
// long" left it to guess which word, and the guess was usually wrong.
// Slide 0 is a problem of the whole deck.
type FitProblem struct {
	Slide int
	// Kind is the slide's kind, as Check's problems say it ("stats"); the
	// sources slide, which the model does not write, is "sources".
	Kind string
	What string
	// Keep is the share of the text that fits, when there is too much of
	// it (0 when Word is set).
	Keep float64
	// Word is the word too wide for its line (or the part of it between
	// two places a line may break inside it); Need is its width and Room
	// the line's, both in points, at Size, the least size the place
	// allows.
	Word             string
	Need, Room, Size float64
}

func (p FitProblem) String() string {
	if p.Slide == 0 {
		// A problem of the whole deck, said as it is.
		return p.What
	}
	at := fmt.Sprintf("slide %d (%s) %s", p.Slide, p.Kind, p.What)
	if p.Word != "" {
		// The room in characters as well as points: a model cannot measure
		// points, and the number of letters that fit is what it can act on.
		chars := utf8.RuneCountInString(p.Word)
		fits := int(math.Floor(float64(chars) * p.Room / p.Need))
		return fmt.Sprintf("%s: the word %q is too wide for its line even at %g pt, the least size there, in the widest layout the slide has: "+
			"it needs %.0f pt and the line is %.0f pt, room for about %d of its %d characters; use a shorter word or an abbreviation",
			at, p.Word, p.Size, p.Need, p.Room, fits, chars)
	}
	cut := int(math.Ceil((1-p.Keep)*10)) * 10
	return fmt.Sprintf("%s: there is too much text for its place; cut it by about %d%%", at, max(cut, 10))
}

// builder lays out one deck.
type builder struct {
	deck     deckspec.Deck
	pal      palette
	problems []FitProblem
	slide    int
	// kind is the slide's kind, as its problems name it.
	kind  string
	total int
}

// Layout lays out every slide of a checked deck, plus the closing sources
// slide when the deck cites any. Text that cannot fit is returned as
// problems, all at once, for the model to shorten; nothing is cut or
// shrunk past what a room can read.
func Layout(deck deckspec.Deck) ([]Scene, []FitProblem) {
	pal, ok := palettes[deck.Theme]
	if !ok {
		// deckspec.Check refuses an unknown theme, so only an unchecked
		// deck gets here; it is answered like any other problem rather
		// than drawn in a look nobody chose.
		return nil, []FitProblem{{What: "theme " + strconv.Quote(deck.Theme) + " is not a theme"}}
	}
	b := &builder{deck: deck, pal: pal, total: len(deck.Slides)}
	if len(deck.Sources) > 0 {
		b.total++
	}
	scenes := make([]Scene, 0, b.total)
	for i, s := range deck.Slides {
		b.slide, b.kind = i+1, string(s.Kind)
		scene := Scene{Name: fmt.Sprintf("Slide %d", i+1), Background: pal.paper, Notes: s.Notes, Transition: pal.transition, Steps: deckspec.Steps(s)}
		switch s.Kind {
		case deckspec.KindTitle:
			b.titleSlide(&scene, s)
		case deckspec.KindSection:
			b.sectionSlide(&scene, s)
		case deckspec.KindBullets:
			b.bulletsSlide(&scene, s)
		case deckspec.KindTwoColumn:
			b.twoColumnSlide(&scene, s)
		case deckspec.KindComparison:
			b.comparisonSlide(&scene, s)
		case deckspec.KindQuote:
			b.quoteSlide(&scene, s)
		case deckspec.KindTimeline:
			b.timelineSlide(&scene, s)
		case deckspec.KindStats:
			b.statsSlide(&scene, s)
		case deckspec.KindDiagram:
			if s.Style == deckspec.DiagramCycle {
				b.cycleSlide(&scene, s)
			} else {
				b.flowSlide(&scene, s)
			}
		case deckspec.KindChart:
			b.chartSlide(&scene, s)
		}
		if s.Kind != deckspec.KindTitle {
			b.footer(&scene, s.Cites)
		}
		scenes = append(scenes, scene)
	}
	if len(deck.Sources) > 0 {
		b.slide, b.kind = len(deck.Slides)+1, "sources"
		scene := Scene{Name: fmt.Sprintf("Slide %d", b.slide), Background: pal.paper, Transition: pal.transition, Steps: 1}
		b.sourcesSlide(&scene)
		b.footer(&scene, nil)
		scenes = append(scenes, scene)
	}
	return scenes, b.problems
}

// text fits paras into a box and returns the block, or records what does
// not fit and returns a block at the least size (never drawn: a deck with
// problems is refused).
func (b *builder) text(name, what string, x, y, w, h float64, paras []para, fam family, most, least, spacing float64, colour, align, anchor string, reveal Reveal) *Text {
	f, short, ok := fit(paras, fam, most, least, spacing, w, h)
	if !ok {
		b.problem(what, short)
	}
	return &Text{Name: name, X: x, Y: y, W: w, H: h, Paras: paras, Family: fam, Fit: f, Color: colour,
		Align: align, Anchor: anchor, Spacing: spacing, Reveal: reveal}
}

// problem records one text that does not fit, on the slide being laid out.
func (b *builder) problem(what string, short shortfall) {
	p := FitProblem{Slide: b.slide, Kind: b.kind, What: what, Keep: short.keep}
	if short.wide != nil {
		p.Keep = 0
		p.Word, p.Need, p.Room, p.Size = short.wide.word, short.wide.need, short.wide.room, short.wide.size
	}
	b.problems = append(b.problems, p)
}

// report records the texts of a group that do not fit, each named by
// name(i); a miss of the group as a whole (cell -1, a list too tall for
// its box) is named group.
func (b *builder) report(misses []miss, group string, name func(int) string) {
	for _, m := range misses {
		what := group
		if m.cell >= 0 {
			what = name(m.cell)
		}
		b.problem(what, m.short)
	}
}

// ink is text colour on ground at size: the theme's ink, made legible.
func (b *builder) ink(ground string, size float64) string {
	return legible(b.pal.ink, ground, b.opposite(ground), size)
}

// muted is the theme's quiet text colour on ground, made legible.
func (b *builder) muted(ground string, size float64) string {
	return legible(b.pal.muted, ground, b.opposite(ground), size)
}

// accentText is the accent as text on ground, deepened (or lightened) only
// as far as it must be to read.
func (b *builder) accentText(ground string, size float64) string {
	return legible(b.pal.accent, ground, b.opposite(ground), size)
}

// darkest is the theme's darkest colour: its ink, or a dark theme's paper.
func (b *builder) darkest() string {
	if b.pal.dark {
		return b.pal.paper
	}
	return b.pal.ink
}

// opposite is what text is moved towards on ground: black on a light
// ground, white on a dark one.
func (b *builder) opposite(ground string) string {
	if luminance(ground) > 0.4 {
		return "000000"
	}
	return "FFFFFF"
}

// card is the ground of a card: its fill, or the paper when the theme
// outlines its cards.
func (b *builder) card(name string, x, y, w, h float64, fill string, reveal Reveal) (*Box, string) {
	box := &Box{Name: name, X: x, Y: y, W: w, H: h, Preset: "roundRect", Radius: b.pal.radius, Fill: fill, Reveal: reveal}
	if b.pal.radius == 0 {
		box.Preset = "rect"
	}
	if b.pal.outline {
		box.Fill, box.Line, box.LineW = b.pal.paper, b.pal.ink, 2
		return box, b.pal.paper
	}
	return box, fill
}

// heading sets a content slide's title and its theme's mark, on step 0.
func (b *builder) heading(scene *Scene, title string) {
	ground := b.pal.paper
	switch b.pal.heading {
	case "band":
		ground = b.pal.accent
		scene.add(&Box{Name: "Heading band", X: 0, Y: 0, W: SlideW, H: headingY + headingH + 14, Preset: "rect", Fill: ground, Reveal: Reveal{Motion: Fade}})
	case "bar":
		scene.add(&Box{Name: "Heading bar", X: marginX - 22, Y: headingY + 20, W: 7, H: headingH - 24, Preset: "rect", Fill: b.pal.accent, Reveal: Reveal{Motion: Pop}})
	}
	colour := b.ink(ground, headingLess)
	if b.pal.heading == "band" {
		colour = onFill(ground, b.darkest(), headingLess)
	}
	scene.add(b.text("Title", "the title", marginX, headingY, contentW, headingH, []para{plain(title)}, b.pal.head.boldIfSans(),
		headingMost, headingLess, 1, colour, "l", "b", Reveal{Motion: Rise}))
	if b.pal.heading == "rule" {
		scene.add(&Box{Name: "Heading rule", X: marginX, Y: headingY + headingH + 10, W: 72, H: 4, Preset: "rect", Fill: b.pal.accent, Reveal: Reveal{Motion: GrowX, Delay: 0.15}})
	}
}

// boldIfSans is the heading face: a sans heading is bold, a serif one
// regular (a serif's own contrast carries it, as Tools' Georgia headings).
func (f family) boldIfSans() family {
	if f == sans {
		return family{regular: f.bold, bold: f.bold, italic: f.italic}
	}
	return f
}

// footer is the slide's number and the sources it cites, quiet, at the
// bottom. They come in with the slide.
func (b *builder) footer(scene *Scene, cites []int) {
	colour := b.muted(scene.Background, footerSize)
	if scene.Background == b.pal.accent {
		colour = onFill(scene.Background, b.darkest(), footerSize)
	}
	number := strconv.Itoa(b.slide)
	scene.add(b.text("Slide number", "the slide number", SlideW-marginX-60, footerY, 60, footerH, []para{plain(number)}, b.pal.body,
		footerSize, footerSize, 1, colour, "r", "ctr", Reveal{Motion: Fade}))
	if len(cites) > 0 {
		list := make([]string, len(cites))
		for i, c := range cites {
			list[i] = strconv.Itoa(c)
		}
		word := "Source "
		if len(cites) > 1 {
			word = "Sources "
		}
		scene.add(b.text("Sources cited", "the sources line", marginX, footerY, 400, footerH, []para{plain(word + strings.Join(list, ", "))}, b.pal.body,
			footerSize, footerSize, 1, colour, "l", "ctr", Reveal{Motion: Fade}))
	}
}

// fitTogether fits several blocks at one size, the largest at which every
// one fits its width and all of them, stacked with gap between, fit
// height. Points of a list read as one list when they share a size. As fit
// does, it breaks a word only when no size holds it whole. When they do
// not fit at the least size, the misses are each block with a word too
// wide for its line, or else the list as a whole (cell -1) with the share
// of it that fits.
func fitTogether(items [][]para, fam family, most, least, spacing, w, height, gap float64) ([]fitted, []miss) {
	for _, breakWords := range []bool{false, true} {
		top := most
		for _, paras := range items {
			top = math.Min(top, topSize(paras, fam, w, breakWords))
		}
		for size := top; size >= least-1e-9; size -= 0.5 {
			out := make([]fitted, len(items))
			total := gap * float64(len(items)-1)
			ok := true
			for i, paras := range items {
				f, _, fits := fitSizes(paras, fam, size, size, spacing, w, math.Inf(1), breakWords)
				if !fits {
					ok = false
					break
				}
				out[i] = f
				total += f.height
			}
			if ok && total <= height+0.01 {
				return out, nil
			}
		}
		// Why, at the least size.
		var misses []miss
		total := gap * float64(len(items)-1)
		for i, paras := range items {
			f, short, fits := fitSizes(paras, fam, least, least, spacing, w, math.Inf(1), breakWords)
			if !fits {
				misses = append(misses, miss{cell: i, short: short})
				continue
			}
			total += f.height
		}
		if len(misses) == 0 {
			return nil, []miss{{cell: -1, short: shortfall{keep: height / total}}}
		}
		if breakWords {
			return nil, misses
		}
	}
	panic("unreachable")
}
