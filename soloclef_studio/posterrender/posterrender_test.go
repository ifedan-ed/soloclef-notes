package posterrender

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixture is the asthma poster in testdata, checked.
func fixture(t testing.TB) Poster {
	t.Helper()
	raw, err := os.ReadFile("testdata/asthma.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	p.Title = "Asthma in children"
	p, err = Check(p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// forCanvas is the fixture as it fits c: a square page holds less, so its
// poster has one figure, two key numbers and no takeaway.
func forCanvas(t testing.TB, c Canvas) Poster {
	p := fixture(t)
	if c.ID == "square" {
		p.Sections[2].Figure = nil
		p.Numbers = p.Numbers[:2]
		p.Takeaway = ""
	}
	return p
}

// Every style draws the poster on every canvas, with its texts and its
// figures nested as SVGs of their own.
func TestEveryStyleOnEveryCanvas(t *testing.T) {
	for _, style := range Styles() {
		for _, c := range Canvases {
			p := forCanvas(t, c)
			d, err := Draw(p, style, c.ID)
			if err != nil {
				t.Fatalf("%s %s: %v", style, c.ID, err)
			}
			if want, got := countFigures(p), strings.Count(d.SVG, "<svg x="); got != want {
				t.Errorf("%s %s: %d figures nested, want %d", style, c.ID, got, want)
			}
			// Each nested figure keeps the font its labels were measured in.
			if want, got := countFigures(p), strings.Count(d.SVG, `overflow="hidden"`); got != want || strings.Count(d.SVG, `font-family="system-ui`) != want {
				t.Errorf("%s %s: the nested figures lost their font family", style, c.ID)
			}
			for _, want := range []string{"Asthma in children", "1 in 11", "Keep it controlled", "Reliever use and control"} {
				if !strings.Contains(d.SVG, want) && !strings.Contains(d.SVG, escape(want)) {
					t.Errorf("%s %s: the drawing lacks %q", style, c.ID, want)
				}
			}
			if strings.Contains(d.SVG, "@font-face") || !strings.Contains(d.Kept, "@font-face{font-family:Carlito") {
				t.Errorf("%s %s: the faces belong in the kept SVG only", style, c.ID)
			}
		}
	}
}

// A poster's limits are told in words and refused with where, how long and
// the start of the text; nothing is cut.
func TestRefusalsNameTheItem(t *testing.T) {
	long := strings.Repeat("wheeze ", 30)
	cases := map[string]struct {
		edit func(*Poster)
		want []string
	}{
		"a long point":  {func(p *Poster) { p.Sections[0].Points[1] = long }, []string{"sections[0].points[1]", "is 30 words; keep it to 20 or fewer", `"wheeze wheeze`}},
		"one section":   {func(p *Poster) { p.Sections = p.Sections[:1] }, []string{"sections: has 1; a poster has 2 to 6 sections"}},
		"an empty one":  {func(p *Poster) { p.Sections[1].Points = nil }, []string{"sections[1]: has neither points nor a figure"}},
		"a word number": {func(p *Poster) { p.Numbers[0].Value = "many" }, []string{"key_numbers[0].value: has no number in it"}},
		"markup":        {func(p *Poster) { p.Takeaway = "**Review** the preventer" }, []string{"takeaway: has markup"}},
		"a bad figure": {func(p *Poster) {
			p.Sections[3].Figure = []byte(`{"type":"bar_chart","title":"x","alt":"y","spec":{"categories":["a"],"series":[{"values":[1,2]}]}}`)
		},
			[]string{"sections[3].figure:", "values"}},
		"a browser figure": {func(p *Poster) {
			p.Sections[3].Figure = []byte(`{"type":"mermaid","title":"x","alt":"y","spec":{"mermaid":"graph TD\nA-->B"}}`)
		},
			[]string{"sections[3].figure:", `type "mermaid" is not drawn here`}},
	}
	for name, c := range cases {
		raw, _ := os.ReadFile("testdata/asthma.json")
		p, _ := Decode(raw)
		p.Title = "Asthma in children"
		c.edit(&p)
		_, err := Check(p)
		var r *Refusal
		if !errors.As(err, &r) {
			t.Fatalf("%s: %v, not a refusal", name, err)
		}
		for _, want := range c.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %q lacks %q", name, err, want)
			}
		}
	}
	if _, err := Decode([]byte(`{"sections":[],"colour":"red"}`)); err == nil || !strings.Contains(err.Error(), `unknown field "colour"`) {
		t.Errorf("an unknown field: %v", err)
	}
}

// A poster that is too full is first set smaller and arranged otherwise;
// only past the floor is it refused, naming the tallest section and its
// longest point.
func TestTooMuchIsRefusedWithTheTallestSection(t *testing.T) {
	p := fixture(t)
	withFigures := p
	withFigures.Sections = append([]Section(nil), p.Sections...)
	p.Sections[2].Figure, p.Sections[3].Figure = nil, nil
	point := "Salbutamol by spacer, two to ten puffs, repeated every twenty to thirty minutes as needed"
	for len(p.Sections) < MaxSections {
		p.Sections = append(p.Sections, Section{Heading: "More", Points: []string{point}})
	}
	for i := range p.Sections {
		for len(p.Sections[i].Points) < MaxPoints {
			p.Sections[i].Points = append(p.Sections[i].Points, point)
		}
	}
	p.Sections[2].Points[4] = point + " and more words here too"
	_, err := Draw(p, "scientific", "square")
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("a full square poster: %v", err)
	}
	for _, want := range []string{"more height than the square poster has", "The tallest is sections[", "the longest is sections["} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
	// Where a figure is most of what does not fit, the figure is named,
	// and what to make smaller in it.
	for i := range withFigures.Sections {
		withFigures.Sections[i].Points = []string{point}
	}
	withFigures.Sections = append(withFigures.Sections, Section{Heading: "More", Points: []string{point}}, Section{Heading: "And more", Points: []string{point}})
	_, err = Draw(withFigures, "scientific", "square")
	if !errors.As(err, &r) || !strings.Contains(err.Error(), "its figure (a ") || !strings.Contains(err.Error(), "draw it smaller") {
		t.Errorf("a poster too full of figures: %v", err)
	}
}

// A word wider than any column is refused by name.
func TestAWordTooWide(t *testing.T) {
	p := fixture(t)
	p.Sections[0].Heading = strings.Repeat("Bronchiolitis", 8)
	_, err := Draw(p, "editorial", "portrait")
	if err == nil || !strings.Contains(err.Error(), `sections[0].heading: the word "BronchiolitisBronchiolitis`) {
		t.Fatalf("a word too wide: %v", err)
	}
}

// The text sizes never fall below their floors, and a sparse poster is
// set larger than a full one.
func TestTypeIsSizedToTheContent(t *testing.T) {
	p := fixture(t)
	l, _ := lookOf("sketch-note")
	c, _ := CanvasNamed("landscape")
	full, err := layout(p, c, l, drawnFigures(t, p, l))
	if err != nil {
		t.Fatal(err)
	}
	sparse := Poster{Title: "Asthma", Sections: []Section{{Heading: "One", Points: []string{"A point"}}, {Heading: "Two", Points: []string{"Another"}}}}
	small, err := layout(sparse, c, l, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !(small.sc.point.size > full.sc.point.size) || full.sc.point.size < floorPoint {
		t.Errorf("points at %.1f (full) and %.1f (sparse); the floor is %.0f", full.sc.point.size, small.sc.point.size, floorPoint)
	}
}

func drawnFigures(t *testing.T, p Poster, l look) map[int]figure {
	figs := map[int]figure{}
	for i, s := range p.Sections {
		if len(s.Figure) > 0 {
			f, err := drawFigure(s.Figure, l, cardBackground(l))
			if err != nil {
				t.Fatal(err)
			}
			figs[i] = f
		}
	}
	return figs
}

// The Markdown names every heading, point and figure.
func TestMarkdown(t *testing.T) {
	md := Markdown(fixture(t))
	for _, want := range []string{"# Asthma in children", "- **1 in 11** children", "## Treat an acute attack", "Figure: First hour of an attack."} {
		if !strings.Contains(md, want) {
			t.Errorf("the Markdown lacks %q:\n%s", want, md)
		}
	}
}

// With resvg (POSTER_TEST_RESVG) and figures-go's fonts (POSTER_TEST_FONTS),
// every style and canvas is drawn as a PNG into POSTER_TEST_OUT, to be
// looked at, and the time each takes is reported.
func TestPNGs(t *testing.T) {
	bin, fonts, out := os.Getenv("POSTER_TEST_RESVG"), os.Getenv("POSTER_TEST_FONTS"), os.Getenv("POSTER_TEST_OUT")
	if bin == "" || fonts == "" || out == "" {
		t.Skip("POSTER_TEST_RESVG, POSTER_TEST_FONTS and POSTER_TEST_OUT are not set")
	}
	r, err := NewRasteriser(bin, fonts, t.TempDir(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, style := range Styles() {
		for _, c := range Canvases {
			p := forCanvas(t, c)
			start := time.Now()
			d, err := Draw(p, style, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			drawn := time.Since(start)
			png, err := r.PNG(context.Background(), d)
			if err != nil {
				t.Fatal(err)
			}
			name := fmt.Sprintf("%s-%s", style, c.ID)
			if err := os.WriteFile(filepath.Join(out, name+".png"), png, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, name+".svg"), []byte(d.Kept), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: laid out and drawn in %s, PNG in %s (%d KB; SVG %d KB, kept %d KB)",
				name, drawn.Round(time.Microsecond), (time.Since(start) - drawn).Round(time.Millisecond), len(png)>>10, len(d.SVG)>>10, len(d.Kept)>>10)
		}
	}
}

// How long laying out and drawing a full poster takes, without resvg.
func BenchmarkDraw(b *testing.B) {
	p := fixture(b)
	for b.Loop() {
		if _, err := Draw(p, "scientific", "landscape"); err != nil {
			b.Fatal(err)
		}
	}
}

func countFigures(p Poster) int {
	n := 0
	for _, s := range p.Sections {
		if len(s.Figure) > 0 {
			n++
		}
	}
	return n
}

// Two figures with the same ids (a freeform arrowhead each) keep their own
// in one document.
func TestNestedFigureIDsAreTheirOwn(t *testing.T) {
	p := fixture(t)
	svg := `{"type":"svg","title":"Arrow","alt":"An arrow.","spec":{"svg":"<svg viewBox=\"0 0 100 40\"><defs><marker id=\"a\" viewBox=\"0 0 10 10\" refX=\"9\" refY=\"5\" orient=\"auto\"><path d=\"M0 0L10 5L0 10z\" fill=\"ink\"/></marker></defs><line x1=\"5\" y1=\"20\" x2=\"90\" y2=\"20\" stroke=\"ink\" marker-end=\"url(#a)\"/></svg>"}}`
	p.Sections[2].Figure, p.Sections[3].Figure = []byte(svg), []byte(svg)
	p, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Draw(p, "scientific", "landscape")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="p1-ff-a"`, `url(#p1-ff-a)`, `id="p2-ff-a"`, `url(#p2-ff-a)`} {
		if !strings.Contains(d.SVG, want) {
			t.Errorf("the drawing lacks %s", want)
		}
	}
}
