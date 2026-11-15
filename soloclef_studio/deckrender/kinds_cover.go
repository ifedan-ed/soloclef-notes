package deckrender

import (
	"fmt"
	"math"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckspec"
)

// The slides that are mostly type: the title, a section divider, a quote.

// coverGround is the title and section slides' ground.
func (b *builder) coverGround() string {
	switch b.pal.cover {
	case "accent":
		return b.pal.accent
	case "tint":
		return b.pal.tint
	}
	return b.pal.paper
}

// coverInk is text on the cover's ground: white on an accent, else ink.
func (b *builder) coverInk(ground string, size float64) string {
	if ground == b.pal.accent {
		return onFill(ground, b.darkest(), size)
	}
	return b.ink(ground, size)
}

func (b *builder) titleSlide(scene *Scene, s deckspec.Slide) {
	ground := b.coverGround()
	scene.Background = ground
	if ground != b.pal.accent {
		scene.add(&Box{Name: "Cover band", X: 0, Y: 0, W: 26, H: SlideH, Preset: "rect", Fill: b.pal.accent, Reveal: Reveal{Motion: Fade}})
	}
	const x, w = 104.0, 752.0
	scene.add(b.text("Title", "the title", x, 120, w, 220, []para{plain(s.Title)}, b.pal.head.boldIfSans(), 60, 34, 1,
		b.coverInk(ground, 34), "l", "b", Reveal{Motion: Rise}))
	rule := b.pal.accent
	if ground == b.pal.accent {
		rule = "FFFFFF"
	}
	scene.add(&Box{Name: "Title rule", X: x, Y: 358, W: 96, H: 5, Preset: "rect", Fill: rule, Reveal: Reveal{Motion: GrowX, Delay: 0.3}})
	if s.Subtitle != "" {
		colour := b.muted(ground, 18)
		if ground == b.pal.accent {
			colour = b.coverInk(ground, 18)
		}
		scene.add(b.text("Subtitle", "the subtitle", x, 382, w, 100, []para{plain(s.Subtitle)}, b.pal.body, 28, 18, 1,
			colour, "l", "t", Reveal{Step: 1, Motion: Rise}))
	}
}

func (b *builder) sectionSlide(scene *Scene, s deckspec.Slide) {
	ground := b.coverGround()
	if ground == b.pal.paper {
		ground = b.pal.tint
	}
	scene.Background = ground
	number := 0
	for _, earlier := range b.deck.Slides[:b.slide] {
		if earlier.Kind == deckspec.KindSection {
			number++
		}
	}
	const x, w = 104.0, 752.0
	numberColour := legible(b.pal.accent, ground, b.opposite(ground), 80)
	if ground == b.pal.accent {
		numberColour = b.coverInk(ground, 80)
	}
	scene.add(b.text("Section number", "the section number", x, 96, 300, 110, []para{plain(fmt.Sprintf("%02d", number))}, b.pal.head.boldIfSans(), 88, 88, 1,
		numberColour, "l", "b", Reveal{Motion: Pop}))
	scene.add(b.text("Title", "the title", x, 214, w, 150, []para{plain(s.Title)}, b.pal.head.boldIfSans(), 50, 30, 1,
		b.coverInk(ground, 30), "l", "t", Reveal{Motion: Rise, Delay: 0.15}))
	if s.Subtitle != "" {
		colour := b.muted(ground, 18)
		if ground == b.pal.accent {
			colour = b.coverInk(ground, 18)
		}
		scene.add(b.text("Subtitle", "the subtitle", x, 376, w, 96, []para{plain(s.Subtitle)}, b.pal.body, 26, 18, 1,
			colour, "l", "t", Reveal{Motion: Rise, Delay: 0.35}))
	}
}

func (b *builder) quoteSlide(scene *Scene, s deckspec.Slide) {
	top, height := 120.0, 260.0
	if s.Title != "" {
		b.heading(scene, s.Title)
		top, height = contentTop+20, 230.0
	}
	// The mark is ornament, drawn faint at 200 pt and over, which the
	// layout rules leave out of contrast as WCAG leaves out decoration. A
	// quote with a heading has no room for it above the text.
	if s.Title == "" {
		mark := mix(b.pal.accent, b.pal.paper, 0.72)
		markText := []para{plain("“")}
		scene.add(&Text{Name: "Quote mark", X: marginX - 24, Y: top - 92, W: 150, H: 280, Paras: markText, Family: b.pal.head,
			Fit: ownFit(markText, b.pal.head, 220, 150), Color: mark, Align: "l", Anchor: "t", Spacing: 1, Reveal: Reveal{Motion: Fade}})
	}
	const x, w = 164.0, 690.0
	italic := family{regular: b.pal.head.italic, bold: b.pal.head.bold, italic: b.pal.head.italic}
	scene.add(b.text("Quote", "the quote", x, top, w, height, []para{plain(s.Quote)}, italic, 40, 22, 1,
		b.ink(b.pal.paper, 22), "l", "ctr", Reveal{Motion: Rise, Delay: 0.1}))
	if s.Attribution != "" {
		scene.add(b.text("Attribution", "the attribution", x, top+height+16, w, 60, []para{plain("— " + s.Attribution)}, b.pal.body, 24, 16, 1,
			b.muted(b.pal.paper, 16), "l", "t", Reveal{Step: 1, Motion: Rise}))
	}
}

// ownFit wraps text the layout itself writes (a mark, a number) at a size
// the layout chose for a box it made to hold it.
func ownFit(paras []para, fam family, size, w float64) fitted {
	f, _, _ := fit(paras, fam, size, size, 1, w, math.Inf(1))
	return f
}
