package pptx

import (
	"fmt"
	"strings"
)

// Alignments and anchors as DrawingML spells them.
const (
	AlignLeft   = "l"
	AlignCenter = "ctr"
	AlignRight  = "r"

	AnchorTop    = "t"
	AnchorMiddle = "ctr"
	AnchorBottom = "b"
)

// Run is a stretch of text in one style.
type Run struct {
	Text string
	// Size is in points.
	Size         float64
	Bold, Italic bool
	Color        string
	Font         string
	// Break makes the run a line break (DrawingML's a:br, styled as the
	// run) rather than text: where the layout broke a word too wide for
	// any line, which PowerPoint would not break at that place itself.
	Break bool
}

// Paragraph is one paragraph of a text box.
type Paragraph struct {
	Align string
	// LineSpacing is a multiple of the font's own line height (1 for
	// single); 0 leaves it single.
	LineSpacing float64
	// SpaceBefore is in points.
	SpaceBefore float64
	Runs        []Run
}

// TextBox is text in a box of fixed size: no insets, wrapping at the box's
// width, never grown or shrunk by the application. The renderer has
// already sized the text to fit, measured in the same font, so what
// PowerPoint or LibreOffice draws is what was measured; a box that grew or
// an autofit that shrank would undo that.
type TextBox struct {
	Name       string
	X, Y, W, H int64
	Anchor     string
	Paragraphs []Paragraph
	id         int
}

// AddText adds a text box.
func (s *Slide) AddText(box TextBox) {
	box.id = s.take()
	s.elements = append(s.elements, &box)
}

func (t *TextBox) write(b *strings.Builder) {
	fmt.Fprintf(b, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr>`, t.id, escapeAttr(t.Name))
	fmt.Fprintf(b, `<a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/></p:spPr>`, t.X, t.Y, t.W, t.H)
	anchor := t.Anchor
	if anchor == "" {
		anchor = AnchorTop
	}
	fmt.Fprintf(b, `<p:txBody><a:bodyPr wrap="square" lIns="0" tIns="0" rIns="0" bIns="0" anchor="%s" rtlCol="0"><a:noAutofit/></a:bodyPr><a:lstStyle/>`, anchor)
	for _, p := range t.Paragraphs {
		b.WriteString(`<a:p><a:pPr`)
		if p.Align != "" {
			b.WriteString(` algn="` + p.Align + `"`)
		}
		b.WriteString(`>`)
		if p.LineSpacing > 0 {
			fmt.Fprintf(b, `<a:lnSpc><a:spcPct val="%d"/></a:lnSpc>`, int64(p.LineSpacing*100000+0.5))
		}
		fmt.Fprintf(b, `<a:spcBef><a:spcPts val="%d"/></a:spcBef><a:buNone/></a:pPr>`, int64(p.SpaceBefore*100+0.5))
		for _, r := range p.Runs {
			element := "r"
			if r.Break {
				element = "br"
			}
			fmt.Fprintf(b, `<a:%s><a:rPr`, element)
			fmt.Fprintf(b, ` lang="en-US" sz="%d" b="%s" i="%s" dirty="0">`, int64(r.Size*100+0.5), flag(r.Bold), flag(r.Italic))
			if r.Color != "" {
				b.WriteString(`<a:solidFill><a:srgbClr val="` + r.Color + `"/></a:solidFill>`)
			}
			if r.Font != "" {
				font := escapeAttr(r.Font)
				b.WriteString(`<a:latin typeface="` + font + `"/><a:cs typeface="` + font + `"/>`)
			}
			if r.Break {
				b.WriteString(`</a:rPr></a:br>`)
				continue
			}
			b.WriteString(`</a:rPr><a:t>` + escapeText(r.Text) + `</a:t></a:r>`)
		}
		b.WriteString(`</a:p>`)
	}
	b.WriteString(`</p:txBody></p:sp>`)
}

func flag(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
