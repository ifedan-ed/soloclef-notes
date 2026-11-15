package deckrender

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckspec"
)

// The sample deck has a slide of every kind; it is drawn in every theme.
func sample(t testing.TB, theme string) deckspec.Deck {
	t.Helper()
	raw, err := os.ReadFile("testdata/photosynthesis.json")
	if err != nil {
		t.Fatal(err)
	}
	deck, err := deckspec.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if theme != "" {
		deck.Theme = theme
	}
	deck, err = deckspec.Check(deck, deckspec.Options{MinSlides: 3, MaxSlides: 30})
	if err != nil {
		t.Fatal(err)
	}
	return deck
}

func TestEveryThemeHasAPaletteAndEveryPaletteATheme(t *testing.T) {
	for _, theme := range deckspec.Themes {
		if _, ok := palettes[theme.ID]; !ok {
			t.Errorf("theme %q is offered to the model and has no palette", theme.ID)
		}
	}
	if len(palettes) != len(deckspec.Themes) {
		t.Errorf("%d palettes and %d themes: a palette nobody can choose", len(palettes), len(deckspec.Themes))
	}
}

func TestTheSampleDeckFitsInEveryTheme(t *testing.T) {
	for _, theme := range deckspec.Themes {
		t.Run(theme.ID, func(t *testing.T) {
			deck := sample(t, theme.ID)
			scenes, problems := Layout(deck)
			if len(problems) > 0 {
				t.Fatalf("problems: %v", problems)
			}
			// Every slide the model wrote, and the sources slide.
			if len(scenes) != len(deck.Slides)+1 {
				t.Fatalf("%d scenes for %d slides and a sources slide", len(scenes), len(deck.Slides))
			}
			data, err := PPTX(deck.Title, scenes, time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			slides := 0
			reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range reader.File {
				if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
					slides++
				}
			}
			if slides != len(scenes) {
				t.Fatalf("%d slides in the file, %d laid out", slides, len(scenes))
			}
			for _, scene := range scenes {
				for _, e := range scene.Elements {
					if text, ok := e.(*Text); ok && text.Fit.height > text.H+0.01 {
						t.Errorf("%s %s: text %.1f pt tall in a box of %.1f", scene.Name, text.Name, text.Fit.height, text.H)
					}
				}
			}
		})
	}
}

func TestTooMuchTextIsSentBackWithHowMuchToCut(t *testing.T) {
	deck := sample(t, "")
	// Six points of sixty words: past what deckspec.Check lets through,
	// which is what the renderer must still answer for in words.
	point := strings.Repeat("A long point that goes on and on ", 12)
	deck.Slides[1].Bullets = []string{point, point, point, point, point, point}
	_, problems := Layout(deck)
	if len(problems) != 1 || problems[0].Slide != 2 || !strings.Contains(problems[0].String(), "slide 2 (bullets) the bullets: there is too much text for its place; cut it by about") {
		t.Fatalf("problems: %v", problems)
	}
}

func TestFramesBuildInStepOrderAndSettle(t *testing.T) {
	deck := sample(t, "")
	scenes, _ := Layout(deck)
	bullets := scenes[1]
	starts := Starts{0, 2, 4, 6, 8}
	early := bullets.Frame(starts, 0.3)
	if !strings.Contains(early, "Photosynthesis stores") || strings.Contains(early, "Inputs:") {
		t.Fatalf("at 0.3 s the heading should be coming in and no bullet yet")
	}
	if late := bullets.Frame(starts, 9); !strings.Contains(late, "Almost all food chains") {
		t.Fatalf("at 9 s the last bullet should be in")
	}
	if settled := bullets.Settled(starts); settled < 8 || settled > 9.5 {
		t.Fatalf("the slide settles at %.2f s; its last step starts at 8", settled)
	}
	if still, end := bullets.Still(), bullets.Frame(starts, 60); still != end {
		t.Fatalf("the still is not the settled frame")
	}
}

func TestANumberCountsUpInItsOwnFormat(t *testing.T) {
	for _, c := range []struct {
		text  string
		share float64
		want  string
	}{
		{"72%", 0.5, "36%"}, {"1,200", 0.5, "600"}, {"12,345", 1, "12,345"}, {"0.8", 0.5, "0.4"}, {"$1.2 million", 0.5, "$0.6 million"}, {"3x", 0, "0x"}, {"none", 0.5, "none"},
	} {
		if got := countTo(c.text, c.share); got != c.want {
			t.Errorf("countTo(%q, %v) = %q, want %q", c.text, c.share, got, c.want)
		}
	}
}

// TestStills writes every slide of the sample in every theme as a PNG, for
// a person to look at: SOLOCLEF_TEST_RESVG names resvg and
// SOLOCLEF_TEST_DECK_OUT the directory to write to; it also writes each
// theme's .pptx there for LibreOffice.
func TestStills(t *testing.T) {
	binary, out := os.Getenv("SOLOCLEF_TEST_RESVG"), os.Getenv("SOLOCLEF_TEST_DECK_OUT")
	if binary == "" || out == "" {
		t.Skip("SOLOCLEF_TEST_RESVG and SOLOCLEF_TEST_DECK_OUT are not set")
	}
	fonts := t.TempDir()
	r, err := NewRasteriser(binary, fonts)
	if err != nil {
		t.Fatal(err)
	}
	for _, theme := range deckspec.Themes {
		deck := sample(t, theme.ID)
		scenes, problems := Layout(deck)
		if len(problems) > 0 {
			t.Fatal(problems)
		}
		dir := filepath.Join(out, theme.ID)
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

// The cost of drawing a deck: the twelve-slide sample laid out and written
// as a .pptx, every text measured and fitted.
func BenchmarkDrawADeck(b *testing.B) {
	deck := sample(b, "")
	b.ReportAllocs()
	for b.Loop() {
		scenes, problems := Layout(deck)
		if len(problems) > 0 {
			b.Fatal(problems)
		}
		if _, err := PPTX(deck.Title, scenes, time.Now()); err != nil {
			b.Fatal(err)
		}
	}
}

// The cost of one video frame's SVG (the rasteriser's is resvg's own).
func BenchmarkAFrame(b *testing.B) {
	scenes, _ := Layout(sample(b, ""))
	starts := Starts{0, 1, 2, 3, 4}
	b.ReportAllocs()
	for b.Loop() {
		_ = scenes[3].Frame(starts, 2.2)
	}
}
