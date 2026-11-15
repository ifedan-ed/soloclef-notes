package deckrender

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// A slide as SVG, at a moment of its build: what a video frame is drawn
// from. resvg rasterises it in the same font files the text was measured
// in, so a frame shows the deck's own lines.
//
// Animation is arithmetic here, not a browser's: each element's progress at
// time t is worked out from its step's start and its motion, and the frame
// is drawn at that progress. That is what lets the video skip a browser
// entirely: a frame costs one SVG string and one resvg call, and only the
// moments when something moves are drawn at all (the rest are held).

// The motions' lengths, in seconds: quick enough not to keep a listener
// waiting, slow enough to be seen as movement, not a flicker.
var motionLength = map[Motion]float64{Rise: 0.6, Fade: 0.5, Pop: 0.45, GrowX: 0.8, Draw: 0.6}

// countLength is how long a number takes to count up.
const countLength = 1.3

// riseDistance is how far, in points, text rises as it comes in.
const riseDistance = 16.0

// Starts is when each build step begins, in seconds from the slide's start.
type Starts []float64

// Settled is the latest moment anything on the slide moves, given its
// steps' starts: after it, the frame no longer changes.
func (sc Scene) Settled(starts Starts) float64 {
	last := 0.0
	for _, e := range sc.Elements {
		r, count := reveal(e)
		end := at(starts, r) + motionLength[r.Motion]
		if count {
			end = math.Max(end, at(starts, r)+countLength)
		}
		last = math.Max(last, end)
	}
	return last
}

// Moving reports the spans of time in which something on the slide is
// moving: one per element, from its start to its end. The video draws a
// frame for every frame time inside one, and holds the frame between them.
func (sc Scene) Moving(starts Starts) [][2]float64 {
	var spans [][2]float64
	for _, e := range sc.Elements {
		r, count := reveal(e)
		begin := at(starts, r)
		end := begin + motionLength[r.Motion]
		if count {
			end = math.Max(end, begin+countLength)
		}
		spans = append(spans, [2]float64{begin, end})
	}
	return spans
}

func reveal(e any) (Reveal, bool) {
	switch v := e.(type) {
	case *Box:
		return v.Reveal, false
	case *Stroke:
		return v.Reveal, false
	case *Text:
		return v.Reveal, v.Count
	}
	return Reveal{}, false
}

// at is when an element starts to come in. A step past the starts given
// (a slide narrated in fewer passages than it has steps is given every
// step's start by its caller) comes in at the last start.
func at(starts Starts, r Reveal) float64 {
	if len(starts) == 0 {
		return r.Delay
	}
	step := min(r.Step, len(starts)-1)
	return starts[step] + r.Delay
}

// easeOut is the cubic ease every motion uses: fast in, settling gently.
func easeOut(p float64) float64 { return 1 - math.Pow(1-p, 3) }

func progress(t, begin, length float64) float64 {
	if length <= 0 {
		if t >= begin {
			return 1
		}
		return 0
	}
	return math.Max(0, math.Min(1, (t-begin)/length))
}

// Still is the slide with everything in: the deck's picture of it.
func (sc Scene) Still() string { return sc.Frame(nil, math.Inf(1)) }

// Frame is the slide at t seconds into its build, starts giving each
// step's beginning; an SVG document at 1920 by 1080 pixels.
func (sc Scene) Frame(starts Starts, t float64) string {
	var b strings.Builder
	b.Grow(8 << 10)
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="1920" height="1080" viewBox="0 0 %g %g">`, SlideW, SlideH)
	fmt.Fprintf(&b, `<rect width="%g" height="%g" fill="#%s"/>`, SlideW, SlideH, sc.Background)
	for _, e := range sc.Elements {
		r, count := reveal(e)
		begin := at(starts, r)
		p := progress(t, begin, motionLength[r.Motion])
		if p <= 0 {
			continue
		}
		eased := easeOut(p)
		switch v := e.(type) {
		case *Box:
			drawBox(&b, v, eased)
		case *Stroke:
			drawStroke(&b, v, eased)
		case *Text:
			counted := 1.0
			if count {
				counted = easeOut(progress(t, begin, countLength))
			}
			drawText(&b, v, eased, counted)
		}
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// open starts a group drawn at progress p of motion m about the box
// (x, y, w, h).
func open(b *strings.Builder, m Motion, p, x, y, w, h float64) {
	opacity := p
	transform := ""
	switch m {
	case Rise:
		transform = fmt.Sprintf(` transform="translate(0 %.2f)"`, (1-p)*riseDistance)
	case Pop:
		scale := 0.6 + 0.4*p
		cx, cy := x+w/2, y+h/2
		transform = fmt.Sprintf(` transform="translate(%.2f %.2f) scale(%.4f) translate(%.2f %.2f)"`, cx, cy, scale, -cx, -cy)
	case GrowX, Draw:
		opacity = math.Min(1, p*3)
	}
	fmt.Fprintf(b, `<g opacity="%.3f"%s>`, opacity, transform)
}

func drawBox(b *strings.Builder, v *Box, p float64) {
	w := v.W
	if v.Reveal.Motion == GrowX {
		w = v.W * p
	}
	open(b, v.Reveal.Motion, p, v.X, v.Y, v.W, v.H)
	style := fillStyle(v.Fill)
	if v.Line != "" {
		style += fmt.Sprintf(` stroke="#%s" stroke-width="%g"`, v.Line, v.LineW)
	}
	switch v.Preset {
	case "ellipse":
		fmt.Fprintf(b, `<ellipse cx="%.2f" cy="%.2f" rx="%.2f" ry="%.2f"%s/>`, v.X+w/2, v.Y+v.H/2, w/2, v.H/2, style)
	case "roundRect":
		r := math.Min(v.Radius, math.Min(w, v.H)/2)
		fmt.Fprintf(b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="%.2f"%s/>`, v.X, v.Y, w, v.H, r, style)
	default:
		fmt.Fprintf(b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f"%s/>`, v.X, v.Y, w, v.H, style)
	}
	b.WriteString(`</g>`)
}

func fillStyle(fill string) string {
	if fill == "" {
		return ` fill="none"`
	}
	return ` fill="#` + fill + `"`
}

// drawStroke draws a line from its start towards its end as far as p, and
// its arrowhead once the line has arrived. The head is the size
// PowerPoint's medium arrowhead is for this width, its tip on the end.
func drawStroke(b *strings.Builder, v *Stroke, p float64) {
	open(b, v.Reveal.Motion, p, 0, 0, 0, 0)
	dx, dy := v.X2-v.X1, v.Y2-v.Y1
	length := math.Hypot(dx, dy)
	if length == 0 {
		b.WriteString(`</g>`)
		return
	}
	ux, uy := dx/length, dy/length
	head := 0.0
	if v.Arrow {
		head = 3 * v.Width
	}
	drawn := (length - head) * p
	if v.Reveal.Motion != Draw {
		drawn = length - head
	}
	fmt.Fprintf(b, `<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" stroke="#%s" stroke-width="%g" stroke-linecap="round"/>`,
		v.X1, v.Y1, v.X1+ux*drawn, v.Y1+uy*drawn, v.Color, v.Width)
	if v.Arrow && p >= 0.98 {
		tip := [2]float64{v.X2, v.Y2}
		base := [2]float64{v.X2 - ux*head*1.2, v.Y2 - uy*head*1.2}
		half := 1.5 * v.Width
		fmt.Fprintf(b, `<polygon points="%.2f,%.2f %.2f,%.2f %.2f,%.2f" fill="#%s"/>`,
			tip[0], tip[1], base[0]-uy*half, base[1]+ux*half, base[0]+uy*half, base[1]-ux*half, v.Color)
	}
	b.WriteString(`</g>`)
}

// drawText draws a fitted block's lines where the deck's text box puts
// them: the block anchored in its box, each line aligned, each piece at its
// measured offset. counted is how far a counting number has risen.
func drawText(b *strings.Builder, v *Text, p, counted float64) {
	f := v.Fit
	top := v.Y
	switch v.Anchor {
	case "ctr":
		top = v.Y + (v.H-f.height)/2
	case "b":
		top = v.Y + v.H - f.height
	}
	open(b, v.Reveal.Motion, p, v.X, v.Y, v.W, v.H)
	y := top + f.ascent
	for i, l := range f.lines {
		if i > 0 {
			y += f.lineHeight
			if l.first {
				y += v.Paras[l.para].gap * f.lineHeight
			}
		}
		pieces := l.pieces
		width := l.width
		if v.Count && counted < 1 && len(pieces) == 1 {
			text := countTo(pieces[0].text, counted)
			pieces = []piece{{text: text, face: pieces[0].face}}
			width = widthOf(text, pieces[0].face, f.size)
		}
		x := v.X
		switch v.Align {
		case "ctr":
			x = v.X + (v.W-width)/2
		case "r":
			x = v.X + v.W - width
		}
		fmt.Fprintf(b, `<text y="%.2f" font-size="%g" fill="#%s">`, y, f.size, v.Color)
		for _, pc := range pieces {
			fmt.Fprintf(b, `<tspan x="%.2f" font-family="%s" font-weight="%s" font-style="%s">%s</tspan>`,
				x+pc.x, svgFamily(pc.face), weight(pc.face), style(pc.face), escapeSVG(pc.text))
		}
		b.WriteString(`</text>`)
	}
	b.WriteString(`</g>`)
}

func widthOf(text string, face Face, size float64) float64 { return width(face, text, size) }

// svgFamily is the family a rasteriser finds in the font files: the open
// twin's own name.
func svgFamily(f Face) string {
	if f.Family == "Cambria" {
		return "Caladea"
	}
	return "Carlito"
}

func weight(f Face) string {
	if f.Bold {
		return "bold"
	}
	return "normal"
}

func style(f Face) string {
	if f.Italic {
		return "italic"
	}
	return "normal"
}

var svgEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escapeSVG(s string) string { return svgEscaper.Replace(xmlSafeText(s)) }

// xmlSafeText drops what XML cannot hold (deckspec refuses control
// characters; this keeps a missed one from breaking a frame).
func xmlSafeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return -1
		}
		return r
	}, s)
}

// countable reports whether a figure's value can count up as it comes
// in: when it has exactly one number. A value with two ("11.2 → 4.6 per
// 1000") has no one number to rise, and counting its first (or, wrapped
// over lines, each line's) would show figures the source never gave.
func countable(text string) bool {
	numbers, in := 0, false
	for _, r := range text {
		digit := r >= '0' && r <= '9'
		if digit && !in {
			numbers++
		}
		in = digit || (in && (r == ',' || r == '.'))
	}
	return numbers == 1
}

// countTo is a figure on its way up: the number inside the text scaled by
// share, written with the figure's own decimals and grouping, its prefix
// and suffix kept ("$1.2 million" counts as "$0.6 million"). Text with no
// number in it is shown as it is.
func countTo(text string, share float64) string {
	start, end := -1, -1
	for i, r := range text {
		digit := r >= '0' && r <= '9'
		if start < 0 && digit {
			start = i
		}
		if start >= 0 {
			if digit || r == ',' || r == '.' {
				end = i + 1
			} else {
				break
			}
		}
	}
	if start < 0 {
		return text
	}
	number := strings.TrimRight(text[start:end], ".,")
	end = start + len(number)
	grouped := strings.Contains(number, ",")
	plainNumber := strings.ReplaceAll(number, ",", "")
	decimals := 0
	if dot := strings.IndexByte(plainNumber, '.'); dot >= 0 {
		decimals = len(plainNumber) - dot - 1
	}
	value, err := strconv.ParseFloat(plainNumber, 64)
	if err != nil {
		return text
	}
	written := strconv.FormatFloat(value*share, 'f', decimals, 64)
	if grouped {
		whole, frac, _ := strings.Cut(written, ".")
		var g strings.Builder
		for i, r := range whole {
			if i > 0 && (len(whole)-i)%3 == 0 {
				g.WriteByte(',')
			}
			g.WriteRune(r)
		}
		written = g.String()
		if frac != "" {
			written += "." + frac
		}
	}
	return text[:start] + written + text[end:]
}
