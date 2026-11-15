package deckrender

// What another renderer may borrow from the deck's: its themes' colours,
// its faces and how they are measured, and its contrast rule. The
// infographic poster (soloclef_studio/posterrender) is drawn in these, so a
// poster and a deck made from the same notebook look like one product and
// are measured by one set of metrics.
//
// This file only reads the deck's own tables and functions; it holds no
// layout. The deck's layout code stays private to this package, so a
// change to how slides are laid out cannot reach a poster by accident.

// Look is one theme's look as another renderer needs it: colours as six
// hex digits (no "#"), whether its headings are serif, and its shape
// choices.
type Look struct {
	Paper, Ink, Muted, Rule string
	Accent, AccentAlt       string
	Tint, TintAlt           string
	// SerifHeadings: headings are set in the serif face (Caladea), the
	// body always in the sans (Carlito).
	SerifHeadings bool
	// Dark is a theme whose paper is dark.
	Dark bool
	// Radius is a card's corner in points; Outline draws cards as an ink
	// outline instead of a tinted fill.
	Radius  float64
	Outline bool
}

// LookOf is a deck theme's look, by the theme's id (deckspec.Themes).
func LookOf(theme string) (Look, bool) {
	p, ok := palettes[theme]
	if !ok {
		return Look{}, false
	}
	return Look{
		Paper: p.paper, Ink: p.ink, Muted: p.muted, Rule: p.rule,
		Accent: p.accent, AccentAlt: p.accentAlt, Tint: p.tint, TintAlt: p.tintAlt,
		SerifHeadings: p.head.regular.Family == serifRegular.Family, Dark: p.dark,
		Radius: p.radius, Outline: p.outline,
	}, true
}

// FaceOf is the deck face for a role: serif or sans, bold or not.
func FaceOf(serifFace, bold bool) Face {
	if serifFace {
		return serif.style(bold, false)
	}
	return sans.style(bold, false)
}

// TextWidth is text's width in points at size in face, measured with the
// very metrics a slide is laid out with.
func TextWidth(face Face, text string, size float64) float64 { return width(face, text, size) }

// FaceMetrics is a face's line height and baseline, in ems: a line of
// text at size is LineEm*size tall, its baseline AscentEm*size below the
// line's top.
func FaceMetrics(face Face) (lineEm, ascentEm float64) {
	m := metricsOf(face)
	return m.line, m.ascent
}

// SVGFamily is the family name a rasteriser finds in the font files for
// face (the open twin's own name: Carlito or Caladea).
func SVGFamily(face Face) string { return svgFamily(face) }

// FontFile is the bytes of face's font file, for a document that carries
// its fonts with it (an SVG's @font-face).
func FontFile(face Face) ([]byte, error) { return fontFiles.ReadFile("fonts/" + face.File) }

// Legible is colour as text of this size on ground, moved towards toward
// by the least that reads (the deck's contrast rule, WCAG 2.1).
func Legible(colour, ground, toward string, size float64) string {
	return legible(colour, ground, toward, size)
}

// OnFill is the colour for text of this size on a filled shape: white
// when it reads, else darkest, deepened as far as it must be.
func OnFill(fill, darkest string, size float64) string { return onFill(fill, darkest, size) }

// Mix is colour a moved step of the way to b (0 is a, 1 is b).
func Mix(a, b string, step float64) string { return mix(a, b, step) }

// ContrastRatio is WCAG's ratio between two colours, 1 to 21.
func ContrastRatio(a, b string) float64 { return contrastRatio(a, b) }
