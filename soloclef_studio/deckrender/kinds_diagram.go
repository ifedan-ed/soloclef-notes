package deckrender

import (
	"fmt"
	"math"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckspec"
)

// The slides that are drawings: a timeline, a flow of stages, a cycle.
// Every part is a native shape in the deck, so a person can move or recolour
// it in PowerPoint, and each part comes in on its own step in the video.
//
// They are drawn here and not by figures-go (Tools' figure library):
// figures-go draws a figure as one picture for a chat answer, while a slide
// needs the same diagram as editable shapes in the deck and as separate
// parts that come in one by one in the video; and figures-go is a private
// module, which the public copy of Soloclef could not build.

// timelineEvent is where one event's parts go.
type timelineEvent struct {
	dot                     float64 // the dot's centre on the axis
	label, text             rect
	labelAnchor, textAnchor string
}

// timelineLayout is one arrangement of a timeline: its axis and events.
type timelineLayout struct {
	axisY  float64
	events []timelineEvent
}

// timelineLayouts are the arrangements of n events to try, in order: every
// event's label above the axis and its text below, each in its own slot
// (the timeline as designed); then, from four events, the events taking
// turns above and below the axis, so each has the room of two slots,
// since its neighbours are on the other side. The dots are then spaced so
// the first and last boxes stay inside the margins.
func timelineLayouts(n int) []timelineLayout {
	slot := contentW / float64(n)
	const axisY = 290.0
	row := timelineLayout{axisY: axisY}
	for i := range n {
		centre := marginX + slot*(float64(i)+0.5)
		row.events = append(row.events, timelineEvent{
			dot:         centre,
			label:       rect{centre - (slot-12)/2, axisY - 26 - 64, slot - 12, 64},
			text:        rect{centre - (slot-14)/2, axisY + 26, slot - 14, contentBot - axisY - 30},
			labelAnchor: "b", textAnchor: "t",
		})
	}
	out := []timelineLayout{row}
	if n < 4 {
		return out
	}
	// Boxes on one side are two dots apart and must leave 16 pt between
	// them: 2(contentW-w)/(n-1) >= w+16.
	const sideGap = 16.0
	w := math.Floor((2*contentW - sideGap*float64(n-1)) / float64(n+1))
	step := (contentW - w) / float64(n-1)
	mid := contentTop + contentH/2
	turns := timelineLayout{axisY: mid}
	for i := range n {
		centre := marginX + w/2 + float64(i)*step
		x := centre - w/2
		e := timelineEvent{dot: centre}
		if i%2 == 0 {
			// Above: the label next to the axis, the text over it.
			e.label = rect{x, mid - 22 - 36, w, 36}
			e.text = rect{x, contentTop + 4, w, mid - 22 - 36 - 6 - (contentTop + 4)}
			e.labelAnchor, e.textAnchor = "b", "b"
		} else {
			e.label = rect{x, mid + 22, w, 36}
			e.text = rect{x, mid + 22 + 36 + 6, w, contentBot - (mid + 22 + 36 + 6)}
			e.labelAnchor, e.textAnchor = "t", "t"
		}
		turns.events = append(turns.events, e)
	}
	return append(out, turns)
}

func (b *builder) timelineSlide(scene *Scene, s deckspec.Slide) {
	b.heading(scene, s.Title)
	n := len(s.Events)
	labels := make([][]para, n)
	texts := make([][]para, n)
	for i, e := range s.Events {
		labels[i] = []para{plain(e.Label)}
		texts[i] = []para{plain(e.Text)}
	}
	var labelMisses, textMisses []miss
	for _, mayBreak := range []bool{false, true} {
		for _, l := range timelineLayouts(n) {
			labelBoxes := make([][2]float64, n)
			textBoxes := make([][2]float64, n)
			for i, e := range l.events {
				labelBoxes[i] = [2]float64{e.label.w, e.label.h}
				textBoxes[i] = [2]float64{e.text.w, e.text.h}
			}
			var labelSize, textSize float64
			var labelFits, textFits []fitted
			if labelSize, labelFits, labelMisses = fitCells(labels, labelBoxes, b.pal.head.boldIfSans(), 28, 16, mayBreak); labelMisses != nil {
				continue
			}
			if textSize, textFits, textMisses = fitCells(texts, textBoxes, b.pal.body, 22, 14, mayBreak); textMisses != nil {
				continue
			}
			b.drawTimeline(scene, l, labels, labelSize, labelFits, texts, textSize, textFits)
			return
		}
	}
	b.report(labelMisses, "", func(i int) string { return fmt.Sprintf("event %d's label", i+1) })
	b.report(textMisses, "", func(i int) string { return fmt.Sprintf("event %d's text", i+1) })
}

func (b *builder) drawTimeline(scene *Scene, l timelineLayout, labels [][]para, labelSize float64, labelFits []fitted, texts [][]para, textSize float64, textFits []fitted) {
	const dot = 20.0
	scene.add(&Stroke{Name: "Timeline axis", X1: marginX, Y1: l.axisY, X2: SlideW - marginX, Y2: l.axisY, Color: b.pal.rule, Width: 4, Arrow: true,
		Reveal: Reveal{Motion: Draw, Delay: 0.1}})
	for i, e := range l.events {
		step := i + 1
		accent := [2]string{b.pal.accent, b.pal.accentAlt}[i%2]
		scene.add(&Box{Name: fmt.Sprintf("Event %d dot", i+1), X: e.dot - dot/2, Y: l.axisY - dot/2, W: dot, H: dot, Preset: "ellipse", Fill: accent,
			Line: b.pal.paper, LineW: 3, Reveal: Reveal{Step: step, Motion: Pop}})
		scene.add(&Text{Name: fmt.Sprintf("Event %d label", i+1), X: e.label.x, Y: e.label.y, W: e.label.w, H: e.label.h, Paras: labels[i],
			Family: b.pal.head.boldIfSans(), Fit: labelFits[i], Color: legible(accent, b.pal.paper, b.opposite(b.pal.paper), labelSize),
			Align: "ctr", Anchor: e.labelAnchor, Spacing: 1, Reveal: Reveal{Step: step, Motion: Rise, Delay: 0.08}})
		scene.add(&Text{Name: fmt.Sprintf("Event %d", i+1), X: e.text.x, Y: e.text.y, W: e.text.w, H: e.text.h, Paras: texts[i],
			Family: b.pal.body, Fit: textFits[i], Color: b.ink(b.pal.paper, textSize), Align: "ctr", Anchor: e.textAnchor, Spacing: 1,
			Reveal: Reveal{Step: step, Motion: Rise, Delay: 0.16}})
	}
}

// stageGeometry is where a stage card's label and text go: beside the
// badge on a short card, under it on a tall one (where a label beside the
// badge would be squeezed into a third of the card's width).
type stageGeometry struct {
	labelX, labelY, labelW, labelH float64
	textY, textH                   float64
}

const stageBadge = 32.0

func geometryOf(w, h float64) stageGeometry {
	if h >= 200 {
		return stageGeometry{labelX: 16, labelY: 58, labelW: w - 32, labelH: 56, textY: 122, textH: h - 132}
	}
	return stageGeometry{labelX: 56, labelY: 10, labelW: w - 70, labelH: 44, textY: 62, textH: h - 70}
}

// stage is one diagram stage's card: a numbered badge, its label and its
// text, in a box of the given size.
func (b *builder) stage(scene *Scene, i int, st deckspec.Step, x, y, w, h float64, size stageSizes, fits stageFits, reveal Reveal) {
	g := geometryOf(w, h)
	fill := [2]string{b.pal.tint, b.pal.tintAlt}[i%2]
	accent := [2]string{b.pal.accent, b.pal.accentAlt}[i%2]
	card, ground := b.card(fmt.Sprintf("Stage %d card", i+1), x, y, w, h, fill, reveal)
	scene.add(card)
	scene.add(&Box{Name: fmt.Sprintf("Stage %d badge", i+1), X: x + 14, Y: y + 14, W: stageBadge, H: stageBadge, Preset: "ellipse", Fill: accent,
		Reveal: Reveal{Step: reveal.Step, Motion: Pop, Delay: reveal.Delay + 0.1}})
	number := []para{plain(strconv.Itoa(i + 1))}
	scene.add(&Text{Name: fmt.Sprintf("Stage %d number", i+1), X: x + 14, Y: y + 14, W: stageBadge, H: stageBadge, Paras: number, Family: b.pal.body.boldIfSans(),
		Fit: ownFit(number, b.pal.body.boldIfSans(), 16, stageBadge), Color: onFill(accent, b.darkest(), 16), Align: "ctr", Anchor: "ctr", Spacing: 1,
		Reveal: Reveal{Step: reveal.Step, Motion: Pop, Delay: reveal.Delay + 0.1}})
	anchor := "ctr"
	if h >= 200 {
		anchor = "t"
	}
	scene.add(&Text{Name: fmt.Sprintf("Stage %d label", i+1), X: x + g.labelX, Y: y + g.labelY, W: g.labelW, H: g.labelH, Paras: []para{plain(st.Label)},
		Family: b.pal.body.boldIfSans(), Fit: fits.label[i], Color: b.ink(ground, size.label), Align: "l", Anchor: anchor, Spacing: 1,
		Reveal: Reveal{Step: reveal.Step, Motion: Rise, Delay: reveal.Delay + 0.12}})
	if st.Text != "" {
		scene.add(&Text{Name: fmt.Sprintf("Stage %d", i+1), X: x + 16, Y: y + g.textY, W: w - 32, H: g.textH, Paras: []para{plain(st.Text)},
			Family: b.pal.body, Fit: fits.text[i], Color: b.ink(ground, size.text), Align: "l", Anchor: "t", Spacing: 1,
			Reveal: Reveal{Step: reveal.Step, Motion: Rise, Delay: reveal.Delay + 0.2}})
	}
}

type stageSizes struct{ label, text float64 }
type stageFits struct{ label, text []fitted }

// fitStages fits every stage's label and text to cards of w by h, one size
// for the labels and one for the texts, or returns what does not fit.
func (b *builder) fitStages(steps []deckspec.Step, w, h, textLeast float64, mayBreak bool) (stageSizes, stageFits, []miss, []miss) {
	g := geometryOf(w, h)
	labels := make([][]para, len(steps))
	labelBoxes := make([][2]float64, len(steps))
	texts := make([][]para, len(steps))
	textBoxes := make([][2]float64, len(steps))
	for i, st := range steps {
		labels[i] = []para{plain(st.Label)}
		labelBoxes[i] = [2]float64{g.labelW, g.labelH}
		texts[i] = []para{plain(st.Text)}
		textBoxes[i] = [2]float64{w - 32, g.textH}
	}
	labelSize, labelFits, labelMisses := fitCells(labels, labelBoxes, b.pal.body.boldIfSans(), 24, 15, mayBreak)
	if labelMisses != nil {
		return stageSizes{}, stageFits{}, labelMisses, nil
	}
	textSize, textFits, textMisses := fitCells(texts, textBoxes, b.pal.body, 20, textLeast, mayBreak)
	if textMisses != nil {
		return stageSizes{}, stageFits{}, nil, textMisses
	}
	return stageSizes{labelSize, textSize}, stageFits{labelFits, textFits}, nil, nil
}

// arrow is a connector drawn between two stage cards, coming in on step
// after delay seconds.
type arrow struct {
	x1, y1, x2, y2 float64
	step           int
	delay          float64
}

// stageLayout is one arrangement of a diagram's stages: every card (all of
// one size, w by h), the arrows between them, and the least size of the
// stages' texts.
type stageLayout struct {
	w, h      float64
	cards     []rect
	arrows    []arrow
	textLeast float64
}

// snake lays n stages out in rows of perRow, the second row running back
// right to left under the first, so every arrow is short and none crosses
// a card. closed adds the arrow from the last stage back to the first, up
// the left side: a cycle drawn as a loop.
func snake(n, perRow int, closed bool, textLeast float64) stageLayout {
	const arrowGap, rowGap = 44.0, 48.0
	rows := (n + perRow - 1) / perRow
	w := (contentW - arrowGap*float64(perRow-1)) / float64(perRow)
	h, gapY := 236.0, 0.0
	if rows == 2 {
		h, gapY = 146, rowGap
	}
	top := contentTop + (contentH-(h*float64(rows)+gapY))/2
	place := func(i int) (float64, float64) {
		row, col := i/perRow, i%perRow
		if row == 1 {
			col = perRow - 1 - col
		}
		return marginX + float64(col)*(w+arrowGap), top + float64(row)*(h+gapY)
	}
	l := stageLayout{w: w, h: h, textLeast: textLeast}
	for i := range n {
		x, y := place(i)
		l.cards = append(l.cards, rect{x, y, w, h})
		if i == 0 {
			continue
		}
		px, py := place(i - 1)
		a := arrow{step: i + 1}
		switch {
		case py != y: // down to the next row
			a.x1, a.y1, a.x2, a.y2 = px+w/2, py+h+6, x+w/2, y-8
		case px < x: // right
			a.x1, a.y1, a.x2, a.y2 = px+w+6, y+h/2, x-8, y+h/2
		default: // left, on the second row
			a.x1, a.y1, a.x2, a.y2 = px-6, y+h/2, x+w+8, y+h/2
		}
		l.arrows = append(l.arrows, a)
	}
	if closed {
		lx, ly := place(n - 1)
		fx, fy := place(0)
		a := arrow{step: n, delay: 0.6}
		if lx == fx {
			// Straight up from the last card to the first.
			a.x1, a.y1, a.x2, a.y2 = lx+w/2, ly-6, fx+w/2, fy+h+8
		} else {
			// From the last card's left side, across the empty corner
			// under the first card, to that card's foot.
			a.x1, a.y1, a.x2, a.y2 = lx-6, ly+h/2, fx+w/2, fy+h+8
		}
		l.arrows = append(l.arrows, a)
	}
	return l
}

// flowLayouts are the arrangements of a flow of n stages to try, in
// order: up to four in one row of tall cards, five and six in two rows of
// three; then, for three or four, two rows of two, each card more than
// twice as wide.
func flowLayouts(n int) []stageLayout {
	if n > 4 {
		return []stageLayout{snake(n, (n+1)/2, false, 14)}
	}
	return []stageLayout{snake(n, n, false, 14), snake(n, (n+1)/2, false, 14)}
}

// The ellipse a cycle's cards sit on, per number of stages: the cards'
// height, the ellipse's vertical radius and centre, and the card widths to
// try (the usual, then a wider one). Each is chosen so that, at every
// width, no two cards overlap and every arrow is at least 12 pt long and
// points from a stage to the next (TestNoCardsOverlapAndEveryArrowLeads);
// five and six stages need lower cards and a taller ellipse for that,
// which the one geometry for all used to lack: at six, two cards stacked
// on the right overlapped by 8 pt, and at five one arrow pointed backwards.
var cycleEllipses = map[int]struct {
	h, ry, cy float64
	widths    []float64
}{
	3: {116, 108, 318, []float64{226, 266}},
	4: {116, 108, 318, []float64{226, 246}},
	5: {108, 128, 328, []float64{192, 214}},
	6: {92, 124, 316, []float64{192, 214}},
}

// cycleRX is the ellipse's horizontal radius: as wide as the slide allows.
const cycleRX = 282.0

// ellipse lays a cycle's n stages out on an ellipse, clockwise from the
// top, cards w wide, with an arrow from each to the next and the last back
// to the first.
func ellipse(n int, w float64) stageLayout {
	e := cycleEllipses[n]
	h := e.h
	centre := func(i int) (float64, float64) {
		angle := -math.Pi/2 + 2*math.Pi*float64(i)/float64(n)
		return SlideW/2 + cycleRX*math.Cos(angle), e.cy + e.ry*math.Sin(angle)
	}
	// edge is how far from a card's centre a line in direction (dx, dy)
	// leaves the card.
	edge := func(dx, dy float64) float64 {
		tx, ty := math.Inf(1), math.Inf(1)
		if dx != 0 {
			tx = (w / 2) / math.Abs(dx)
		}
		if dy != 0 {
			ty = (h / 2) / math.Abs(dy)
		}
		return math.Min(tx, ty)
	}
	l := stageLayout{w: w, h: h, textLeast: 13}
	for i := range n {
		x, y := centre(i)
		l.cards = append(l.cards, rect{x - w/2, y - h/2, w, h})
	}
	// Each arrow comes in with the stage it leads to; the last closes the
	// cycle with the last stage.
	for i := range n {
		x1, y1 := centre(i)
		x2, y2 := centre((i + 1) % n)
		dx, dy := x2-x1, y2-y1
		length := math.Hypot(dx, dy)
		dx, dy = dx/length, dy/length
		from, to := edge(dx, dy)+6, edge(dx, dy)+8
		a := arrow{x1: x1 + dx*from, y1: y1 + dy*from, x2: x2 - dx*to, y2: y2 - dy*to, step: i + 2}
		if i == n-1 {
			a.step, a.delay = n, 0.6
		}
		l.arrows = append(l.arrows, a)
	}
	return l
}

// cycleLayouts are the arrangements of a cycle of n stages to try, in
// order: the ellipse at its usual card width, the ellipse with wider
// cards, then, from four stages, the loop: two rows like a flow's, with
// the arrow from the last stage back up to the first, where every card is
// wider still.
func cycleLayouts(n int) []stageLayout {
	var out []stageLayout
	for _, w := range cycleEllipses[n].widths {
		out = append(out, ellipse(n, w))
	}
	if n >= 4 {
		out = append(out, snake(n, (n+1)/2, true, 13))
	}
	return out
}

// diagramSlide draws a flow or a cycle in the first of its arrangements
// that holds every stage's label and text.
func (b *builder) diagramSlide(scene *Scene, s deckspec.Slide, layouts []stageLayout) {
	b.heading(scene, s.Title)
	var labelMisses, textMisses []miss
	for _, mayBreak := range []bool{false, true} {
		for _, l := range layouts {
			var size stageSizes
			var fits stageFits
			size, fits, labelMisses, textMisses = b.fitStages(s.Steps, l.w, l.h, l.textLeast, mayBreak)
			if labelMisses != nil || textMisses != nil {
				continue
			}
			for i, st := range s.Steps {
				c := l.cards[i]
				b.stage(scene, i, st, c.x, c.y, c.w, c.h, size, fits, Reveal{Step: i + 1, Motion: Rise, Delay: 0.2})
			}
			// The arrows go after every card, so none is drawn under a card.
			for i, a := range l.arrows {
				scene.add(&Stroke{Name: fmt.Sprintf("Arrow %d", i+1), X1: a.x1, Y1: a.y1, X2: a.x2, Y2: a.y2,
					Color: b.pal.accent, Width: 3, Arrow: true, Reveal: Reveal{Step: a.step, Motion: Draw, Delay: a.delay}})
			}
			return
		}
	}
	b.report(labelMisses, "", func(i int) string { return fmt.Sprintf("step %d's label", i+1) })
	b.report(textMisses, "", func(i int) string { return fmt.Sprintf("step %d's text", i+1) })
}

func (b *builder) flowSlide(scene *Scene, s deckspec.Slide) {
	b.diagramSlide(scene, s, flowLayouts(len(s.Steps)))
}

func (b *builder) cycleSlide(scene *Scene, s deckspec.Slide) {
	b.diagramSlide(scene, s, cycleLayouts(len(s.Steps)))
}

// chartValueWidths are the widths to try for a bar chart's value column,
// narrowest first: a value with a long unit takes room from the bars
// rather than being refused.
var chartValueWidths = []float64{96, 150, 210}

func (b *builder) chartSlide(scene *Scene, s deckspec.Slide) {
	b.heading(scene, s.Title)
	const labelW, captionH = 230.0, 34.0
	n := len(s.Bars)
	bottom := contentBot
	if s.Caption != "" {
		bottom -= captionH + 8
	}
	rowH := (bottom - contentTop) / float64(n)
	labels := make([][]para, n)
	labelBoxes := make([][2]float64, n)
	values := make([][]para, n)
	for i, bar := range s.Bars {
		labels[i] = []para{plain(bar.Label)}
		labelBoxes[i] = [2]float64{labelW, rowH - 4}
		values[i] = []para{plain(deckspec.FormatNumber(bar.Value) + unitSuffix(s.Unit))}
	}
	labelSize, labelFits, misses := fitCells(labels, labelBoxes, b.pal.body, 22, 14, true)
	if misses != nil {
		b.report(misses, "", func(i int) string { return fmt.Sprintf("bar %d's label", i+1) })
		return
	}
	var valueW, valueSize float64
	var valueFits []fitted
	fitValues := func() {
		for _, mayBreak := range []bool{false, true} {
			for _, valueW = range chartValueWidths {
				valueBoxes := make([][2]float64, n)
				for i := range values {
					valueBoxes[i] = [2]float64{valueW - 8, rowH - 4}
				}
				if valueSize, valueFits, misses = fitCells(values, valueBoxes, b.pal.body.boldIfSans(), 22, 13, mayBreak); misses == nil {
					return
				}
			}
		}
	}
	fitValues()
	if misses != nil {
		b.report(misses, "", func(i int) string { return fmt.Sprintf("bar %d's value with its unit (shorten the unit)", i+1) })
		return
	}
	barH := math.Min(rowH*0.62, 46)
	x0 := marginX + labelW + 18
	span := SlideW - marginX - valueW - x0
	largest := 0.0
	for _, bar := range s.Bars {
		largest = math.Max(largest, bar.Value)
	}
	scene.add(&Stroke{Name: "Chart axis", X1: x0, Y1: contentTop, X2: x0, Y2: bottom, Color: b.pal.rule, Width: 2, Reveal: Reveal{Motion: Draw}})
	for i, bar := range s.Bars {
		y := contentTop + float64(i)*rowH
		scene.add(&Text{Name: fmt.Sprintf("Bar %d label", i+1), X: marginX, Y: y + 2, W: labelW, H: rowH - 4, Paras: labels[i], Family: b.pal.body,
			Fit: labelFits[i], Color: b.ink(b.pal.paper, labelSize), Align: "r", Anchor: "ctr", Spacing: 1, Reveal: Reveal{Motion: Rise, Delay: 0.05 * float64(i)}})
		length := math.Max(span*bar.Value/largest, 3)
		delay := 0.12 * float64(i)
		scene.add(&Box{Name: fmt.Sprintf("Bar %d", i+1), X: x0 + 1, Y: y + (rowH-barH)/2, W: length, H: barH, Preset: "rect", Fill: b.pal.accent,
			Reveal: Reveal{Step: 1, Motion: GrowX, Delay: delay}})
		text := deckspec.FormatNumber(bar.Value) + unitSuffix(s.Unit)
		scene.add(&Text{Name: fmt.Sprintf("Bar %d value", i+1), X: x0 + length + 10, Y: y + 2, W: valueW - 8, H: rowH - 4, Paras: values[i],
			Family: b.pal.body.boldIfSans(), Fit: valueFits[i], Color: b.ink(b.pal.paper, valueSize), Align: "l", Anchor: "ctr", Spacing: 1,
			Reveal: Reveal{Step: 1, Motion: Rise, Delay: delay + 0.5}, Count: countable(text)})
	}
	if s.Caption != "" {
		scene.add(b.text("Caption", "the chart's caption", marginX, bottom+8, contentW, captionH, []para{plain(s.Caption)}, b.pal.body, 16, 12, 1,
			b.muted(b.pal.paper, 12), "l", "ctr", Reveal{Step: 1, Motion: Fade, Delay: 0.4}))
	}
}

func unitSuffix(unit string) string {
	switch unit {
	case "":
		return ""
	case "%":
		return "%"
	}
	return " " + unit
}
