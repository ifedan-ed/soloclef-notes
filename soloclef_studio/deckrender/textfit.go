package deckrender

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Text is sized to its box here, once, in the face it is drawn in: the
// largest size from the layout's range at which every word fits the width
// and every line the height. The deck and the video are both drawn from
// the lines this decides, so the deck's text box holds exactly what was
// measured and the video's frame shows the same lines.
//
// Tools estimated lines from a character count (pptxrender estimateLines);
// measuring real advances is what lets a box be filled without a margin
// for error, and what lets a too-long text be refused with a number.

// span is a stretch of a paragraph in one style.
type span struct {
	text         string
	bold, italic bool
}

// para is one paragraph of a text block.
type para struct {
	spans []span
	// gap is the space before the paragraph, in lines of its text.
	gap float64
}

// plain is a paragraph of one unstyled span.
func plain(text string) para { return para{spans: []span{{text: text}}} }

// leadBold is a bullet that may begin with a **bold** lead phrase
// (deckspec allows exactly one, at the start).
func leadBold(text string) para {
	if rest, ok := strings.CutPrefix(text, "**"); ok {
		if lead, tail, ok := strings.Cut(rest, "**"); ok {
			return para{spans: []span{{text: lead, bold: true}, {text: tail}}}
		}
	}
	return plain(text)
}

// piece is a run of one face within a line, at its offset from the line's
// start.
type piece struct {
	text string
	face Face
	x    float64
}

// line is one drawn line.
type line struct {
	pieces []piece
	width  float64
	// para is which paragraph the line belongs to; first is its first line.
	para  int
	first bool
	// joined is a line that goes on with the word the line before it
	// ended inside (a word too wide for any line, broken where Unicode
	// lets a line break inside it). The deck writes an explicit line break
	// there, since PowerPoint would not break the word at that place on
	// its own.
	joined bool
}

// fitted is a block of text at the size it was fitted to.
type fitted struct {
	size  float64
	lines []line
	// lineHeight is the distance between baselines and ascent the first
	// baseline's distance below the block's top, in points.
	lineHeight, ascent float64
	height             float64
}

// broken reports whether any line goes on with a word broken inside it.
func (f fitted) broken() bool {
	for _, l := range f.lines {
		if l.joined {
			return true
		}
	}
	return false
}

// word is a stretch between spaces, which may change style inside it.
type word struct {
	pieces []piece
	width  float64
}

// text is the word as it reads.
func (w word) text() string {
	var b strings.Builder
	for _, pc := range w.pieces {
		b.WriteString(pc.text)
	}
	return b.String()
}

// words splits a paragraph at its spaces, keeping each piece's face.
func words(p para, fam family, size float64) []word {
	var out []word
	current := word{}
	flush := func() {
		if len(current.pieces) > 0 {
			out = append(out, current)
		}
		current = word{}
	}
	for _, s := range p.spans {
		face := fam.style(s.bold, s.italic)
		start := 0
		for i, r := range s.text {
			if r != ' ' {
				continue
			}
			if i > start {
				current.add(s.text[start:i], face, size)
			}
			flush()
			start = i + utf8.RuneLen(r)
		}
		if start < len(s.text) {
			current.add(s.text[start:], face, size)
		}
	}
	flush()
	return out
}

func (w *word) add(text string, face Face, size float64) {
	wd := width(face, text, size)
	w.pieces = append(w.pieces, piece{text: text, face: face, x: w.width})
	w.width += wd
}

// breaksAfter reports whether a line may break after r inside a word,
// between prev and next: the opportunities Unicode's line breaking
// algorithm (UAX #14) gives within a word, and only those, so a break
// falls where a reader expects one (the deck then writes the break out,
// textBox, rather than trust each program's own rules):
//   - after a hyphen-minus (class HY) between two letters; LB25 forbids
//     the break before a digit ("pages 10-12", "-5"), and between letters
//     is the case a compound word is;
//   - after a hyphen U+2010 or an en dash U+2013 (class BA), not before a
//     digit, for the same reason as LB25;
//   - after a solidus "/" (class SY) not before a digit (LB25 again), as
//     an address or "and/or" is broken;
//   - after an em dash U+2014 (class B2).
//
// Nothing is hyphenated, that is broken at a syllable with a hyphen
// added: that needs the word's language and a dictionary of its patterns
// (Liang's, as TeX has per language), and a deck is in whatever language
// its author writes. A wrong syllable break reads worse than a word sent
// back to be shortened, so a word with no opportunity is sent back.
func breaksAfter(prev, r, next rune) bool {
	digit := next >= '0' && next <= '9'
	switch r {
	case '-':
		return unicode.IsLetter(prev) && unicode.IsLetter(next)
	case '\u2010', '\u2013', '/':
		return prev != 0 && next != 0 && !digit
	case '\u2014':
		return prev != 0 && next != 0
	}
	return false
}

// split breaks a word at its opportunities (breaksAfter) into the parts a
// line may end between, each measured at size.
func (w word) split(size float64) []word {
	type styled struct {
		r    rune
		face Face
	}
	var runes []styled
	for _, pc := range w.pieces {
		for _, r := range pc.text {
			runes = append(runes, styled{r, pc.face})
		}
	}
	var out []word
	current := word{}
	var run strings.Builder
	face := Face{}
	flushRun := func() {
		if run.Len() > 0 {
			current.add(run.String(), face, size)
			run.Reset()
		}
	}
	for i, s := range runes {
		if run.Len() > 0 && s.face != face {
			flushRun()
		}
		face = s.face
		run.WriteRune(s.r)
		prev, next := rune(0), rune(0)
		if i > 0 {
			prev = runes[i-1].r
		}
		if i+1 < len(runes) {
			next = runes[i+1].r
		}
		if breaksAfter(prev, s.r, next) {
			flushRun()
			out = append(out, current)
			current = word{}
		}
	}
	flushRun()
	if len(current.pieces) > 0 {
		out = append(out, current)
	}
	return out
}

// tooWide is a word, or the part of one between two of its break
// opportunities, wider than its line at a size: need is its width, room
// the line's, both in points.
type tooWide struct {
	word             string
	need, room, size float64
}

// wrap breaks paragraphs into lines no wider than limit. With breakWords,
// a word wider than the line is broken at the places Unicode lets a line
// break inside a word (breaksAfter); a word (or, broken, a part of one)
// still wider than the line is reported, since no size of this range can
// be allowed to draw it: it would run out of its box.
//
// Words of one face that follow each other are joined into one piece with
// their space, so a line is one run per style: the deck's runs and the
// frame's text spans are then the same few, and a renderer's own spacing
// between words is the space measured here.
func wrap(paras []para, fam family, size, limit float64, breakWords bool) ([]line, *tooWide) {
	var out []line
	for pi, p := range paras {
		space := width(fam.regular, " ", size)
		cur := line{para: pi, first: true}
		// place puts w on the line, after a space unless it goes on with
		// the part of a word before it.
		place := func(w word, spaced bool) {
			at := cur.width
			if len(cur.pieces) > 0 && spaced {
				at += space
			}
			if len(cur.pieces) > 0 && at+w.width > limit {
				out = append(out, cur)
				cur = line{para: pi, joined: !spaced}
				at = 0
			}
			for i, pc := range w.pieces {
				n := len(cur.pieces)
				if n > 0 && cur.pieces[n-1].face == pc.face {
					joint := ""
					if i == 0 && spaced && at > cur.width {
						joint = " "
					}
					cur.pieces[n-1].text += joint + pc.text
					continue
				}
				cur.pieces = append(cur.pieces, piece{text: pc.text, face: pc.face, x: at + pc.x})
			}
			cur.width = at + w.width
		}
		for _, w := range words(p, fam, size) {
			if w.width <= limit {
				place(w, true)
				continue
			}
			if !breakWords {
				return nil, &tooWide{word: w.text(), need: w.width, room: limit, size: size}
			}
			parts := w.split(size)
			for _, part := range parts {
				if part.width > limit {
					return nil, &tooWide{word: part.text(), need: part.width, room: limit, size: size}
				}
			}
			for i, part := range parts {
				place(part, i == 0)
			}
		}
		out = append(out, cur)
	}
	return out, nil
}

// topSize is the largest size, on the half-point grid sizes are tried
// on, at which the widest word of paras (or, with breakWords, the widest
// part of one) fits a line of limit. Every width is in proportion to the
// size, so one measure at 1 pt says it: the sizes above it need not be
// wrapped to be refused, which is most of the work when a long word is
// what decides (a figure tried from 84 pt down in three arrangements).
func topSize(paras []para, fam family, limit float64, breakWords bool) float64 {
	widest := 0.0
	for _, p := range paras {
		for _, w := range words(p, fam, 1) {
			if !breakWords {
				widest = math.Max(widest, w.width)
				continue
			}
			// Broken, a word is held when its widest part is: whole when
			// it fits, in parts when it does not.
			for _, part := range w.split(1) {
				widest = math.Max(widest, part.width)
			}
		}
	}
	if widest == 0 {
		return math.Inf(1)
	}
	return math.Floor(limit/widest*2) / 2
}

// shortfall is why a text does not fit its box even at its least size:
// a word too wide for its line, or else the share of the text that fits
// (the rest is what to cut).
type shortfall struct {
	wide *tooWide
	keep float64
}

// fit finds the largest size from most down to least (in half points) at
// which paras fit a box of w by h. spacing is the line spacing as a
// multiple of single. It reports false when even the least size does not
// fit, with why: the word too wide for the line, or the share of the text
// that would fit.
//
// A word is broken inside only when it fits whole at no size down to the
// least: a smaller size reads better than a broken word, and the least
// size is still one a room can read.
func fit(paras []para, fam family, most, least, spacing, w, h float64) (fitted, shortfall, bool) {
	f, short, ok := fitSizes(paras, fam, most, least, spacing, w, h, false)
	if ok || short.wide == nil {
		return f, short, ok
	}
	return fitSizes(paras, fam, most, least, spacing, w, h, true)
}

// fitSizes is fit with words broken or not.
func fitSizes(paras []para, fam family, most, least, spacing, w, h float64, breakWords bool) (fitted, shortfall, bool) {
	m := metricsOf(fam.regular)
	if most > least {
		most = math.Min(most, topSize(paras, fam, w, breakWords))
	}
	for size := most; size >= least-1e-9; size -= 0.5 {
		lines, wide := wrap(paras, fam, size, w, breakWords)
		if wide != nil {
			continue
		}
		lineHeight := m.line * size * spacing
		height := float64(len(lines)) * lineHeight
		for _, l := range lines {
			if l.first && l.para > 0 {
				height += paras[l.para].gap * lineHeight
			}
		}
		// The first line's ascent is what single spacing gives it; a
		// spacing under one draws the lines closer, not the first one
		// higher.
		if height <= h+0.01 {
			return fitted{size: size, lines: lines, lineHeight: lineHeight, ascent: m.ascent * size, height: height}, shortfall{}, true
		}
	}
	// Why it does not fit at the least size.
	lines, wide := wrap(paras, fam, least, w, breakWords)
	if wide != nil {
		return fitted{}, shortfall{wide: wide}, false
	}
	lineHeight := m.line * least * spacing
	return fitted{}, shortfall{keep: h / (float64(len(lines)) * lineHeight)}, false
}
