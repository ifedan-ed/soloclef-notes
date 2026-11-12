package deliverables

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// The placement rules: a built document's rendered pages held to where its
// text was meant to be, by measurement. They replace a vision model's look
// at pictures of the pages. A model's look cost a paid call per twenty
// pages, failed whenever the provider did (a deck went out "not looked
// at"), and could say something different of the same deck twice; these
// cost milliseconds and say the same thing every time, with the shape and
// the words to fix.
//
// What they read: the PDF LibreOffice drew (the same PDF that becomes the
// preview), as poppler's pdftotext -bbox-layout lists every word with its
// box, and for a deck the deck's own shapes (layout.go). A word is matched
// back to the shape it belongs to by its letters, as PedsHub Tools does for
// its themes (tools-go internal/pptxcheck Overflows).
//
// Every page, of any document:
//   - every word lies on the page;
//   - no two words are drawn over each other.
// Every slide, in addition:
//   - each box's text is drawn whole inside its box (a growing box may grow
//     down; a box that does not wrap may run wide);
//   - no two boxes' text is drawn over each other;
//   - no line, connector or unfilled freeform crosses a line of text;
//   - no filled shape or picture drawn later hides a box's text;
//   - every picture lies on the slide, or covers all of it;
//   - nothing is set under 12 pt, and a body of three or more paragraphs
//     not under 16 pt;
//   - text reads against what it sits on (WCAG: 4.5:1 under 24 pt, 3:1
//     from 24 pt), wherever both colours are known.

// The rules' numbers.
const (
	// layoutSlack is how far, in points, a word may sit past its box and
	// still be in it: LibreOffice's and PowerPoint's metrics differ by
	// about this much. It is the slack Tools holds its themes to.
	layoutSlack = 3.0
	// pageSlack is how far a word may reach past the page's edge: under a
	// point is rounding, not clipping.
	pageSlack = 1.0
	// minTypePoints is the least size of any text on a slide, and
	// minBodyPoints the least of a body of text (three or more
	// paragraphs): a slide is read across a room. Tools' numbers.
	minTypePoints  = 12.0
	minBodyPoints  = 16.0
	bodyParagraphs = 3
	// largeTextPoints and the two ratios are WCAG 2.1 AA's contrast, with
	// "large" at 24 pt as Tools holds it: a projector washes colour out.
	largeTextPoints = 24.0
	contrastBody    = 4.5
	contrastLarge   = 3.0
	// bigMarkPoints: a word taller than this is a big numeral or mark,
	// drawn behind text by design; only text is held to not overlapping.
	bigMarkPoints = 40.0
	// maxLayoutFindings bounds what one verification lists: past this the
	// model has enough to fix, and the rest repeat.
	maxLayoutFindings = 40
	// maxWordsBytes bounds the word list read back: forty dense pages are
	// about two megabytes of it.
	maxWordsBytes = 32 << 20
)

// pdfWord is one word as drawn, in points from its page's top left.
type pdfWord struct {
	box  rect
	text string
	// line numbers the line poppler put it in, across the whole document.
	line int
}

// pdfPage is one page's size and words.
type pdfPage struct {
	width, height float64
	words         []pdfWord
}

// readPDFWords reads pdftotext -bbox-layout's output. It is XHTML; it is
// read leniently, as it is written (Tools reads it the same way).
func readPDFWords(data []byte) ([]pdfPage, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = false
	decoder.AutoClose = xml.HTMLAutoClose
	decoder.Entity = xml.HTMLEntity
	var pages []pdfPage
	line := 0
	number := func(element xml.StartElement, name string) float64 {
		for _, a := range element.Attr {
			if a.Name.Local == name {
				v, _ := strconv.ParseFloat(a.Value, 64)
				return v
			}
		}
		return 0
	}
	// RawToken: the list has no namespaces to resolve, and skipping that
	// halves the time it takes to read.
	var word *pdfWord
	for {
		token, err := decoder.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "page":
				pages = append(pages, pdfPage{width: number(t, "width"), height: number(t, "height")})
			case "line":
				line++
			case "word":
				if len(pages) == 0 {
					return nil, errors.New("a word before any page")
				}
				x0, y0, x1, y1 := number(t, "xMin"), number(t, "yMin"), number(t, "xMax"), number(t, "yMax")
				word = &pdfWord{box: rect{x0, y0, x1 - x0, y1 - y0}, line: line}
			}
		case xml.CharData:
			if word != nil {
				word.text += string(t)
			}
		case xml.EndElement:
			if t.Name.Local == "word" && word != nil {
				page := &pages[len(pages)-1]
				page.words = append(page.words, *word)
				word = nil
			}
		}
	}
	return pages, nil
}

// ligatures are the joined letters a PDF may carry for two or three; the
// deck's text has them apart.
var ligatures = strings.NewReplacer("ﬀ", "ff", "ﬁ", "fi", "ﬂ", "fl", "ﬃ", "ffi", "ﬄ", "ffl", "ﬅ", "st", "ﬆ", "st")

// pieces are a text's letters and digits in runs: what a word is for
// finding it again in a drawing, where hyphens, dashes and slashes may end
// a line (Tools' pieces).
func pieces(s string) []string {
	return strings.FieldsFunc(strings.ToLower(ligatures.Replace(s)), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

// where names a page in a finding.
func where(slides bool, number int) string {
	if slides {
		return fmt.Sprintf("slide %d", number)
	}
	return fmt.Sprintf("page %d", number)
}

// overlapping reports two word boxes drawn over each other: overlapping by
// more than a quarter of the smaller (Tools' measure; touching letters of
// tight kerning are not an overlap).
func overlapping(a, b rect) bool {
	ix := math.Min(a.right(), b.right()) - math.Max(a.x, b.x)
	iy := math.Min(a.bottom(), b.bottom()) - math.Max(a.y, b.y)
	if ix <= 0 || iy <= 0 {
		return false
	}
	small := math.Min(a.w*a.h, b.w*b.h)
	return small > 0 && ix*iy > 0.25*small
}

// pageFindings holds every page to its edges and its words to not
// overlapping. apart, when not nil, says a pair of words is judged
// elsewhere (two shapes' text on a slide, judged as boxes).
func pageFindings(pages []pdfPage, slides bool, apart func(page, i, j int) bool) []string {
	var findings []string
	for p, page := range pages {
		var off []string
		for _, w := range page.words {
			if w.box.x < -pageSlack || w.box.y < -pageSlack || w.box.right() > page.width+pageSlack || w.box.bottom() > page.height+pageSlack {
				off = append(off, w.text)
			}
		}
		if len(off) > 0 {
			findings = append(findings, fmt.Sprintf("%s: text runs off the %s and is cut: %s", where(slides, p+1), map[bool]string{true: "slide", false: "page"}[slides], quoteWords(off)))
		}
		// Words sorted by their left edge: a pair can overlap only while
		// the second starts before the first ends, so most pairs are
		// never compared.
		order := make([]int, len(page.words))
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool { return page.words[order[a]].box.x < page.words[order[b]].box.x })
		// One finding per page, the pairs listed: one collision of two
		// paragraphs is many pairs of words, and one thing to fix.
		reported := map[string]bool{}
		var pairs []string
		for a := 0; a < len(order); a++ {
			first := page.words[order[a]]
			if first.box.h > bigMarkPoints {
				continue
			}
			for b := a + 1; b < len(order); b++ {
				second := page.words[order[b]]
				if second.box.x >= first.box.right() {
					break
				}
				if second.box.h > bigMarkPoints || !overlapping(first.box, second.box) {
					continue
				}
				if apart != nil && apart(p, order[a], order[b]) {
					continue
				}
				pair := first.text + " / " + second.text
				if !reported[pair] {
					reported[pair] = true
					pairs = append(pairs, pair)
				}
			}
		}
		if len(pairs) > 0 {
			findings = append(findings, fmt.Sprintf("%s: words are drawn over each other: %s", where(slides, p+1), quoteWords(pairs)))
		}
	}
	return findings
}

// quoteWords lists up to six words.
func quoteWords(words []string) string {
	var out []string
	for i, w := range words {
		if i == 6 {
			out = append(out, fmt.Sprintf("and %d more", len(words)-6))
			break
		}
		out = append(out, strconv.Quote(w))
	}
	return strings.Join(out, ", ")
}

// placed is a frame's words as drawn: which of the page's words are its,
// and the lines they make.
type placed struct {
	words   []int
	lines   []rect
	missing []string
}

// placeWords finds each frame's words on its slide's page. A word is a
// frame's when it lies in the frame's room and one of its pieces is still
// wanted by the frame's text; a word is given to one frame only, smaller
// frames choosing first, so text in a badge on a card is the badge's.
func placeWords(frames []textFrame, page pdfPage, slideHeight, slideWidth float64) ([]placed, []int) {
	out := make([]placed, len(frames))
	owner := make([]int, len(page.words))
	for i := range owner {
		owner[i] = -1
	}
	order := make([]int, len(frames))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return frames[order[a]].box.w*frames[order[a]].box.h < frames[order[b]].box.w*frames[order[b]].box.h
	})
	for _, f := range order {
		frame := frames[f]
		if frame.rotated {
			continue
		}
		room := rect{frame.box.x - layoutSlack, frame.box.y - layoutSlack, frame.box.w + 2*layoutSlack, frame.box.h + 2*layoutSlack}
		if !frame.wraps {
			// Unwrapped text runs as wide as it is, centred or not.
			room.x, room.w = -layoutSlack, slideWidth+2*layoutSlack
		}
		if frame.grows {
			// The box grows down to fit its text, as LibreOffice draws it.
			room.h = slideHeight - room.y + layoutSlack
		}
		wanted := map[string]int{}
		for _, piece := range pieces(frame.text) {
			wanted[piece]++
		}
		var spaced strings.Builder
		for i, w := range page.words {
			if owner[i] >= 0 || !room.covers(w.box.x+w.box.w/2, w.box.y+w.box.h/2, 0) {
				continue
			}
			ps := pieces(w.text)
			mine := false
			for _, p := range ps {
				if wanted[p] > 0 {
					wanted[p]--
					mine = true
				}
			}
			if mine {
				owner[i] = f
				out[f].words = append(out[f].words, i)
			} else if len(ps) == 1 && len([]rune(ps[0])) <= 3 {
				// Letter-spaced capitals come out of the PDF a letter at a
				// time: those letters, joined in order, are looked in. They
				// are not claimed: a short word nearby may be another
				// shape's.
				spaced.WriteString(ps[0])
			}
		}
		letters := spaced.String()
		for _, p := range pieces(frame.text) {
			if wanted[p] == 0 {
				continue
			}
			if len([]rune(p)) > 1 && strings.Contains(letters, p) {
				letters = strings.Replace(letters, p, "", 1)
				wanted[p]--
				continue
			}
			out[f].missing = append(out[f].missing, p)
			wanted[p]--
		}
		// The frame's lines: its words grouped by the line poppler drew
		// them in.
		lines := map[int]rect{}
		var ids []int
		for _, i := range out[f].words {
			w := page.words[i]
			if box, seen := lines[w.line]; seen {
				lines[w.line] = union(box, w.box)
			} else {
				lines[w.line] = w.box
				ids = append(ids, w.line)
			}
		}
		sort.Ints(ids)
		for _, id := range ids {
			out[f].lines = append(out[f].lines, lines[id])
		}
	}
	return out, owner
}

func union(a, b rect) rect {
	x0, y0 := math.Min(a.x, b.x), math.Min(a.y, b.y)
	x1, y1 := math.Max(a.right(), b.right()), math.Max(a.bottom(), b.bottom())
	return rect{x0, y0, x1 - x0, y1 - y0}
}

// crosses reports whether a segment passes through a box (Liang-Barsky).
func crosses(segment [2]point, box rect) bool {
	p0, p1 := segment[0], segment[1]
	dx, dy := p1.x-p0.x, p1.y-p0.y
	t0, t1 := 0.0, 1.0
	for _, edge := range [4][2]float64{{-dx, p0.x - box.x}, {dx, box.right() - p0.x}, {-dy, p0.y - box.y}, {dy, box.bottom() - p0.y}} {
		p, q := edge[0], edge[1]
		if p == 0 {
			if q < 0 {
				return false
			}
			continue
		}
		r := q / p
		if p < 0 {
			t0 = math.Max(t0, r)
		} else {
			t1 = math.Min(t1, r)
		}
		if t0 > t1 {
			return false
		}
	}
	return true
}

// textCore is the part of a line of text a stroke may not pass through:
// the line less its outer fifth above and below (an underline rule under
// a heading touches its descenders, and is not crossing it) and a point at
// each end (an arrow may point at a label).
func textCore(line rect) rect {
	trimY := line.h * 0.2
	return rect{line.x + 1, line.y + trimY, line.w - 2, line.h - 2*trimY}
}

// deckFindings holds each slide's shapes to the words drawn for it.
func deckFindings(deck deckShapes, pages []pdfPage) []string {
	var findings []string
	for i, slide := range deck.slides {
		if i >= len(pages) {
			break
		}
		page := pages[i]
		// The PDF's page is the slide at LibreOffice's scale, which is one
		// to one; were it not, the words are brought to the slide's points.
		scale := 1.0
		if deck.width > 0 && page.width > 0 {
			scale = page.width / deck.width
		}
		if math.Abs(scale-1) > 1e-3 {
			for j := range page.words {
				b := page.words[j].box
				page.words[j].box = rect{b.x / scale, b.y / scale, b.w / scale, b.h / scale}
			}
		}
		findings = append(findings, slideFindings(slide, page, deck.width, deck.height)...)
	}
	return findings
}

func slideFindings(slide slideShapes, page pdfPage, width, height float64) []string {
	var findings []string
	at := where(true, slide.number)
	add := func(format string, args ...any) { findings = append(findings, at+": "+fmt.Sprintf(format, args...)) }
	frames := slide.frames
	drawn, owner := placeWords(frames, page, height, width)

	for f, frame := range frames {
		if frame.wraps && !frame.rotated && len(drawn[f].missing) > 0 {
			what := "its box"
			if frame.table {
				what = "its table's box (a row grew taller than the table was given)"
			}
			add("the text of %s is not drawn whole inside %s: %d of its %d words are outside it or broken, among them %s. Enlarge the box, shorten the text, or split the slide",
				frame, what, len(drawn[f].missing), len(pieces(frame.text)), quoteWords(drawn[f].missing))
		}
	}

	// Two shapes' text drawn over each other.
	for a := 0; a < len(frames); a++ {
		for b := a + 1; b < len(frames); b++ {
			if overlapLines(drawn[a].lines, drawn[b].lines) {
				add("the text of %s and the text of %s are drawn over each other; move one of the boxes", frames[a], frames[b])
			}
		}
	}

	// A line through text.
	for _, s := range slide.strokes {
		for f, frame := range frames {
			if s.name == frame.name {
				continue
			}
			if strokeCrosses(s, drawn[f].lines) {
				add("the line %q crosses the text of %s; route the line around the text or move the text", s.name, frame)
			}
		}
	}

	// Text hidden under a filled shape or a picture drawn after it.
	for f, frame := range frames {
		for _, c := range slide.covers {
			if c.z <= frame.z || c.name == frame.name {
				continue
			}
			for _, i := range drawn[f].words {
				w := page.words[i].box
				if c.box.covers(w.x+w.w/2, w.y+w.h/2, layoutSlack) {
					what := "the shape"
					if c.picture {
						what = "the picture"
					}
					add("the text of %s is hidden under %s %q drawn over it; move one, or draw the text after it", frame, what, c.name)
					break
				}
			}
		}
	}

	// Pictures on the slide.
	for _, p := range slide.pictures {
		background := p.box.x <= 0 && p.box.y <= 0 && p.box.right() >= width && p.box.bottom() >= height
		off := p.box.x < -layoutSlack || p.box.y < -layoutSlack || p.box.right() > width+layoutSlack || p.box.bottom() > height+layoutSlack
		if off && !background {
			add("the picture %q runs off the slide (%.0f, %.0f to %.0f, %.0f pt on a %.0f by %.0f pt slide); keep it on the slide, or make it cover the whole slide as a background", p.name, p.box.x, p.box.y, p.box.right(), p.box.bottom(), width, height)
		}
	}

	// Type size and contrast, a finding per frame: its worst run.
	for _, frame := range frames {
		smallest, smallText := math.Inf(1), ""
		bodyChars, smallChars := 0, 0
		worst, worstNeed, worstText, worstRun := math.Inf(1), 0.0, "", textRun{}
		for _, run := range frame.runs {
			text := strings.TrimSpace(run.text)
			if run.size < smallest {
				smallest, smallText = run.size, text
			}
			chars := len([]rune(text))
			bodyChars += chars
			if run.size < minBodyPoints {
				smallChars += chars
			}
			if run.colour == "" || run.ground == "" || decorative(text, run.size) {
				continue
			}
			need := contrastNeed(run.size)
			if ratio := contrast(run.colour, run.ground); ratio < need && ratio/need < worst/math.Max(worstNeed, 1e-9) {
				worst, worstNeed, worstText, worstRun = ratio, need, text, run
			}
		}
		if smallest < minTypePoints {
			add("%q in %s is set at %.3g pt; nothing on a slide may be under %g pt", clip(smallText), frame.name, smallest, minTypePoints)
		} else if !frame.table && frame.paragraphs >= bodyParagraphs && smallChars*2 > bodyChars {
			add("the body text of %s is set under %g pt; a body of %d or more paragraphs needs at least %g pt, so shorten it or split the slide", frame, minBodyPoints, bodyParagraphs, minBodyPoints)
		}
		if worstNeed > 0 {
			add("%q in %s is #%s on #%s at %.3g pt: contrast %.2f:1, and it needs %.1f:1; darken or lighten the text or its ground", clip(worstText), frame.name, worstRun.colour, worstRun.ground, worstRun.size, worst, worstNeed)
		}
	}

	// Words drawn over each other inside one shape, or outside any; two
	// shapes' text is judged above, as boxes.
	apart := func(_ int, i, j int) bool { return owner[i] >= 0 && owner[j] >= 0 && owner[i] != owner[j] }
	for _, finding := range pageFindings([]pdfPage{page}, true, apart) {
		// pageFindings numbers its one page 1; this is that slide.
		findings = append(findings, at+strings.TrimPrefix(finding, "slide 1"))
	}
	return findings
}

// overlapLines reports two frames' lines drawn over each other: crossing
// by more than a point across and a quarter of the lower line's height.
func overlapLines(a, b []rect) bool {
	for _, x := range a {
		for _, y := range b {
			ix := math.Min(x.right(), y.right()) - math.Max(x.x, y.x)
			iy := math.Min(x.bottom(), y.bottom()) - math.Max(x.y, y.y)
			if ix > 1 && iy > 0.25*math.Min(x.h, y.h) {
				return true
			}
		}
	}
	return false
}

func strokeCrosses(s stroke, lines []rect) bool {
	for _, line := range lines {
		core := textCore(line)
		if core.w <= 0 || core.h <= 0 {
			continue
		}
		for _, segment := range s.segments {
			if crosses(segment, core) {
				return true
			}
		}
	}
	return false
}

// clip is a run's text short enough to quote.
func clip(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > 60 {
		return string(runes[:57]) + "..."
	}
	return text
}

// layoutFindings is every placement finding of a rendered document, at
// most maxLayoutFindings of them. deck is nil for a document that is not
// a deck.
func layoutFindings(pages []pdfPage, deck *deckShapes) []string {
	var findings []string
	if deck != nil {
		findings = deckFindings(*deck, pages)
	} else {
		findings = pageFindings(pages, false, nil)
	}
	findings = distinct(findings)
	if len(findings) > maxLayoutFindings {
		more := len(findings) - maxLayoutFindings
		findings = append(findings[:maxLayoutFindings], fmt.Sprintf("and %d more placement findings of the same kinds", more))
	}
	return findings
}

// The colour arithmetic: WCAG 2.1's relative luminance and contrast ratio,
// and the blends a theme colour's modifiers and a see-through fill make.

func hexRGB(hex string) (r, g, b float64) {
	v, _ := strconv.ParseUint(hex, 16, 32)
	return float64(v >> 16 & 0xFF), float64(v >> 8 & 0xFF), float64(v & 0xFF)
}

func rgbHex(r, g, b float64) string {
	c := func(v float64) int { return int(math.Round(math.Max(0, math.Min(255, v)))) }
	return fmt.Sprintf("%02X%02X%02X", c(r), c(g), c(b))
}

// luminance is WCAG's relative luminance of a six-digit hex colour.
func luminance(hex string) float64 {
	r, g, b := hexRGB(hex)
	linear := func(c float64) float64 {
		c /= 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}

// contrast is WCAG's contrast ratio of two colours, 1 to 21.
func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// contrastNeed is the ratio text of this size is held to.
func contrastNeed(size float64) float64 {
	if size >= largeTextPoints {
		return contrastLarge
	}
	return contrastBody
}

// decorative is a single mark set huge as a watermark: ornament, which
// WCAG leaves out (Tools' Decorative).
func decorative(text string, size float64) bool {
	switch text {
	case "?", "!", "“", "”", "\"":
		return size >= 200
	}
	return false
}

// blend is over laid on base at opacity alpha.
func blend(base, over string, alpha float64) string {
	br, bg, bb := hexRGB(base)
	or, og, ob := hexRGB(over)
	mix := func(b, o float64) float64 { return b + (o-b)*alpha }
	return rgbHex(mix(br, or), mix(bg, og), mix(bb, ob))
}

// adjustLightness applies lumMod (multiply) or lumOff (add) to a colour's
// HSL lightness, as DrawingML defines them.
func adjustLightness(hex, modifier string, value float64) string {
	r, g, b := hexRGB(hex)
	r, g, b = r/255, g/255, b/255
	high, low := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l := (high + low) / 2
	h, s := 0.0, 0.0
	if high != low {
		d := high - low
		if l > 0.5 {
			s = d / (2 - high - low)
		} else {
			s = d / (high + low)
		}
		switch high {
		case r:
			h = math.Mod((g-b)/d+6, 6)
		case g:
			h = (b-r)/d + 2
		default:
			h = (r-g)/d + 4
		}
		h /= 6
	}
	if modifier == "lumMod" {
		l *= value
	} else {
		l += value
	}
	l = math.Max(0, math.Min(1, l))
	if s == 0 {
		return rgbHex(l*255, l*255, l*255)
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	channel := func(t float64) float64 {
		t = math.Mod(t+1, 1)
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	return rgbHex(channel(h+1.0/3)*255, channel(h)*255, channel(h-1.0/3)*255)
}
