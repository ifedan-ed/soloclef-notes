package deckrender

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// The fonts every slide is measured and drawn in. A deck names Calibri and
// Cambria, which PowerPoint and Google Slides have everywhere; Carlito and
// Caladea are their metric-compatible open twins (SIL OFL 1.1, the
// copyright files beside them), which LibreOffice substitutes for them by
// name. So one measurement holds for all three: text sized here to its box
// fits that box in PowerPoint, in LibreOffice's preview, and in a video
// frame resvg draws from these very files.
//
//go:embed fonts/*.ttf
var fontFiles embed.FS

// Face is one family and style.
type Face struct {
	// Family is the name a deck's run names (what PowerPoint looks up);
	// File is the open twin measured and drawn here.
	Family string
	File   string
	Bold   bool
	Italic bool
}

// The faces.
var (
	sansRegular  = Face{"Calibri", "Carlito-Regular.ttf", false, false}
	sansBold     = Face{"Calibri", "Carlito-Bold.ttf", true, false}
	sansItalic   = Face{"Calibri", "Carlito-Italic.ttf", false, true}
	serifRegular = Face{"Cambria", "Caladea-Regular.ttf", false, false}
	serifBold    = Face{"Cambria", "Caladea-Bold.ttf", true, false}
	serifItalic  = Face{"Cambria", "Caladea-Italic.ttf", false, true}
)

// family is a regular, a bold and an italic of one typeface.
type family struct{ regular, bold, italic Face }

var (
	sans  = family{sansRegular, sansBold, sansItalic}
	serif = family{serifRegular, serifBold, serifItalic}
)

func (f family) style(bold, italic bool) Face {
	switch {
	case bold:
		return f.bold
	case italic:
		return f.italic
	}
	return f.regular
}

// metrics is what layout needs of a face, in ems: each character's
// advance, the line height, and where the baseline sits in a line.
type metrics struct {
	font *sfnt.Font
	// upem is the font's units per em.
	upem float64
	// ascent is the baseline's distance below a line's top, and line the
	// distance between baselines, both as LibreOffice and PowerPoint set a
	// single-spaced line of this font (its hhea ascender plus descender:
	// neither adds the line gap, which is zero in both families anyway).
	ascent, line float64
	mu           sync.Mutex
	advances     map[rune]float64
	buffer       sfnt.Buffer
}

var (
	loaded   = map[string]*metrics{}
	loadedMu sync.Mutex
)

// metricsOf loads a face once per process.
func metricsOf(face Face) *metrics {
	loadedMu.Lock()
	defer loadedMu.Unlock()
	if m, ok := loaded[face.File]; ok {
		return m
	}
	data, err := fontFiles.ReadFile("fonts/" + face.File)
	if err != nil {
		// The files are embedded: a missing one is a broken build.
		panic(fmt.Sprintf("deckrender: embedded font %s: %v", face.File, err))
	}
	f, err := sfnt.Parse(data)
	if err != nil {
		panic(fmt.Sprintf("deckrender: embedded font %s: %v", face.File, err))
	}
	m := &metrics{font: f, upem: float64(f.UnitsPerEm()), advances: map[rune]float64{}}
	ppem := fixed.Int26_6(f.UnitsPerEm()) << 6
	fm, err := f.Metrics(&m.buffer, ppem, font.HintingNone)
	if err != nil {
		panic(fmt.Sprintf("deckrender: embedded font %s metrics: %v", face.File, err))
	}
	m.ascent = float64(fm.Ascent) / 64 / m.upem
	m.line = float64(fm.Ascent+fm.Descent) / 64 / m.upem
	loaded[face.File] = m
	return m
}

// advance is a character's width in ems. A character the font lacks is
// counted as a full em: wider than any Latin letter, so text in a script
// the face does not cover is given room rather than overflowing (the
// viewer's own font for that script draws it).
func (m *metrics) advance(r rune) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.advances[r]; ok {
		return w
	}
	w := 1.0
	if index, err := m.font.GlyphIndex(&m.buffer, r); err == nil && index != 0 {
		if a, err := m.font.GlyphAdvance(&m.buffer, index, fixed.Int26_6(m.font.UnitsPerEm())<<6, font.HintingNone); err == nil {
			w = float64(a) / 64 / m.upem
		}
	}
	m.advances[r] = w
	return w
}

// width is a string's width in points at size.
func width(face Face, text string, size float64) float64 {
	m := metricsOf(face)
	total := 0.0
	for _, r := range text {
		total += m.advance(r)
	}
	return total * size
}

// WriteFonts writes the font files into dir, for a rasteriser that reads
// fonts from a directory (resvg's --use-fonts-dir). It is how the video
// draws its frames in exactly the faces the layout measured.
func WriteFonts(dir string) error {
	entries, err := fontFiles.ReadDir("fonts")
	if err != nil {
		return err
	}
	for _, e := range entries {
		data, err := fontFiles.ReadFile("fonts/" + e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
