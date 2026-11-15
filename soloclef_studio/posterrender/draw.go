package posterrender

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender"
)

// Drawn is a poster drawn: the SVG resvg draws (the faces found in its font
// directories), the SVG that is kept (the same drawing carrying its faces,
// so it draws exactly in any browser or editor), and its size.
type Drawn struct {
	SVG    string
	Kept   string
	Width  float64
	Height float64
	// Style and Orientation are what it was drawn in.
	Style, Orientation string
}

// Draw lays the poster out on the orientation's canvas in the style and
// draws it. The poster must have been checked (Check). A *Refusal is the
// content's fault, worded for the model; any other error is a fault here.
func Draw(p Poster, styleID, orientation string) (Drawn, error) {
	l, err := lookOf(styleID)
	if err != nil {
		return Drawn{}, err
	}
	c, ok := CanvasNamed(orientation)
	if !ok {
		return Drawn{}, fmt.Errorf("posterrender: %q is not an orientation", orientation)
	}
	figs := map[int]figure{}
	for i, s := range p.Sections {
		if len(s.Figure) == 0 {
			continue
		}
		f, err := drawFigure(s.Figure, l, cardBackground(l))
		if err != nil {
			return Drawn{}, fmt.Errorf("posterrender: sections[%d].figure: %w", i, err)
		}
		figs[i] = f
	}
	out, err := layout(p, c, l, figs)
	if err != nil {
		return Drawn{}, err
	}
	d := &drawing{look: l, faces: map[deckrender.Face]bool{}, figs: figs}
	d.poster(p, c, out)
	svg := d.document(c, p.Title, false)
	return Drawn{SVG: svg, Kept: d.document(c, p.Title, true), Width: c.Width, Height: c.Height, Style: styleID, Orientation: orientation}, nil
}

// cardBackground is the colour a section's panel is filled with.
func cardBackground(l look) string {
	if l.cards == cardFill {
		return l.Tint
	}
	return l.Paper
}

// drawing collects the SVG body and the faces it uses.
type drawing struct {
	look  look
	b     strings.Builder
	faces map[deckrender.Face]bool
	// figs are the sections' figures, drawn once by Draw.
	figs map[int]figure
	// figures counts nested figures, to give each its own ids.
	figures int
}

func (d *drawing) rect(x, y, w, h, r float64, fill, stroke string, strokeW float64) {
	fmt.Fprintf(&d.b, `<rect x="%s" y="%s" width="%s" height="%s"`, num(x), num(y), num(w), num(h))
	if r > 0 {
		fmt.Fprintf(&d.b, ` rx="%s"`, num(r))
	}
	if fill == "" {
		d.b.WriteString(` fill="none"`)
	} else {
		d.b.WriteString(` fill="#` + fill + `"`)
	}
	if stroke != "" {
		fmt.Fprintf(&d.b, ` stroke="#%s" stroke-width="%s"`, stroke, num(strokeW))
	}
	d.b.WriteString("/>")
}

func (d *drawing) circle(cx, cy, r float64, fill string) {
	fmt.Fprintf(&d.b, `<circle cx="%s" cy="%s" r="%s" fill="#%s"/>`, num(cx), num(cy), num(r), fill)
}

func (d *drawing) line(x1, y1, x2, y2 float64, stroke string, w float64) {
	fmt.Fprintf(&d.b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#%s" stroke-width="%s"/>`, num(x1), num(y1), num(x2), num(y2), stroke, num(w))
}

// text draws lines from top y, each anchored at x ("start" or "middle").
func (d *drawing) text(lines []string, f font, x, y float64, fill, anchor string) {
	d.faces[f.face] = true
	weight := "normal"
	if f.face.Bold {
		weight = "bold"
	}
	for i, line := range lines {
		base := y + f.ascent() + float64(i)*f.lineHeight()
		fmt.Fprintf(&d.b, `<text x="%s" y="%s" font-family="%s" font-size="%s" font-weight="%s" fill="#%s"`,
			num(x), num(base), deckrender.SVGFamily(f.face), num(f.size), weight, fill)
		if anchor == "middle" {
			d.b.WriteString(` text-anchor="middle"`)
		}
		d.b.WriteString(">" + escape(line) + "</text>")
	}
}

// poster draws everything at the positions layout chose.
func (d *drawing) poster(p Poster, c Canvas, out laid) {
	l, sc := d.look, out.sc
	inner := c.Width - 2*margin
	d.rect(0, 0, c.Width, c.Height, 0, l.Paper, "", 0)
	y := margin
	// The header.
	switch l.header {
	case headerBand:
		h := out.headerH
		d.rect(margin, y, inner, h, l.Radius, l.Ink, "", 0)
		ink := deckrender.OnFill(l.Ink, l.Paper, out.title.size)
		d.text(out.titleL, out.title, margin+sc.pad, y+sc.pad, ink, "start")
		if len(out.subtitle) > 0 {
			sub := deckrender.Legible(deckrender.Mix(l.Paper, l.Ink, 0.2), l.Ink, l.Paper, sc.subtitle.size)
			d.text(out.subtitle, sc.subtitle, margin+sc.pad, y+sc.pad+out.title.blockHeight(len(out.titleL))+sc.gap*0.4, sub, "start")
		}
		y += h
	default:
		d.text(out.titleL, out.title, margin, y, deckrender.Legible(l.Ink, l.Paper, l.Ink, out.title.size), "start")
		y += out.title.blockHeight(len(out.titleL))
		if len(out.subtitle) > 0 {
			y += sc.gap * 0.4
			d.text(out.subtitle, sc.subtitle, margin, y, deckrender.Legible(l.Muted, l.Paper, l.Ink, sc.subtitle.size), "start")
			y += sc.subtitle.blockHeight(len(out.subtitle))
		}
		y += sc.gap * 0.6
		d.rect(margin, y, 96, ruleHeight, ruleHeight/2, l.Accent, "", 0)
		y += ruleHeight
	}
	y += sc.gap
	// The key numbers.
	if len(p.Numbers) > 0 {
		d.numbers(p.Numbers, sc, margin, y, inner, out.numbersH)
		y += out.numbersH + sc.gap
	}
	// The sections.
	for r, row := range out.rows {
		w := cardWidth(inner, len(row), sc)
		x := margin
		for _, i := range row {
			d.section(p.Sections[i], i, sc, x, y, w, out.rowH[r])
			x += w + sc.gap
		}
		y += out.rowH[r] + sc.gap
	}
	y -= sc.gap
	// The takeaway and the sources, at the foot.
	if p.Takeaway != "" {
		y += sc.gap
		lines, _ := wrap(p.Takeaway, sc.takeaway, inner-2*sc.pad-takeawayBar)
		h := 2*sc.pad*0.8 + sc.takeaway.blockHeight(len(lines))
		d.rect(margin, y, inner, h, l.Radius, l.TintAlt, "", 0)
		d.rect(margin, y, takeawayBar, h, 0, l.AccentAlt, "", 0)
		d.text(lines, sc.takeaway, margin+takeawayBar+sc.pad, y+sc.pad*0.8, deckrender.Legible(l.Ink, l.TintAlt, l.Ink, sc.takeaway.size), "start")
		y += h
	}
	if p.Sources != "" {
		y += sc.gap * 0.6
		lines, _ := wrap(p.Sources, sc.sources, inner)
		d.text(lines, sc.sources, margin, y, deckrender.Legible(l.Muted, l.Paper, l.Ink, sc.sources.size), "start")
	}
}

// numbers draws the key numbers' row in the style's way.
func (d *drawing) numbers(numbers []KeyNumber, sc scale, x, y, inner, h float64) {
	l := d.look
	tileW := (inner - float64(len(numbers)-1)*sc.gap) / float64(len(numbers))
	ground := l.Paper
	switch l.numbers {
	case numbersBand:
		d.rect(x, y, inner, h, l.Radius, l.Accent, "", 0)
		ground = l.Accent
	case numbersAccent:
		ground = l.Accent
	case numbersTiles:
		ground = cardBackground(l)
	}
	valueInk := deckrender.Legible(l.Accent, ground, l.Ink, sc.value.size)
	labelInk := deckrender.Legible(l.Ink, ground, l.Ink, sc.valueLab.size)
	if ground == l.Accent {
		valueInk = deckrender.OnFill(l.Accent, l.Ink, sc.value.size)
		labelInk = deckrender.OnFill(l.Accent, l.Ink, sc.valueLab.size)
	}
	for i, n := range numbers {
		tx := x + float64(i)*(tileW+sc.gap)
		switch l.numbers {
		case numbersTiles:
			d.panel(tx, y, tileW, h)
		case numbersAccent:
			d.rect(tx, y, tileW, h, l.Radius, l.Accent, "", 0)
		case numbersRuled:
			if i > 0 {
				d.line(tx-sc.gap/2, y+sc.pad*0.5, tx-sc.gap/2, y+h-sc.pad*0.5, l.Rule, 1.5)
			}
		}
		d.text([]string{n.Value}, sc.value, tx+sc.pad, y+sc.pad, valueInk, "start")
		lines, _ := wrap(n.Label, sc.valueLab, tileW-2*sc.pad)
		d.text(lines, sc.valueLab, tx+sc.pad, y+sc.pad+sc.value.lineHeight(), labelInk, "start")
	}
}

// panel draws a section's (or a tile's) ground in the style's way.
func (d *drawing) panel(x, y, w, h float64) {
	l := d.look
	switch l.cards {
	case cardFill:
		d.rect(x, y, w, h, l.Radius, l.Tint, "", 0)
	case cardOutline:
		stroke, sw := deckrender.Mix(l.Rule, l.Ink, 0.35), 1.5
		if l.Outline {
			// The whiteboard's marker: the ink itself, a heavier line.
			stroke, sw = l.Ink, 3
		}
		d.rect(x+sw/2, y+sw/2, w-sw, h-sw, l.Radius, l.Paper, stroke, sw)
	case cardRuled:
		d.rect(x, y, w, ruleHeight/2, 0, l.Accent, "", 0)
	}
}

// section draws one section in its box.
func (d *drawing) section(s Section, i int, sc scale, x, y, w, h float64) {
	l := d.look
	_, at, miss := measureSection(s, i, sc, w, l, d.figs)
	if miss != nil {
		// layout measured this very section at this very width.
		panic("posterrender: a section that fitted no longer does: " + miss.path)
	}
	d.panel(x, y, w, h)
	ground := cardBackground(l)
	top := y + sc.pad
	if l.cards == cardRuled {
		top += ruleHeight
	}
	if l.marker != markerNone {
		r := sc.heading.lineHeight() / 2
		cx, cy := x+sc.pad+r, top+r
		d.circle(cx, cy, r*0.92, l.Accent)
		mark := strconv.Itoa(i + 1)
		if l.marker == markerLetter {
			mark = string(rune('A' + i))
		}
		f := font{deckrender.FaceOf(false, true), sc.heading.size * 0.78}
		d.text([]string{mark}, f, cx, cy-f.lineHeight()/2, deckrender.OnFill(l.Accent, l.Ink, f.size), "middle")
	}
	d.text(at.heading, sc.heading, x+sc.pad+at.headingX, top, deckrender.Legible(l.Accent, ground, l.Ink, sc.heading.size), "start")
	cy := y + at.contentTop
	px := x + sc.pad
	ink := deckrender.Legible(l.Ink, ground, l.Ink, sc.point.size)
	py := cy
	for j, lines := range at.points {
		if j > 0 {
			py += sc.point.size * 0.45
		}
		dot := sc.point.size * 0.17
		d.circle(px+dot, py+sc.point.ascent()-sc.point.size*0.3, dot, l.Accent)
		d.text(lines, sc.point, px+bulletIndent(sc.point), py, ink, "start")
		py += sc.point.blockHeight(len(lines))
	}
	if at.fig != nil {
		fw, fh := figUnits*at.figScale, at.fig.h*at.figScale
		var fx, fy float64
		if at.figBeside {
			fx, fy = px+at.pointsW+sc.gap, cy
		} else {
			// Centred under the points.
			fx = x + (w-fw)/2
			fy = py
			if len(at.points) > 0 {
				fy += sc.gap * 0.6
			}
		}
		d.figure(at.fig.svg, fx, fy, fw, fh)
	}
}

var (
	svgOpen = regexp.MustCompile(`^<svg [^>]*>`)
	idAttr  = regexp.MustCompile(`\bid="([^"]+)"`)
	idRef   = regexp.MustCompile(`url\(#([^)]+)\)|href="#([^"]+)"`)
	// fontFamily is a figure's font-family attribute, as figures-go writes
	// it on its root (core.FontFamily: no double quotes inside).
	fontFamily = regexp.MustCompile(`font-family="[^"]*"`)
)

// figure nests a figures-go SVG at x, y, scaled to w by h. Its ids are
// given a prefix of their own: two figures on one poster may each have an
// arrowhead marker called "ff-a", and in one document the second would
// draw with the first's.
func (d *drawing) figure(svg string, x, y, w, h float64) {
	d.figures++
	prefix := "p" + strconv.Itoa(d.figures) + "-"
	svg = idAttr.ReplaceAllString(svg, `id="`+prefix+`$1"`)
	svg = idRef.ReplaceAllStringFunc(svg, func(m string) string {
		if strings.HasPrefix(m, "url(#") {
			return "url(#" + prefix + m[len("url(#"):]
		}
		return `href="#` + prefix + m[len(`href="#`):]
	})
	open := svgOpen.FindString(svg)
	vb := viewBox.FindString(open)
	// The figure's own font family goes with it: its labels were measured
	// in figures-go's sans stack, and without it a browser sets them in its
	// default serif (and resvg in the poster's own face).
	family := fontFamily.FindString(open)
	body := strings.TrimSuffix(strings.TrimPrefix(svg, open), "</svg>")
	fmt.Fprintf(&d.b, `<svg x="%s" y="%s" width="%s" height="%s" %s %s overflow="hidden">%s</svg>`, num(x), num(y), num(w), num(h), vb, family, body)
}

// document is the whole SVG: with the faces it uses embedded (keep) or
// named only (for resvg, which reads them from its font directories).
func (d *drawing) document(c Canvas, title string, keep bool) string {
	var b strings.Builder
	// Grown once: the kept document carries its faces (about 1.7 MB), and
	// growing to that by doubling copies it several times over.
	faces := ""
	if keep {
		faces = d.fontFaces()
	}
	b.Grow(d.b.Len() + len(faces) + 512)
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %s %s" width="%s" height="%s" role="img" aria-label="%s">`,
		num(c.Width), num(c.Height), num(c.Width), num(c.Height), escape(title))
	b.WriteString("<title>" + escape(title) + "</title>")
	b.WriteString(faces)
	b.WriteString(d.b.String())
	b.WriteString("</svg>")
	return b.String()
}

// fontFaces is a <style> with each face the poster uses as a data: URL.
// An SVG opened in a browser or an editor has none of the deck's faces;
// without them its text would be set in another font of other widths and
// run past the panels it was measured for.
func (d *drawing) fontFaces() string {
	faces := make([]deckrender.Face, 0, len(d.faces))
	for f := range d.faces {
		faces = append(faces, f)
	}
	sort.Slice(faces, func(i, j int) bool { return faces[i].File < faces[j].File })
	var b strings.Builder
	b.WriteString("<defs><style>")
	for _, f := range faces {
		b.WriteString(fontFace(f))
	}
	b.WriteString("</style></defs>")
	return b.String()
}

// faceRules are the @font-face rules made so far, by face: a face is the
// same bytes for the life of the process, and encoding the two common ones
// (1.3 MB) for every poster was most of a poster's time and memory.
var faceRules sync.Map

// fontFace is one face's @font-face rule, its file inline.
func fontFace(f deckrender.Face) string {
	if rule, ok := faceRules.Load(f); ok {
		return rule.(string)
	}
	data, err := deckrender.FontFile(f)
	if err != nil {
		// Embedded in the binary: a missing face is a broken build.
		panic("posterrender: " + err.Error())
	}
	weight := "normal"
	if f.Bold {
		weight = "bold"
	}
	rule := fmt.Sprintf("@font-face{font-family:%s;font-weight:%s;font-style:normal;src:url(data:font/ttf;base64,%s) format(\"truetype\")}",
		deckrender.SVGFamily(f), weight, base64.StdEncoding.EncodeToString(data))
	faceRules.Store(f, rule)
	return rule
}
