package deckrender

import (
	"fmt"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckspec"
)

// The list slides: bullets, two columns, and the closing sources slide.

// list places points, each with a marker, top-down in a box: one size for
// every point, the largest that lets them all fit; the space left over
// goes between them, up to a limit, so a short list does not huddle at the
// top. Each point comes in on its own step from first on, one after
// another when they share a step.
// what names the list as a whole and point(i) one of its points, as the
// deck's fields are named, for the problems sent back.
func (b *builder) list(scene *Scene, name, what string, point func(int) string, points []string, x, y, w, h float64, ground, marker string, most float64, first int, ownSteps bool) {
	const markerSize, indent, minGap, maxGap = 11.0, 28.0, 14.0, 34.0
	items := make([][]para, len(points))
	for i, p := range points {
		items[i] = []para{leadBold(p)}
	}
	fits, misses := fitTogether(items, b.pal.body, most, bodyLeast, 1, w-indent, h, minGap)
	if misses != nil {
		b.report(misses, what, point)
		return
	}
	used := minGap * float64(len(points)-1)
	for _, f := range fits {
		used += f.height
	}
	gap := minGap
	if len(points) > 1 {
		gap = min(maxGap, minGap+(h-used)/float64(len(points)))
	}
	size := fits[0].size
	colour := b.ink(ground, size)
	at := y
	for i, f := range fits {
		step, delay := first+i, 0.0
		if !ownSteps {
			step, delay = first, 0.12*float64(i)
		}
		centre := at + f.ascent - size*0.32
		scene.add(&Box{Name: fmt.Sprintf("%s marker %d", name, i+1), X: x, Y: centre - markerSize/2, W: markerSize, H: markerSize,
			Preset: "ellipse", Fill: marker, Reveal: Reveal{Step: step, Motion: Pop, Delay: delay}})
		text := &Text{Name: fmt.Sprintf("%s %d", name, i+1), X: x + indent, Y: at, W: w - indent, H: f.height + 2, Paras: items[i],
			Family: b.pal.body, Fit: f, Color: colour, Align: "l", Anchor: "t", Spacing: 1,
			Reveal: Reveal{Step: step, Motion: Rise, Delay: delay + 0.05}}
		scene.add(text)
		at += f.height + gap
	}
}

func (b *builder) bulletsSlide(scene *Scene, s deckspec.Slide) {
	b.heading(scene, s.Title)
	b.list(scene, "Bullet", "the bullets", func(i int) string { return fmt.Sprintf("bullet %d", i+1) }, s.Bullets, marginX, contentTop+6, contentW, contentH-6, b.pal.paper, b.pal.accent, 28, 1, true)
}

func (b *builder) twoColumnSlide(scene *Scene, s deckspec.Slide) {
	b.heading(scene, s.Title)
	const gap, pad, headH = 28.0, 24.0, 40.0
	w := (contentW - gap) / 2
	for i, col := range []*deckspec.Column{s.Left, s.Right} {
		side := [2]string{"Left", "Right"}[i]
		fill := [2]string{b.pal.tint, b.pal.tintAlt}[i]
		marker := [2]string{b.pal.accent, b.pal.accentAlt}[i]
		x := marginX + float64(i)*(w+gap)
		step := 1 + i
		card, ground := b.card(side+" column", x, contentTop, w, contentH, fill, Reveal{Step: step, Motion: Rise})
		scene.add(card)
		scene.add(b.text(side+" heading", "the "+[2]string{"left", "right"}[i]+" column's heading", x+pad, contentTop+pad-4, w-2*pad, headH, []para{plain(col.Heading)},
			b.pal.head.boldIfSans(), 26, 18, 1, legible(marker, ground, b.opposite(ground), 18), "l", "ctr", Reveal{Step: step, Motion: Rise, Delay: 0.1}))
		lower := [2]string{"left", "right"}[i]
		b.list(scene, side+" bullet", "the "+lower+" column's bullets", func(j int) string { return fmt.Sprintf("%s column bullet %d", lower, j+1) }, col.Bullets, x+pad, contentTop+pad+headH+12, w-2*pad, contentH-2*pad-headH-12,
			ground, legible(marker, ground, b.opposite(ground), 18), 22, step, false)
	}
}

// sourcesSlide lists the deck's sources, numbered as the slides cite them.
// Each source is a box of its own: a reference list is read up close, so it
// may go down to the slide's floor of 12 pt, which the layout rules allow
// for single paragraphs and not for a body of three or more in one box.
func (b *builder) sourcesSlide(scene *Scene) {
	b.heading(scene, "Sources")
	items := make([][]para, len(b.deck.Sources))
	for i, source := range b.deck.Sources {
		text := source.Title
		if source.Detail != "" {
			text += ". " + source.Detail
		}
		items[i] = []para{{spans: []span{{text: strconv.Itoa(i+1) + ".", bold: true}, {text: " " + text}}}}
	}
	// One column holds up to about eight; more go in two.
	columns := [][][]para{items}
	if len(items) > 8 {
		half := (len(items) + 1) / 2
		columns = [][][]para{items[:half], items[half:]}
	}
	const between, gap = 28.0, 12.0
	w := (contentW - between*float64(len(columns)-1)) / float64(len(columns))
	colour := b.ink(b.pal.paper, 12)
	n := 0
	for c, col := range columns {
		x := marginX + float64(c)*(w+between)
		fits, misses := fitTogether(col, b.pal.body, 20, 12, 1, w, contentH, gap)
		if misses != nil {
			first := n
			b.report(misses, "the sources (shorten their titles and details)", func(i int) string { return fmt.Sprintf("source %d", first+i+1) })
			return
		}
		at := contentTop
		for i, f := range fits {
			n++
			scene.add(&Text{Name: fmt.Sprintf("Source %d", n), X: x, Y: at, W: w, H: f.height + 2, Paras: col[i], Family: b.pal.body, Fit: f,
				Color: colour, Align: "l", Anchor: "t", Spacing: 1, Reveal: Reveal{Motion: Rise, Delay: 0.08 * float64(n)}})
			at += f.height + gap
		}
	}
}
