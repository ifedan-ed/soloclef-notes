package pptx

import (
	"fmt"
	"strings"
)

// Preset geometries the renderer draws.
const (
	Rect      = "rect"
	RoundRect = "roundRect"
	Ellipse   = "ellipse"
	Chevron   = "chevron"
)

// Shape is a filled or outlined preset shape, with no text of its own: the
// renderer puts text in a text box over it, so the text's box is exactly
// what was measured, with no shape's own insets in between.
type Shape struct {
	Name string
	// X, Y, W, H are in EMU.
	X, Y, W, H int64
	Preset     string
	// Fill and Line are six hex digits, "" for none. LineW is in EMU.
	Fill, Line string
	LineW      int64
	// Adjust is a round rectangle's corner, as a share of the shorter
	// side (0 to 0.5); 0 leaves PowerPoint's default.
	Adjust float64
	id     int
}

// AddShape adds a shape.
func (s *Slide) AddShape(shape Shape) {
	shape.id = s.take()
	s.elements = append(s.elements, &shape)
}

func (sh *Shape) write(b *strings.Builder) {
	fmt.Fprintf(b, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr>`, sh.id, escapeAttr(sh.Name))
	fmt.Fprintf(b, `<a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="%s">`, sh.X, sh.Y, sh.W, sh.H, sh.Preset)
	if sh.Adjust > 0 && sh.Preset == RoundRect {
		fmt.Fprintf(b, `<a:avLst><a:gd name="adj" fmla="val %d"/></a:avLst>`, int64(sh.Adjust*100000))
	} else {
		b.WriteString(`<a:avLst/>`)
	}
	b.WriteString(`</a:prstGeom>`)
	writeFill(b, sh.Fill)
	writeLine(b, sh.Line, sh.LineW, false)
	// No theme style at all: python-pptx's autoshape style refers to the
	// theme's shadow, which LibreOffice draws under every card.
	b.WriteString(`<a:effectLst/></p:spPr><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>`)
}

func writeFill(b *strings.Builder, hex string) {
	if hex == "" {
		b.WriteString(`<a:noFill/>`)
		return
	}
	b.WriteString(`<a:solidFill><a:srgbClr val="` + hex + `"/></a:solidFill>`)
}

func writeLine(b *strings.Builder, hex string, width int64, arrow bool) {
	if hex == "" {
		b.WriteString(`<a:ln><a:noFill/></a:ln>`)
		return
	}
	fmt.Fprintf(b, `<a:ln w="%d"><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:round/>`, width, hex)
	if arrow {
		b.WriteString(`<a:tailEnd type="triangle" w="med" len="med"/>`)
	}
	b.WriteString(`</a:ln>`)
}

// Line is a straight line from (X1, Y1) to (X2, Y2), in EMU.
type Line struct {
	Name           string
	X1, Y1, X2, Y2 int64
	Color          string
	Width          int64
	// Arrow puts an arrowhead at (X2, Y2).
	Arrow bool
	id    int
}

// AddLine adds a line.
func (s *Slide) AddLine(line Line) {
	line.id = s.take()
	s.elements = append(s.elements, &line)
}

func (l *Line) write(b *strings.Builder) {
	x, y := min(l.X1, l.X2), min(l.Y1, l.Y2)
	w, h := abs(l.X2-l.X1), abs(l.Y2-l.Y1)
	fmt.Fprintf(b, `<p:cxnSp><p:nvCxnSpPr><p:cNvPr id="%d" name="%s"/><p:cNvCxnSpPr/><p:nvPr/></p:nvCxnSpPr><p:spPr><a:xfrm`, l.id, escapeAttr(l.Name))
	if l.X1 > l.X2 {
		b.WriteString(` flipH="1"`)
	}
	if l.Y1 > l.Y2 {
		b.WriteString(` flipV="1"`)
	}
	fmt.Fprintf(b, `><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="line"><a:avLst/></a:prstGeom>`, x, y, w, h)
	writeLine(b, l.Color, l.Width, l.Arrow)
	b.WriteString(`<a:effectLst/></p:spPr></p:cxnSp>`)
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
