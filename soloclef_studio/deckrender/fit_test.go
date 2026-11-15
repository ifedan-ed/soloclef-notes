package deckrender

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckspec"
)

// The clinical deck is the one a video failed on (14 Nov 2026, job 29):
// figures whose values are words ("Radiographs", "Bronchodilators"), a
// long institution's name, a rate written as a change ("11.2 → 4.6 per
// 1000"), labels like "Post-intervention", and an address wider than the
// slide. Every one of them must be drawn, at a size a room can read.
func asthma(t testing.TB, narrated bool) deckspec.Deck {
	t.Helper()
	raw, err := os.ReadFile("testdata/asthma.json")
	if err != nil {
		t.Fatal(err)
	}
	deck, err := deckspec.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !narrated {
		for i := range deck.Slides {
			deck.Slides[i].Narration = nil
		}
	}
	deck, err = deckspec.Check(deck, deckspec.Options{MinSlides: 3, MaxSlides: 30, Narrated: narrated})
	if err != nil {
		t.Fatal(err)
	}
	return deck
}

// texts is every text block of a scene, by name.
func texts(scene Scene) map[string]*Text {
	out := map[string]*Text{}
	for _, e := range scene.Elements {
		if t, ok := e.(*Text); ok {
			out[t.Name] = t
		}
	}
	return out
}

// inside checks that every text of every scene is drawn within its box
// and at no less than the least size its kind allows.
func inside(t *testing.T, scenes []Scene) {
	t.Helper()
	for _, scene := range scenes {
		for _, e := range scene.Elements {
			text, ok := e.(*Text)
			if !ok {
				continue
			}
			if text.Fit.height > text.H+0.01 {
				t.Errorf("%s %s: text %.1f pt tall in a box of %.1f", scene.Name, text.Name, text.Fit.height, text.H)
			}
			for _, l := range text.Fit.lines {
				if l.width > text.W+0.01 {
					t.Errorf("%s %s: a line %.1f pt wide in a box of %.1f", scene.Name, text.Name, l.width, text.W)
				}
			}
			if text.Fit.size < 12 {
				t.Errorf("%s %s: drawn at %g pt", scene.Name, text.Name, text.Fit.size)
			}
		}
	}
}

func TestTheClinicalDeckIsDrawnInEveryTheme(t *testing.T) {
	for _, theme := range deckspec.Themes {
		t.Run(theme.ID, func(t *testing.T) {
			deck := asthma(t, false)
			deck.Theme = theme.ID
			scenes, problems := Layout(deck)
			if len(problems) > 0 {
				t.Fatalf("problems: %v", problems)
			}
			inside(t, scenes)
		})
	}
}

// Four figures whose values are long words do not fit four to a row at
// the figure's least size; they are drawn two to a row, never smaller.
func TestFiguresThatAreWordsTakeWiderCards(t *testing.T) {
	scenes, problems := Layout(asthma(t, false))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	four := texts(scenes[1])
	for i := 1; i <= 4; i++ {
		value := four[fmt.Sprintf("Figure %d", i)]
		if value.Fit.size < figureLeast {
			t.Errorf("figure %d drawn at %g pt, under the figures' least size", i, value.Fit.size)
		}
		if len(value.Fit.lines) != 1 {
			t.Errorf("figure %d is %d lines", i, len(value.Fit.lines))
		}
	}
	if w := four["Figure 1"].W; w < 300 {
		t.Errorf("the figures were left four to a row (%.0f pt wide)", w)
	}
	// "Bronchodilators" alone would fit a row of four only far below the
	// least size: the row was tried first and given up for that reason.
	row := figureLayouts(4)[0]
	if _, _, misses := fitCells([][]para{{plain("Bronchodilators")}}, [][2]float64{{row.cards[0].value.w, row.cards[0].value.h}}, sans.boldIfSans(), row.most, figureLeast, false); misses == nil {
		t.Fatal("the word fits a row of four; this test no longer tests the arrangement")
	}
	// Three figures, two of them words: the same.
	three := texts(scenes[2])
	if three["Figure 1"].W < 300 || three["Figure 3"].Fit.size < figureLeast {
		t.Errorf("three figures: %.0f pt wide, the third at %g pt", three["Figure 1"].W, three["Figure 3"].Fit.size)
	}
}

// A timeline whose event text has a word too long for one slot of six
// ("Bronchohyperresponsiveness") takes turns above and below its line
// rather than breaking the word; and across the whole deck, the address
// is the only text broken inside a word, since every other word fits
// whole in some arrangement.
func TestWordsAreBrokenOnlyWhenNoArrangementHoldsThem(t *testing.T) {
	scenes, problems := Layout(asthma(t, false))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	timeline := texts(scenes[5])
	axis := contentTop + contentH/2
	if first, second := timeline["Event 1"], timeline["Event 2"]; first.Y+first.H > axis || second.Y < axis {
		t.Errorf("the events do not take turns: event 1 at %.0f, event 2 at %.0f, the axis at %.0f", first.Y, second.Y, axis)
	}
	for _, scene := range scenes {
		for name, text := range texts(scene) {
			if text.Fit.broken() && !(scene.Name == "Slide 10" && name == "Bullet 2") {
				t.Errorf("%s %s has a word broken", scene.Name, name)
			}
		}
	}
}

// A rate written as a change is a valid figure, drawn on one line, and
// does not count up: it has two numbers and no one of them to raise.
func TestARatioIsAFigureThatDoesNotCount(t *testing.T) {
	scenes, problems := Layout(asthma(t, false))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	ratio := texts(scenes[3])["Figure 1"]
	if got := ratio.Fit.lines[0].pieces[0].text; len(ratio.Fit.lines) != 1 || got != "11.2 → 4.6 per 1000" {
		t.Errorf("the ratio is drawn as %d lines, first %q", len(ratio.Fit.lines), got)
	}
	if ratio.Count {
		t.Error("a value with two numbers counts up")
	}
	if !countable("72%") || !countable("1,200") || countable("33.5% vs 24.1%") || countable("Radiographs") {
		t.Error("countable")
	}
}

// The institution's name, in a comparison's header among three columns,
// in the deck's subtitle and on the title slide, fits as words.
func TestALongInstitutionalNameFits(t *testing.T) {
	scenes, problems := Layout(asthma(t, false))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	header := texts(scenes[4])["Column 1"]
	if header.Fit.size < 16 || header.Fit.broken() {
		t.Errorf("the header is drawn at %g pt, broken %v", header.Fit.size, header.Fit.broken())
	}
	subtitle := texts(scenes[0])["Subtitle"]
	if !strings.Contains(lineText(subtitle.Fit), "NYC Health + Hospitals") {
		t.Errorf("the subtitle reads %q", lineText(subtitle.Fit))
	}
}

func lineText(f fitted) string {
	var parts []string
	for _, l := range f.lines {
		for _, p := range l.pieces {
			parts = append(parts, p.text)
		}
	}
	return strings.Join(parts, " ")
}

// An address wider than the whole slide at the least size is broken where
// Unicode lets a line break inside it, and the deck says so with explicit
// line breaks, so PowerPoint breaks it in the same places.
func TestAWordWiderThanTheSlideIsBrokenWhereALineMayBreak(t *testing.T) {
	deck := asthma(t, false)
	scenes, problems := Layout(deck)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	address := texts(scenes[9])["Bullet 2"]
	if !address.Fit.broken() {
		t.Fatal("the address was not broken")
	}
	for i, l := range address.Fit.lines {
		text := l.pieces[len(l.pieces)-1].text
		if i < len(address.Fit.lines)-1 && !strings.HasSuffix(text, "/") && !strings.HasSuffix(text, "-") {
			t.Errorf("line %d ends %q, not at a break opportunity", i+1, text)
		}
	}
	if joined := strings.ReplaceAll(lineText(address.Fit), " ", ""); joined != deck.Slides[9].Bullets[1] {
		t.Errorf("the broken address reads %q", joined)
	}
	data, err := PPTX(deck.Title, scenes, time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	slide := zipped(t, data, "ppt/slides/slide10.xml")
	if n := strings.Count(slide, "<a:br>"); n != len(address.Fit.lines)-1 {
		t.Errorf("%d line breaks in the deck for %d lines", n, len(address.Fit.lines))
	}
	// Elsewhere the deck leaves wrapping to PowerPoint, as before.
	if strings.Count(zipped(t, data, "ppt/slides/slide2.xml"), "<a:br>") != 0 {
		t.Error("a slide without a broken word has line breaks")
	}
}

func zipped(t *testing.T, data []byte, name string) string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range reader.File {
		if f.Name == name {
			r, _ := f.Open()
			defer r.Close()
			body, _ := io.ReadAll(r)
			return string(body)
		}
	}
	t.Fatalf("no %s in the deck", name)
	return ""
}

func TestBreakOpportunitiesAreUnicodes(t *testing.T) {
	for _, c := range []struct {
		word string
		want []string
	}{
		{"post-bronchodilator", []string{"post-", "bronchodilator"}},
		{"pages 10-12", nil},
		{"10-12", []string{"10-12"}},
		{"and/or", []string{"and/", "or"}},
		{"1/2", []string{"1/2"}},
		{"doi.org/10.1016/j.jpeds", []string{"doi.org/10.1016/", "j.jpeds"}},
		{"2015–2019", []string{"2015–2019"}},
		{"before–after", []string{"before–", "after"}},
		{"-5", []string{"-5"}},
		{"Radiographs", []string{"Radiographs"}},
	} {
		if c.want == nil {
			continue
		}
		var w word
		w.add(c.word, sansRegular, 16)
		var got []string
		for _, part := range w.split(16) {
			got = append(got, part.text())
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%q splits as %q, want %q", c.word, got, c.want)
		}
	}
}

// A word that cannot fit even the widest place the slide has, and has
// nowhere to break, is sent back by name: the slide, the field, the word,
// and the room there is.
func TestAWordThatCannotFitIsNamedWithItsRoom(t *testing.T) {
	deck := asthma(t, false)
	deck.Slides[1].Stats[1].Value = "Electroencephalographies"
	deck.Slides[9].Bullets[1] = strings.Repeat("Pneumonoultramicroscopic", 5)
	_, problems := Layout(deck)
	if len(problems) != 2 {
		t.Fatalf("problems: %v", problems)
	}
	value, bullet := problems[0].String(), problems[1].String()
	for _, want := range []string{`slide 2 (stats) stat 2's value: the word "Electroencephalographies" is too wide`, "at 36 pt", "the line is 333 pt", "of its 24 characters"} {
		if !strings.Contains(value, want) {
			t.Errorf("no %q in %q", want, value)
		}
	}
	for _, want := range []string{"slide 10 (bullets) bullet 2: the word", "at 16 pt", "the line is 804 pt"} {
		if !strings.Contains(bullet, want) {
			t.Errorf("no %q in %q", want, bullet)
		}
	}
}

// Every arrangement of every slide kind with cards keeps its cards apart
// and inside the content area, and every arrow leads from one stage to
// the next: the geometry is checked here, not only looked at.
func TestNoCardsOverlapAndEveryArrowLeads(t *testing.T) {
	type named struct {
		name   string
		cards  []rect
		arrows []arrow
		cycle  bool
	}
	var all []named
	for n := 2; n <= 4; n++ {
		for i, l := range figureLayouts(n) {
			var cards []rect
			for _, c := range l.cards {
				cards = append(cards, c.card)
			}
			all = append(all, named{name: fmt.Sprintf("%d figures, arrangement %d", n, i+1), cards: cards})
		}
	}
	for n := 3; n <= 6; n++ {
		for i, l := range flowLayouts(n) {
			all = append(all, named{name: fmt.Sprintf("flow of %d, arrangement %d", n, i+1), cards: l.cards, arrows: l.arrows})
		}
		for i, l := range cycleLayouts(n) {
			all = append(all, named{name: fmt.Sprintf("cycle of %d, arrangement %d", n, i+1), cards: l.cards, arrows: l.arrows, cycle: true})
		}
		for i, l := range timelineLayouts(n) {
			var boxes []rect
			for _, e := range l.events {
				boxes = append(boxes, e.label, e.text)
			}
			all = append(all, named{name: fmt.Sprintf("timeline of %d, arrangement %d", n, i+1), cards: boxes})
		}
	}
	for _, l := range all {
		for i, a := range l.cards {
			if a.x < marginX-0.01 || a.x+a.w > SlideW-marginX+0.01 || a.y < contentTop-0.01 || a.y+a.h > contentBot+0.01 {
				t.Errorf("%s: card %d (%.0f,%.0f %.0fx%.0f) leaves the content area", l.name, i+1, a.x, a.y, a.w, a.h)
			}
			for j, b := range l.cards[i+1:] {
				if a.x < b.x+b.w-0.01 && b.x < a.x+a.w-0.01 && a.y < b.y+b.h-0.01 && b.y < a.y+a.h-0.01 {
					t.Errorf("%s: cards %d and %d overlap", l.name, i+1, i+j+2)
				}
			}
		}
		for i, a := range l.arrows {
			// Arrow i leads from stage i to the next; a cycle's last
			// leads from the last stage back to the first.
			from, to := i, i+1
			if l.cycle && i == len(l.arrows)-1 {
				from, to = len(l.cards)-1, 0
			}
			c1, c2 := l.cards[from], l.cards[to]
			dx, dy := (c2.x+c2.w/2)-(c1.x+c1.w/2), (c2.y+c2.h/2)-(c1.y+c1.h/2)
			ax, ay := a.x2-a.x1, a.y2-a.y1
			if math.Hypot(ax, ay) < 12 {
				t.Errorf("%s: arrow %d is %.1f pt long", l.name, i+1, math.Hypot(ax, ay))
			}
			if ax*dx+ay*dy <= 0 {
				t.Errorf("%s: arrow %d points away from stage %d", l.name, i+1, to+1)
			}
		}
	}
}

// The cost of laying out the clinical deck, which tries more than one
// arrangement on most of its slides.
func BenchmarkLayOutTheClinicalDeck(b *testing.B) {
	deck := asthma(b, false)
	b.ReportAllocs()
	for b.Loop() {
		if _, problems := Layout(deck); len(problems) > 0 {
			b.Fatal(problems)
		}
	}
}

// The cost of laying out the sample alone (BenchmarkDrawADeck adds the
// .pptx), whose slides fit as designed.
func BenchmarkLayOutTheSample(b *testing.B) {
	deck := sample(b, "")
	b.ReportAllocs()
	for b.Loop() {
		if _, problems := Layout(deck); len(problems) > 0 {
			b.Fatal(problems)
		}
	}
}

// The cost of a slide refused: the widest arrangement tried and failed,
// and the word measured.
func BenchmarkARefusedFigure(b *testing.B) {
	deck := asthma(b, false)
	deck.Slides = deck.Slides[:3]
	deck.Slides[1].Stats[1].Value = "Electroencephalographies"
	b.ReportAllocs()
	for b.Loop() {
		if _, problems := Layout(deck); len(problems) != 1 {
			b.Fatal(problems)
		}
	}
}

// TestClinicalStills draws the clinical deck's slides, and frames of its
// figures coming in, as PNGs for a person to look at (SOLOCLEF_TEST_RESVG
// and SOLOCLEF_TEST_DECK_OUT, as TestStills).
func TestClinicalStills(t *testing.T) {
	binary, out := os.Getenv("SOLOCLEF_TEST_RESVG"), os.Getenv("SOLOCLEF_TEST_DECK_OUT")
	if binary == "" || out == "" {
		t.Skip("SOLOCLEF_TEST_RESVG and SOLOCLEF_TEST_DECK_OUT are not set")
	}
	r, err := NewRasteriser(binary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, theme := range []string{"clinical", "night"} {
		deck := asthma(t, false)
		deck.Theme = theme
		scenes, problems := Layout(deck)
		if len(problems) > 0 {
			t.Fatal(problems)
		}
		dir := filepath.Join(out, "clinical-"+theme)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := PPTX(deck.Title, scenes, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "deck.pptx"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		for i, scene := range scenes {
			if err := r.PNG(context.Background(), scene.Still(), filepath.Join(dir, fmt.Sprintf("slide-%02d.png", i+1))); err != nil {
				t.Fatal(err)
			}
		}
	}
}
