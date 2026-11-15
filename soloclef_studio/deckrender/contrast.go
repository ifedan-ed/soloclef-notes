package deckrender

import (
	"fmt"
	"math"
	"strconv"
)

// Contrast (WCAG 2.1), Tools' rule (pptxrender/contrast.go): a slide is
// read across a room, on projectors that wash colour out, so text is held
// to these ratios against what it is drawn on. Text under 24 pt needs
// 4.5:1, larger text 3:1. Colours are fixed while drawing, so every theme
// passes by construction and an accent a theme or a person chose is
// deepened only as far as it must be.
const (
	contrastBody  = 4.5
	contrastLarge = 3.0
	largeText     = 24.0
)

func needFor(size float64) float64 {
	if size >= largeText {
		return contrastLarge
	}
	return contrastBody
}

func hexRGB(hex string) (r, g, b float64) {
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil || len(hex) != 6 {
		panic(fmt.Sprintf("deckrender: %q is not a colour", hex))
	}
	return float64(v >> 16 & 0xFF), float64(v >> 8 & 0xFF), float64(v & 0xFF)
}

func luminance(hex string) float64 {
	r, g, b := hexRGB(hex)
	lin := func(c float64) float64 {
		c /= 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// contrastRatio is WCAG's ratio between two colours, 1 to 21.
func contrastRatio(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// mix is a moved a step of the way to b (0 is a, 1 is b).
func mix(a, b string, step float64) string {
	ar, ag, ab := hexRGB(a)
	br, bg, bb := hexRGB(b)
	m := func(x, y float64) int { return int(math.Round(x + (y-x)*step)) }
	return fmt.Sprintf("%02X%02X%02X", m(ar, br), m(ag, bg), m(ab, bb))
}

// legible is colour as text of this size on ground: unchanged when it
// already reads, else moved towards toward (the theme's ink, or its paper
// on a dark slide) by the least that does. The hue stays.
func legible(colour, ground, toward string, size float64) string {
	need := needFor(size)
	if contrastRatio(colour, ground) >= need {
		return colour
	}
	for i := 1; i <= 20; i++ {
		c := mix(colour, toward, float64(i)/20)
		if contrastRatio(c, ground) >= need {
			return c
		}
	}
	return toward
}

// onFill is the colour for text of this size on a filled shape: white when
// it reads, else the theme's darkest colour (its ink, or a dark theme's
// paper), deepened towards black if even that does not. A light accent
// (a dark theme's) takes dark text; a deep one takes white.
func onFill(fill, darkest string, size float64) string {
	need := needFor(size)
	if contrastRatio("FFFFFF", fill) >= need {
		return "FFFFFF"
	}
	return legible(darkest, fill, "000000", size)
}
