package posterrender

import (
	"encoding/xml"
	"strconv"
	"strings"
	"unicode"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender"
)

// font is a face at a size, measured with the deck's own metrics.
type font struct {
	face deckrender.Face
	size float64
}

// leading is the space between baselines as a share of a single-spaced
// line: a poster is read at a distance and in blocks, so it is set a
// little looser than a slide's single spacing.
const leading = 1.16

func (f font) width(s string) float64 { return deckrender.TextWidth(f.face, s, f.size) }

// lineHeight is the distance from one baseline to the next.
func (f font) lineHeight() float64 {
	line, _ := deckrender.FaceMetrics(f.face)
	return line * f.size * leading
}

// ascent is the baseline's distance below a line box's top, with the
// leading's extra split above and below the line.
func (f font) ascent() float64 {
	line, ascent := deckrender.FaceMetrics(f.face)
	return ascent*f.size + (leading-1)*line*f.size/2
}

// blockHeight is n lines' height.
func (f font) blockHeight(n int) float64 { return float64(n) * f.lineHeight() }

// wrap breaks text into lines no wider than w, greedily, at spaces. A word
// wider than w on its own is returned as tooWide (the first such), and the
// lines are then not to be used: a poster never breaks a word, and never
// draws one past its column.
func wrap(text string, f font, w float64) (lines []string, tooWide string) {
	words := strings.Fields(text)
	space := f.width(" ")
	var line strings.Builder
	lineW := 0.0
	for _, word := range words {
		ww := f.width(word)
		if ww > w {
			return nil, word
		}
		switch {
		case line.Len() == 0:
			line.WriteString(word)
			lineW = ww
		case lineW+space+ww <= w:
			line.WriteByte(' ')
			line.WriteString(word)
			lineW += space + ww
		default:
			lines = append(lines, line.String())
			line.Reset()
			line.WriteString(word)
			lineW = ww
		}
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines, ""
}

// num writes a coordinate with at most two decimals: shorter SVG, and no
// position finer than a hundredth of a pixel means anything.
func num(v float64) string {
	return strconv.FormatFloat(float64(int64(v*100+sign(v)*0.5))/100, 'f', -1, 64)
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

// escape is text as SVG character data or an attribute value, without
// what XML cannot hold (Check refuses control characters; this keeps a
// missed one from breaking the document).
func escape(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
