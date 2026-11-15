package posterrender

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender"
	"git.danvics.com/danvics/soloclef/soloclef_studio/figurekit"
)

// How a poster is laid out.
//
// The page is, top to bottom: the header (title and subtitle), the key
// numbers in one row, the sections in a grid of rows, the takeaway, and
// the sources. Everything but the title is set in one type scale, scaled
// together by s: the largest s at which everything fits is used, so a
// short poster is set large and a full one small, never below the floors
// below (a poster is zoomed into on a phone, but text under about fifteen
// pixels on a 1024-pixel side is not read). At each s the sections are
// tried in each arrangement the canvas allows (three columns, then four or
// two on a landscape page; one, then two on a portrait one), so a section
// with a figure can have a wider column before any text is made smaller.
// Rows share a height, and a row that leaves room spreads into it, so the
// page is filled evenly rather than left with a gap at the foot.
//
// Only when nothing fits at the floor is the poster refused, naming what
// does not fit: a word wider than its column, the title, or the section
// that needs the most height, with its longest point.

// The type scale at s = 1, and each size's floor, in pixels at a
// 1024-pixel short side (every canvas has one).
var (
	baseSubtitle, floorSubtitle = 28.0, 19.0
	baseHeading, floorHeading   = 30.0, 20.0
	basePoint, floorPoint       = 22.0, 15.0
	baseValue, floorValue       = 52.0, 28.0
	baseValueLab, floorValueLab = 19.0, 14.0
	baseTakeaway, floorTakeaway = 25.0, 17.0
	baseSources, floorSources   = 15.0, 12.0
	// The title is fitted on its own, from its largest to its smallest,
	// in at most maxTitleLines lines.
	maxTitleSize, minTitleSize = 66.0, 40.0
	maxTitleLines              = 3
)

// The range of s: above 1 a sparse poster is set larger; at sFloor every
// size has reached its floor (the points' is the last).
const (
	sTop   = 1.36
	sFloor = 15.0 / 22.0
	sStep  = 0.04
)

// Spacing, in pixels at s = 1.
const (
	margin     = 52.0
	gapBase    = 26.0
	padBase    = 24.0
	maxNumLine = 4
)

// A figure is drawn 400 units wide by figures-go and scaled into its
// section: never below figMinScale (its labels would be too small to read)
// nor above figMaxScale times s (it would outshout the points beside it,
// and on a full page it gives its height back first).
const (
	figUnits    = 400.0
	figMinScale = 0.85
	figMaxScale = 1.25
	// sideBySide: a figure beside the points rather than under them, when
	// the section is at least this wide and the points keep minPointsW.
	minPointsW = 300.0
)

// scale is the type scale and spacing at one s.
type scale struct {
	s                                                            float64
	subtitle, heading, point, value, valueLab, takeaway, sources font
	gap, pad                                                     float64
}

func scaleAt(s float64, l look) scale {
	at := func(base, floor float64) float64 { return math.Max(base*s, floor) }
	head := deckrender.FaceOf(l.SerifHeadings, true)
	body := deckrender.FaceOf(false, false)
	bold := deckrender.FaceOf(false, true)
	return scale{
		s:        s,
		subtitle: font{body, at(baseSubtitle, floorSubtitle)},
		heading:  font{head, at(baseHeading, floorHeading)},
		point:    font{body, at(basePoint, floorPoint)},
		value:    font{head, at(baseValue, floorValue)},
		valueLab: font{body, at(baseValueLab, floorValueLab)},
		takeaway: font{bold, at(baseTakeaway, floorTakeaway)},
		sources:  font{body, at(baseSources, floorSources)},
		gap:      math.Max(gapBase*math.Min(s, 1), 16),
		pad:      math.Max(padBase*math.Min(s, 1), 14),
	}
}

// figure is a section's figure drawn in the poster's colours, ready to
// nest.
type figure struct {
	svg string
	// h is its height in its own units (it is figUnits wide).
	h float64
}

var viewBox = regexp.MustCompile(`viewBox="0 0 ([0-9.]+) ([0-9.]+)"`)

// drawFigure draws a checked spec on a card of the given colour.
func drawFigure(spec []byte, l look, cardBg string) (figure, error) {
	svg, err := figurekit.DrawSVG(spec, &figurekit.Palette{
		Background: "#" + cardBg, Text: "#" + l.Ink, Muted: "#" + l.Muted,
		Accent: "#" + l.Accent, Series: []string{"#" + l.AccentAlt},
	}, false)
	if err != nil {
		return figure{}, err
	}
	m := viewBox.FindStringSubmatch(svg)
	if m == nil {
		return figure{}, fmt.Errorf("posterrender: a figure without its size: %.80s", svg)
	}
	w, _ := strconv.ParseFloat(m[1], 64)
	h, _ := strconv.ParseFloat(m[2], 64)
	if w <= 0 || h <= 0 {
		return figure{}, fmt.Errorf("posterrender: a figure %vx%v", w, h)
	}
	return figure{svg: svg, h: h * figUnits / w}, nil
}

// misfit is why one arrangement at one s did not fit: a word too wide
// for its place, or a height over what is left.
type misfit struct {
	// word and path: a word wider than its column, and where it is.
	word, path string
	// need and room: the height the sections need and the height left.
	need, room float64
	// tallest is the section that needs the most, or -1; figure is the
	// height its figure takes in it (0 for none).
	tallest int
	figure  float64
}

// laid is a poster laid out: what to draw, at which sizes and where.
type laid struct {
	sc       scale
	title    font
	titleL   []string
	subtitle []string
	cols     int
	rows     [][]int // section indices per row
	rowH     []float64
	headerH  float64
	numbersH float64
	footerH  float64
}

// layout fits the poster on c in l, or refuses saying what does not fit.
func layout(p Poster, c Canvas, l look, figs map[int]figure) (laid, error) {
	inner := c.Width - 2*margin
	var last misfit
	for s := sTop; s >= sFloor-1e-9; s -= sStep {
		sc := scaleAt(s, l)
		// The title shrinks with the rest once the page is full, so a
		// crowded poster gives its sections the room first; it is never
		// set smaller than minTitleSize.
		title, titleLines, err := fitTitle(p.Title, l, inner, c.ID, math.Max(minTitleSize, maxTitleSize*math.Min(1, s)))
		if err != nil {
			return laid{}, err
		}
		out := laid{sc: sc, title: title, titleL: titleLines}
		subtitle, word := wrap(p.Subtitle, sc.subtitle, inner-headerInset(l, sc))
		if word != "" {
			last = misfit{word: word, path: "subtitle", tallest: -1}
			continue
		}
		out.subtitle = subtitle
		out.headerH = headerHeight(out, l)
		if len(p.Numbers) > 0 {
			h, m := numbersHeight(p.Numbers, sc, inner, l)
			if m != nil {
				last = *m
				continue
			}
			out.numbersH = h
		}
		fh, m := footerHeight(p, sc, inner)
		if m != nil {
			last = *m
			continue
		}
		out.footerH = fh
		room := c.Height - 2*margin - out.headerH - out.footerH
		if out.numbersH > 0 {
			room -= out.numbersH + sc.gap
		}
		room -= sc.gap // between the header and what follows
		var best *misfit
		for _, cols := range arrangements(c, len(p.Sections)) {
			rows := rowsOf(len(p.Sections), cols)
			heights, need, tallest, figureH, m := sectionHeights(p, rows, sc, inner, l, figs)
			if m == nil && need <= room {
				out.cols, out.rows, out.rowH = cols, rows, heights
				spread(out.rowH, room-need)
				return out, nil
			}
			if m == nil {
				m = &misfit{need: need, room: room, tallest: tallest, figure: figureH}
			}
			if best == nil || better(m, best) {
				best = m
			}
		}
		last = *best
	}
	return laid{}, refusalFor(p, c, last)
}

// better says whether misfit a is nearer to fitting than b: an overflow is
// nearer than a word that fits nowhere, and a smaller overflow nearer
// than a larger.
func better(a, b *misfit) bool {
	switch {
	case a.word == "" && b.word != "":
		return true
	case a.word != "" || b.word != "":
		return false
	}
	return a.need-a.room < b.need-b.room
}

// spread gives a row its share of the room left, so the grid fills the
// page: in proportion to each row's own height, so a row of short
// sections does not swell beside a row of long ones.
func spread(heights []float64, extra float64) {
	if extra <= 0 {
		return
	}
	total := 0.0
	for _, h := range heights {
		total += h
	}
	for i := range heights {
		heights[i] += extra * heights[i] / total
	}
}

// fitTitle sets the title as large as it fits in maxTitleLines lines,
// starting from largest.
func fitTitle(text string, l look, w float64, canvas string, largest float64) (font, []string, error) {
	face := deckrender.FaceOf(l.SerifHeadings, true)
	w -= titleInset(l)
	for size := largest; size >= minTitleSize; size -= 2 {
		f := font{face, size}
		lines, word := wrap(text, f, w)
		if word == "" && len(lines) <= maxTitleLines {
			return f, lines, nil
		}
	}
	f := font{face, minTitleSize}
	if _, word := wrap(text, f, w); word != "" {
		return font{}, nil, &Refusal{Problems: []string{fmt.Sprintf("title: the word %q is wider than the %s poster even in its smallest heading; break or abbreviate it", word, canvas)}}
	}
	// The words that fit in maxTitleLines lines of this width, by the
	// title's own average word.
	words := len(strings.Fields(text))
	lines, _ := wrap(text, f, w)
	keep := max(1, words*maxTitleLines/len(lines))
	return font{}, nil, &Refusal{Problems: []string{fmt.Sprintf("title: is %d words and takes %d lines on the %s poster even in its smallest heading, and %d fit; keep it to about %d words: %s",
		words, len(lines), canvas, maxTitleLines, keep, excerpt(text))}}
}

// titleInset is what a banded header takes from the title's width.
func titleInset(l look) float64 {
	if l.header == headerBand {
		return 2 * padBase
	}
	return 0
}

func headerInset(l look, sc scale) float64 {
	if l.header == headerBand {
		return 2 * sc.pad
	}
	return 0
}

// headerHeight is the title, the subtitle and the header's own furniture.
func headerHeight(out laid, l look) float64 {
	h := out.title.blockHeight(len(out.titleL))
	if len(out.subtitle) > 0 {
		h += out.sc.gap*0.4 + out.sc.subtitle.blockHeight(len(out.subtitle))
	}
	switch l.header {
	case headerRule:
		h += out.sc.gap*0.6 + ruleHeight
	case headerBand:
		h += 2 * out.sc.pad
	}
	return h
}

// ruleHeight is the accent rule under a title.
const ruleHeight = 6.0

// numbersHeight is the key numbers' row: the tallest tile.
func numbersHeight(numbers []KeyNumber, sc scale, inner float64, l look) (float64, *misfit) {
	tileW := (inner - float64(len(numbers)-1)*sc.gap) / float64(len(numbers))
	textW := tileW - 2*sc.pad
	tallest := 0.0
	for i, n := range numbers {
		if sc.value.width(n.Value) > textW {
			return 0, &misfit{word: n.Value, path: fmt.Sprintf("key_numbers[%d].value", i), tallest: -1}
		}
		lines, word := wrap(n.Label, sc.valueLab, textW)
		if word != "" {
			return 0, &misfit{word: word, path: fmt.Sprintf("key_numbers[%d].label", i), tallest: -1}
		}
		if len(lines) > maxNumLine {
			return 0, &misfit{need: math.Inf(1), room: 0, tallest: -1, path: fmt.Sprintf("key_numbers[%d].label", i)}
		}
		h := sc.value.lineHeight() + sc.valueLab.blockHeight(len(lines))
		tallest = math.Max(tallest, h)
	}
	return tallest + 2*sc.pad, nil
}

// footerHeight is the takeaway and the sources.
func footerHeight(p Poster, sc scale, inner float64) (float64, *misfit) {
	h := 0.0
	if p.Takeaway != "" {
		lines, word := wrap(p.Takeaway, sc.takeaway, inner-2*sc.pad-takeawayBar)
		if word != "" {
			return 0, &misfit{word: word, path: "takeaway", tallest: -1}
		}
		h += sc.gap + 2*sc.pad*0.8 + sc.takeaway.blockHeight(len(lines))
	}
	if p.Sources != "" {
		lines, word := wrap(p.Sources, sc.sources, inner)
		if word != "" {
			return 0, &misfit{word: word, path: "sources", tallest: -1}
		}
		h += sc.gap*0.6 + sc.sources.blockHeight(len(lines))
	}
	return h, nil
}

// takeawayBar is the accent bar at the takeaway's left.
const takeawayBar = 8.0

// arrangements is the column counts to try for n sections on c, the one
// that reads best first. An arrangement that leaves one section alone in
// its last row is tried last: a lone wide panel under a row of narrow ones
// reads as an afterthought.
func arrangements(c Canvas, n int) []int {
	var prefer []int
	switch c.ID {
	case "landscape":
		prefer = []int{3, 4, 2}
	case "portrait":
		prefer = []int{1, 2}
	default:
		prefer = []int{2, 3, 1}
	}
	var good, lone []int
	for _, cols := range prefer {
		if cols > n {
			continue
		}
		if n > cols && n%cols == 1 {
			lone = append(lone, cols)
		} else {
			good = append(good, cols)
		}
	}
	out := append(good, lone...)
	if len(out) == 0 {
		out = []int{n}
	}
	return out
}

// rowsOf puts n sections in rows of cols, in order; the last row holds the
// rest, each of its sections wider.
func rowsOf(n, cols int) [][]int {
	var rows [][]int
	for i := 0; i < n; i += cols {
		row := []int{}
		for j := i; j < min(n, i+cols); j++ {
			row = append(row, j)
		}
		rows = append(rows, row)
	}
	return rows
}

// cardWidth is the width of each of k sections side by side.
func cardWidth(inner float64, k int, sc scale) float64 {
	return (inner - float64(k-1)*sc.gap) / float64(k)
}

// sectionHeights measures every row: the height of its tallest section.
// need is the rows and the gaps between them; tallest the section that
// sets the most height, and figureH the height its figure takes there.
func sectionHeights(p Poster, rows [][]int, sc scale, inner float64, l look, figs map[int]figure) (heights []float64, need float64, tallest int, figureH float64, m *misfit) {
	tallest = -1
	tallestH := 0.0
	for r, row := range rows {
		w := cardWidth(inner, len(row), sc)
		rowH := 0.0
		for _, i := range row {
			h, at, miss := measureSection(p.Sections[i], i, sc, w, l, figs)
			if miss != nil {
				return nil, 0, -1, 0, miss
			}
			rowH = math.Max(rowH, h)
			if h > tallestH {
				tallest, tallestH, figureH = i, h, 0
				if at.fig != nil {
					figureH = at.fig.h * at.figScale
				}
			}
		}
		heights = append(heights, rowH)
		need += rowH
		if r > 0 {
			need += sc.gap
		}
	}
	return heights, need, tallest, figureH, nil
}

// placed is a section's content laid out at one width.
type placed struct {
	heading    []string
	headingX   float64 // where the heading starts: after a marker
	points     [][]string
	pointsW    float64
	fig        *figure
	figScale   float64
	figBeside  bool
	contentTop float64 // below the heading
}

// The bullet before a point: a dot, and the indent the point's lines
// share.
func bulletIndent(f font) float64 { return f.size * 0.95 }

// measureSection lays one section out at width w and returns its height.
func measureSection(s Section, i int, sc scale, w float64, l look, figs map[int]figure) (float64, placed, *misfit) {
	var out placed
	innerW := w - 2*sc.pad
	at := fmt.Sprintf("sections[%d]", i)
	if l.marker != markerNone {
		out.headingX = sc.heading.lineHeight() + sc.pad*0.5
	}
	heading, word := wrap(s.Heading, sc.heading, innerW-out.headingX)
	if word != "" {
		return 0, out, &misfit{word: word, path: at + ".heading", tallest: -1}
	}
	out.heading = heading
	h := sc.pad + sc.heading.blockHeight(len(heading))
	if l.cards == cardRuled {
		h += ruleHeight
	}
	out.contentTop = h + sc.pad*0.5
	pointsAt := func(width float64) ([][]string, float64, *misfit) {
		var all [][]string
		ph := 0.0
		for j, point := range s.Points {
			lines, word := wrap(point, sc.point, width-bulletIndent(sc.point))
			if word != "" {
				return nil, 0, &misfit{word: word, path: fmt.Sprintf("%s.points[%d]", at, j), tallest: -1}
			}
			all = append(all, lines)
			ph += sc.point.blockHeight(len(lines))
			if j > 0 {
				ph += sc.point.size * 0.45
			}
		}
		return all, ph, nil
	}
	fig, hasFig := figs[i]
	if !hasFig {
		points, ph, miss := pointsAt(innerW)
		if miss != nil {
			return 0, out, miss
		}
		out.points, out.pointsW = points, innerW
		return out.contentTop + ph + sc.pad, out, nil
	}
	maxScale := math.Max(figMinScale, figMaxScale*sc.s)
	// Under the points: the figure as wide as the section allows.
	under := math.Min(innerW/figUnits, maxScale)
	if under < figMinScale {
		return 0, out, &misfit{word: "", path: at + ".figure", need: math.Inf(1), tallest: i}
	}
	points, ph, miss := pointsAt(innerW)
	if miss != nil {
		return 0, out, miss
	}
	gapFig := 0.0
	if len(points) > 0 {
		gapFig = sc.gap * 0.6
	}
	underH := ph + gapFig + fig.h*under
	best := placed{heading: out.heading, headingX: out.headingX, contentTop: out.contentTop,
		points: points, pointsW: innerW, fig: &fig, figScale: under}
	bestH := underH
	// Beside the points, when the section is wide enough for both: on a
	// landscape page that is often far shorter.
	if len(s.Points) > 0 {
		besideScale := math.Min(maxScale, (innerW-minPointsW-sc.gap)/figUnits)
		if besideScale >= figMinScale {
			pw := innerW - besideScale*figUnits - sc.gap
			bp, bph, miss := pointsAt(pw)
			if miss == nil {
				if bh := math.Max(bph, fig.h*besideScale); bh < bestH {
					best = placed{heading: out.heading, headingX: out.headingX, contentTop: out.contentTop,
						points: bp, pointsW: pw, fig: &fig, figScale: besideScale, figBeside: true}
					bestH = bh
				}
			}
		}
	}
	return best.contentTop + bestH + sc.pad, best, nil
}

// refusalFor words a misfit at the floor for the model.
func refusalFor(p Poster, c Canvas, m misfit) error {
	switch {
	case m.word != "":
		return &Refusal{Problems: []string{fmt.Sprintf("%s: the word %q is wider than its column on the %s poster even at the smallest readable size; break it, abbreviate it, or use fewer sections so each is wider",
			m.path, m.word, c.ID)}}
	case strings.HasSuffix(m.path, ".figure"):
		return &Refusal{Problems: []string{fmt.Sprintf("%s: no arrangement of %d sections on the %s poster leaves this section wide enough for its figure to be read; use fewer sections, or drop the figure",
			m.path, len(p.Sections), c.ID)}}
	case strings.HasPrefix(m.path, "key_numbers"):
		return &Refusal{Problems: []string{fmt.Sprintf("%s: is too long to fit under its number on the %s poster in %d lines; keep it to about %d words",
			m.path, c.ID, maxNumLine, Words(MaxNumberLabel)/2)}}
	case m.tallest < 0:
		return &Refusal{Problems: []string{fmt.Sprintf("the poster does not fit its %s canvas even at the smallest readable size; write fewer, shorter points", c.ID)}}
	}
	s := p.Sections[m.tallest]
	what := fmt.Sprintf("%d points", len(s.Points))
	if len(s.Figure) > 0 {
		what += " and a figure"
	}
	longest, longestWords := -1, 0
	for j, point := range s.Points {
		if n := len(strings.Fields(point)); n > longestWords {
			longest, longestWords = j, n
		}
	}
	advice := "shorten its points"
	if longest >= 0 {
		advice = fmt.Sprintf("shorten its points (the longest is sections[%d].points[%d], %d words: %s)", m.tallest, longest, longestWords, excerpt(s.Points[longest]))
	}
	if len(s.Figure) > 0 {
		advice += ", or drop its figure"
	}
	if m.figure > 0.3*m.room {
		// The figure is most of the overflow: shortening points would
		// not be enough, and the model is told what to shrink instead.
		var f struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(s.Figure, &f)
		advice = fmt.Sprintf("its figure (a %s) alone takes %d%% of the room the sections have even at the smallest size it can be read at; draw it smaller (fewer steps, events, rows or bars, shorter labels) or drop it",
			f.Type, int(math.Round(100*m.figure/m.room)))
	}
	over := int(math.Ceil(100 * (m.need - m.room) / m.room))
	return &Refusal{Problems: []string{fmt.Sprintf("the sections need %d%% more height than the %s poster has, even at the smallest readable size. The tallest is sections[%d] (%q, %s): %s; or write fewer points in the other sections",
		over, c.ID, m.tallest, s.Heading, what, advice)}}
}
