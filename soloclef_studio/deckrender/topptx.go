package deckrender

import (
	"math"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender/pptx"
)

// PPTX writes laid-out slides as a .pptx: every scene's shapes, lines and
// text boxes as native, editable PowerPoint shapes, with the speaker notes
// and the theme's transition. created is the file's creation time.
func PPTX(title string, scenes []Scene, created time.Time) ([]byte, error) {
	deck := pptx.New(title, created)
	for _, scene := range scenes {
		slide := deck.AddSlide()
		slide.SetName(scene.Name)
		slide.SetBackground(scene.Background)
		slide.SetTransition(scene.Transition)
		slide.SetNotes(scene.Notes, deck)
		for _, element := range scene.Elements {
			switch e := element.(type) {
			case *Box:
				shape := pptx.Shape{Name: e.Name, X: emu(e.X), Y: emu(e.Y), W: emu(e.W), H: emu(e.H), Preset: e.Preset, Fill: e.Fill, Line: e.Line, LineW: emu(e.LineW)}
				if e.Preset == "roundRect" && e.Radius > 0 {
					shape.Adjust = math.Min(0.5, e.Radius/math.Min(e.W, e.H))
				}
				slide.AddShape(shape)
			case *Stroke:
				slide.AddLine(pptx.Line{Name: e.Name, X1: emu(e.X1), Y1: emu(e.Y1), X2: emu(e.X2), Y2: emu(e.Y2), Color: e.Color, Width: emu(e.Width), Arrow: e.Arrow})
			case *Text:
				slide.AddText(textBox(e))
			}
		}
	}
	return deck.Bytes()
}

// textBox is a fitted block as a text box: its paragraphs at the fitted
// size, so PowerPoint wraps them where they were measured to wrap. A block
// in which a word was broken (a word too wide for any line) is written as
// its measured lines instead, with a line break where the word was
// broken, since PowerPoint would push the whole word to a line of its own
// and overflow there.
func textBox(t *Text) pptx.TextBox {
	box := pptx.TextBox{Name: t.Name, X: emu(t.X), Y: emu(t.Y), W: emu(t.W), H: emu(t.H), Anchor: t.Anchor}
	broken := t.Fit.broken()
	for i, p := range t.Paras {
		paragraph := pptx.Paragraph{Align: t.Align, SpaceBefore: p.gap * t.Fit.lineHeight}
		if t.Spacing != 1 {
			paragraph.LineSpacing = t.Spacing
		}
		if i == 0 {
			paragraph.SpaceBefore = 0
		}
		run := func(text string, face Face) pptx.Run {
			return pptx.Run{Text: text, Size: t.Fit.size, Bold: face.Bold, Italic: face.Italic, Color: t.Color, Font: face.Family}
		}
		if broken {
			paragraph.Runs = linesAsRuns(t.Fit.lines, i, run)
		} else {
			for _, s := range p.spans {
				paragraph.Runs = append(paragraph.Runs, run(s.text, t.Family.style(s.bold, s.italic)))
			}
		}
		box.Paragraphs = append(box.Paragraphs, paragraph)
	}
	return box
}

// linesAsRuns is paragraph para's measured lines as runs: a space between
// lines that were wrapped at a space (PowerPoint wraps there itself), a
// line break between the two parts of a broken word.
func linesAsRuns(lines []line, para int, run func(string, Face) pptx.Run) []pptx.Run {
	var runs []pptx.Run
	for _, l := range lines {
		if l.para != para {
			continue
		}
		if len(runs) > 0 {
			if l.joined {
				brk := runs[len(runs)-1]
				brk.Text, brk.Break = "", true
				runs = append(runs, brk)
			} else {
				runs[len(runs)-1].Text += " "
			}
		}
		for _, pc := range l.pieces {
			runs = append(runs, run(pc.text, pc.face))
		}
	}
	return runs
}

func emu(points float64) int64 { return int64(math.Round(points * pptx.EmuPerPt)) }
