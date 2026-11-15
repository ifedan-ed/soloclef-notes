package deckrender

import (
	"fmt"
	"math"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckspec"
)

// The slides laid out on a grid: a comparison and a set of figures.

// miss is one text of a group that does not fit at the least size, and
// why; cell -1 is the group as a whole.
type miss struct {
	cell  int
	short shortfall
}

// fitCells finds one size, the largest at which every cell fits its box:
// a grid reads as one when its cells share a size. When even the least
// size does not fit, it returns every cell that does not, and why.
//
// mayBreak allows a word too wide for its cell to be broken inside it. A
// slide with more than one arrangement first tries every arrangement
// with words whole and only then again with mayBreak, so a word is broken
// only when even the widest arrangement cannot hold it.
func fitCells(cells [][]para, boxes [][2]float64, fam family, most, least float64, mayBreak bool) (float64, []fitted, []miss) {
	fams := make([]family, len(cells))
	for i := range fams {
		fams[i] = fam
	}
	return fitMixed(cells, boxes, fams, most, least, mayBreak)
}

// fitMixed is fitCells for cells in different faces (a comparison's bold
// aspects beside its regular cells).
//
// As fit does for one text, it first looks for a size at which every
// word fits whole, and, when mayBreak, breaks words only when there is
// none (and a word too wide is why).
func fitMixed(cells [][]para, boxes [][2]float64, fams []family, most, least float64, mayBreak bool) (float64, []fitted, []miss) {
	at := func(i int, size float64, breakWords bool) (fitted, shortfall, bool) {
		return fitSizes(cells[i], fams[i], size, size, 1, boxes[i][0], boxes[i][1], breakWords)
	}
	for _, breakWords := range []bool{false, true} {
		top := most
		for i := range cells {
			top = math.Min(top, topSize(cells[i], fams[i], boxes[i][0], breakWords))
		}
		if size, out, ok := fitGroup(len(cells), top, least, func(i int, size float64) (fitted, bool) {
			f, _, ok := at(i, size, breakWords)
			return f, ok
		}); ok {
			return size, out, nil
		}
		var misses []miss
		wide := false
		for i := range cells {
			if _, short, fits := at(i, least, breakWords); !fits {
				misses = append(misses, miss{cell: i, short: short})
				wide = wide || short.wide != nil
			}
		}
		if breakWords || !wide || !mayBreak {
			// Broken words did not help, would not (no word is too wide),
			// or are not allowed yet.
			return 0, nil, misses
		}
	}
	panic("unreachable")
}

// fitGroup finds the largest size from most down to least at which every
// one of n cells fits (cell says whether cell i does at a size).
//
// Each size first tries the cell that failed the size before: a group
// fails on the same long word at size after size, so trying it first
// turns most failed sizes into one wrap instead of one per cell.
func fitGroup(n int, most, least float64, cell func(i int, size float64) (fitted, bool)) (float64, []fitted, bool) {
	out := make([]fitted, n)
	hardest := 0
	for size := most; size >= least-1e-9; size -= 0.5 {
		f, fits := cell(hardest, size)
		if !fits {
			continue
		}
		out[hardest] = f
		ok := true
		for i := range n {
			if i == hardest {
				continue
			}
			f, fits := cell(i, size)
			if !fits {
				ok, hardest = false, i
				break
			}
			out[i] = f
		}
		if ok {
			return size, out, true
		}
	}
	return 0, nil, false
}

// comparisonAspects is the width of a comparison's aspect column, widest
// first: when the cells do not fit beside the usual one, the aspects
// (short names, 36 characters at most) give the cells some of their room.
func comparisonAspects(columns int) []float64 {
	if columns == 3 {
		return []float64{176, 132}
	}
	return []float64{210, 160}
}

func (b *builder) comparisonSlide(scene *Scene, s deckspec.Slide) {
	b.heading(scene, s.Title)
	const headH, gap, pad = 46.0, 12.0, 14.0
	k := len(s.Columns)
	r := len(s.Rows)
	top := contentTop + headH + 6
	rowH := (contentBot - top) / float64(r)

	headers := make([][]para, k)
	for j, name := range s.Columns {
		headers[j] = []para{plain(name)}
	}
	var cells [][]para
	var fams []family
	// names[i] is how the model's deck names cells[i].
	var names []string
	for i, row := range s.Rows {
		cells = append(cells, []para{plain(row.Aspect)})
		fams = append(fams, b.pal.body.boldIfSans())
		names = append(names, fmt.Sprintf("row %d's aspect", i+1))
		for j, c := range row.Cells {
			cells = append(cells, []para{plain(c)})
			fams = append(fams, b.pal.body)
			names = append(names, fmt.Sprintf("row %d cell %d", i+1, j+1))
		}
	}

	var headMisses, cellMisses []miss
	for _, mayBreak := range []bool{false, true} {
		for _, aspectW := range comparisonAspects(k) {
			colW := (contentW - aspectW - gap*float64(k)) / float64(k)
			headerBoxes := make([][2]float64, k)
			for j := range headers {
				headerBoxes[j] = [2]float64{colW - 2*pad, headH - 8}
			}
			var boxes [][2]float64
			for _, row := range s.Rows {
				boxes = append(boxes, [2]float64{aspectW - pad, rowH - 10})
				for range row.Cells {
					boxes = append(boxes, [2]float64{colW - 2*pad, rowH - 10})
				}
			}
			var headSize, size float64
			var headFits, fits []fitted
			headSize, headFits, headMisses = fitCells(headers, headerBoxes, b.pal.body.boldIfSans(), 22, 16, mayBreak)
			size, fits, cellMisses = fitMixed(cells, boxes, fams, 22, 14, mayBreak)
			if headMisses == nil && cellMisses == nil {
				b.drawComparison(scene, s, aspectW, colW, rowH, top, headers, headSize, headFits, cells, size, fits)
				return
			}
		}
	}
	b.report(headMisses, "", func(j int) string { return fmt.Sprintf("column %d", j+1) })
	b.report(cellMisses, "", func(i int) string { return names[i] })
}

// drawComparison draws a comparison laid out with an aspect column of
// aspectW and columns of colW.
func (b *builder) drawComparison(scene *Scene, s deckspec.Slide, aspectW, colW, rowH, top float64, headers [][]para, headSize float64, headFits []fitted, cells [][]para, size float64, fits []fitted) {
	const headH, gap, pad = 46.0, 12.0, 14.0
	colX := func(j int) float64 { return marginX + aspectW + gap + float64(j)*(colW+gap) }
	// The header: each compared thing on an accent cell.
	for j := range s.Columns {
		fill := [3]string{b.pal.accent, b.pal.accentAlt, b.pal.ink}[j]
		scene.add(&Box{Name: fmt.Sprintf("Column %d header", j+1), X: colX(j), Y: contentTop, W: colW, H: headH, Preset: "rect", Fill: fill, Reveal: Reveal{Motion: Rise, Delay: 0.1 * float64(j)}})
		scene.add(&Text{Name: fmt.Sprintf("Column %d", j+1), X: colX(j) + pad, Y: contentTop + 4, W: colW - 2*pad, H: headH - 8, Paras: headers[j], Family: b.pal.body.boldIfSans(),
			Fit: headFits[j], Color: onFill(fill, b.darkest(), headSize), Align: "ctr", Anchor: "ctr", Spacing: 1, Reveal: Reveal{Motion: Rise, Delay: 0.1 * float64(j)}})
	}
	// The rows: aspects in bold, cells in the body face, one size for all.
	n := 0
	for i, row := range s.Rows {
		y := top + float64(i)*rowH
		step := i + 1
		ground := b.pal.paper
		if i%2 == 0 && !b.pal.outline {
			ground = b.pal.tint
			scene.add(&Box{Name: fmt.Sprintf("Row %d band", i+1), X: marginX, Y: y + 2, W: contentW, H: rowH - 4, Preset: "rect", Fill: ground, Reveal: Reveal{Step: step, Motion: Fade}})
		} else {
			scene.add(&Box{Name: fmt.Sprintf("Row %d rule", i+1), X: marginX, Y: y + rowH - 1, W: contentW, H: 1, Preset: "rect", Fill: b.pal.rule, Reveal: Reveal{Step: step, Motion: GrowX}})
		}
		scene.add(&Text{Name: fmt.Sprintf("Row %d aspect", i+1), X: marginX + pad, Y: y + 5, W: aspectW - pad, H: rowH - 10, Paras: cells[n], Family: b.pal.body.boldIfSans(),
			Fit: fits[n], Color: b.ink(ground, size), Align: "l", Anchor: "ctr", Spacing: 1, Reveal: Reveal{Step: step, Motion: Rise}})
		n++
		for j := range row.Cells {
			scene.add(&Text{Name: fmt.Sprintf("Row %d cell %d", i+1, j+1), X: colX(j) + pad, Y: y + 5, W: colW - 2*pad, H: rowH - 10, Paras: cells[n], Family: b.pal.body,
				Fit: fits[n], Color: b.ink(ground, size), Align: "l", Anchor: "ctr", Spacing: 1,
				Reveal: Reveal{Step: step, Motion: Rise, Delay: 0.08 * float64(j+1)}})
			n++
		}
	}
}

// figureLeast is the least size of a figure's value: the heading's
// largest size. A stats slide exists to show its figures, so a figure is
// never drawn smaller than the slide's own title could be; past that the
// slide is laid out another way, not shrunk.
const figureLeast = headingMost

// rect is a place on a slide, in points.
type rect struct{ x, y, w, h float64 }

// figureCard is where one figure's parts go.
type figureCard struct {
	card, value, rule, label rect
}

// figureLayout is one arrangement of a stats slide's figures: where each
// card and its parts go, the value's largest size, and how the value and
// the label sit in their boxes.
type figureLayout struct {
	cards                  []figureCard
	most                   float64
	valueAlign, labelAlign string
	labelAnchor            string
	ruleGrow               Motion
}

// figureLayouts are the arrangements of n figures to try, in order: all in
// one row of tall cards (the slide as it is designed); two to a row, each
// card twice as wide; then one to a row, the value beside its label, the
// widest place a value can have. Each is tried at every size down to the
// floors before the next is, so a slide changes shape only when its text
// cannot be drawn in the shape before at a size a room can read. It is the
// layout deciding, never text cut or shrunk past the floor.
func figureLayouts(n int) []figureLayout {
	const gap, pad = 24.0, 20.0
	var out []figureLayout

	// One row of tall cards: value above, rule, label below.
	{
		const cardH = 270.0
		w := (contentW - gap*float64(n-1)) / float64(n)
		y := contentTop + (contentH-cardH)/2
		l := figureLayout{most: [5]float64{0, 0, 84, 72, 60}[n], valueAlign: "ctr", labelAlign: "ctr", labelAnchor: "t", ruleGrow: GrowX}
		for i := range n {
			x := marginX + float64(i)*(w+gap)
			l.cards = append(l.cards, figureCard{
				card:  rect{x, y, w, cardH},
				value: rect{x + pad, y + 26, w - 2*pad, 110},
				rule:  rect{x + w/2 - 24, y + 146, 48, 3},
				label: rect{x + pad, y + 162, w - 2*pad, cardH - 172},
			})
		}
		out = append(out, l)
	}

	// Two to a row: shorter cards, value above its label; a last card on
	// its own is centred under the others.
	if n > 2 {
		const rowGap = 20.0
		rows := (n + 1) / 2
		w := (contentW - gap) / 2
		cardH := (contentH - rowGap*float64(rows-1)) / float64(rows)
		l := figureLayout{most: 72, valueAlign: "ctr", labelAlign: "ctr", labelAnchor: "t", ruleGrow: GrowX}
		for i := range n {
			row, col := i/2, i%2
			x := marginX + float64(col)*(w+gap)
			if i == n-1 && n%2 == 1 {
				x = marginX + (contentW-w)/2
			}
			y := contentTop + float64(row)*(cardH+rowGap)
			l.cards = append(l.cards, figureCard{
				card:  rect{x, y, w, cardH},
				value: rect{x + pad, y + 14, w - 2*pad, 66},
				rule:  rect{x + w/2 - 24, y + 86, 48, 3},
				label: rect{x + pad, y + 96, w - 2*pad, cardH - 104},
			})
		}
		out = append(out, l)
	}

	// One to a row: the value on the left, right-aligned against a
	// vertical rule so the figures line up, the label on the right.
	{
		const rowGap = 14.0
		cardH := (contentH - rowGap*float64(n-1)) / float64(n)
		valueW := math.Round((contentW - 2*pad) * 0.42)
		labelX := pad + valueW + 35
		l := figureLayout{most: 60, valueAlign: "r", labelAlign: "l", labelAnchor: "ctr", ruleGrow: Fade}
		for i := range n {
			x, y := marginX, contentTop+float64(i)*(cardH+rowGap)
			l.cards = append(l.cards, figureCard{
				card:  rect{x, y, contentW, cardH},
				value: rect{x + pad, y + 6, valueW, cardH - 12},
				rule:  rect{x + pad + valueW + 16, y + cardH*0.2, 3, cardH * 0.6},
				label: rect{x + labelX, y + 6, contentW - labelX - pad, cardH - 12},
			})
		}
		out = append(out, l)
	}
	return out
}

// fitFigures fits a slide's values, each on one line when they can all
// be at some size down to the least, wrapped only when they cannot: a
// figure wrapped at a larger size ("11.2 → 4.6 per" over "1000") parts
// the number from its unit, which reads worse than the same figure on one
// line a few points smaller.
func fitFigures(values [][]para, boxes [][2]float64, face family, most float64, mayBreak bool) (float64, []fitted, []miss) {
	top := most
	for i := range values {
		top = math.Min(top, topSize(values[i], face, boxes[i][0], false))
	}
	size, fits, ok := fitGroup(len(values), top, figureLeast, func(i int, size float64) (fitted, bool) {
		f, _, fits := fitSizes(values[i], face, size, size, 1, boxes[i][0], boxes[i][1], false)
		return f, fits && len(f.lines) == 1
	})
	if ok {
		return size, fits, nil
	}
	return fitCells(values, boxes, face, most, figureLeast, mayBreak)
}

func (b *builder) statsSlide(scene *Scene, s deckspec.Slide) {
	b.heading(scene, s.Title)
	n := len(s.Stats)
	values := make([][]para, n)
	labels := make([][]para, n)
	for i, st := range s.Stats {
		values[i] = []para{plain(st.Value)}
		labels[i] = []para{plain(st.Label)}
	}
	valueFace := b.pal.head.boldIfSans()
	var valueMisses, labelMisses []miss
	for _, mayBreak := range []bool{false, true} {
		for _, l := range figureLayouts(n) {
			valueBoxes := make([][2]float64, n)
			labelBoxes := make([][2]float64, n)
			for i, c := range l.cards {
				valueBoxes[i] = [2]float64{c.value.w, c.value.h}
				labelBoxes[i] = [2]float64{c.label.w, c.label.h}
			}
			var valueSize, labelSize float64
			var valueFits, labelFits []fitted
			valueSize, valueFits, valueMisses = fitFigures(values, valueBoxes, valueFace, l.most, mayBreak)
			if valueMisses != nil {
				continue
			}
			labelSize, labelFits, labelMisses = fitCells(labels, labelBoxes, b.pal.body, 24, bodyLeast, mayBreak)
			if labelMisses != nil {
				continue
			}
			b.drawFigures(scene, s, l, values, valueSize, valueFits, labels, labelSize, labelFits)
			return
		}
	}
	// Even the widest arrangement does not hold them: what is said is
	// measured in it, the most room the slide can give.
	b.report(valueMisses, "", func(i int) string { return fmt.Sprintf("stat %d's value", i+1) })
	b.report(labelMisses, "", func(i int) string { return fmt.Sprintf("stat %d's label", i+1) })
}

// drawFigures draws a stats slide in the arrangement l.
func (b *builder) drawFigures(scene *Scene, s deckspec.Slide, l figureLayout, values [][]para, valueSize float64, valueFits []fitted, labels [][]para, labelSize float64, labelFits []fitted) {
	valueFace := b.pal.head.boldIfSans()
	for i, c := range l.cards {
		step := i + 1
		fill := [2]string{b.pal.tint, b.pal.tintAlt}[i%2]
		card, ground := b.card(fmt.Sprintf("Figure %d card", i+1), c.card.x, c.card.y, c.card.w, c.card.h, fill, Reveal{Step: step, Motion: Rise})
		scene.add(card)
		accent := [2]string{b.pal.accent, b.pal.accentAlt}[i%2]
		scene.add(&Text{Name: fmt.Sprintf("Figure %d", i+1), X: c.value.x, Y: c.value.y, W: c.value.w, H: c.value.h, Paras: values[i], Family: valueFace,
			Fit: valueFits[i], Color: legible(accent, ground, b.opposite(ground), valueSize), Align: l.valueAlign, Anchor: "ctr", Spacing: 1,
			Reveal: Reveal{Step: step, Motion: Rise, Delay: 0.05}, Count: countable(s.Stats[i].Value)})
		scene.add(&Box{Name: fmt.Sprintf("Figure %d rule", i+1), X: c.rule.x, Y: c.rule.y, W: c.rule.w, H: c.rule.h, Preset: "rect", Fill: accent, Reveal: Reveal{Step: step, Motion: l.ruleGrow, Delay: 0.3}})
		scene.add(&Text{Name: fmt.Sprintf("Figure %d label", i+1), X: c.label.x, Y: c.label.y, W: c.label.w, H: c.label.h, Paras: labels[i], Family: b.pal.body,
			Fit: labelFits[i], Color: b.ink(ground, labelSize), Align: l.labelAlign, Anchor: l.labelAnchor, Spacing: 1, Reveal: Reveal{Step: step, Motion: Rise, Delay: 0.2}})
	}
}
