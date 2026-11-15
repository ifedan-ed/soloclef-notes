package posterrender

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender"
)

// Rasteriser makes a poster's PNG with resvg (linebender/resvg, Apache-2.0
// OR MIT), the renderer figures-go and the deck's video frames use: it draws
// SVG exactly with no browser, and it reads fonts from the directories it
// is given and nothing else, so the PNG is set in exactly the faces the
// layout measured, on every server.
//
// Two font directories: the deck's faces (Carlito, Caladea), which the
// rasteriser writes itself from the faces compiled into deckrender, and
// figures-go's (FreeSans, DejaVu Sans), which the image copies from the
// module (FontDir). A figure names a system font stack ending in
// sans-serif, which resvg is told is FreeSans: the face whose widths
// figures-go measured its labels with.
type Rasteriser struct {
	bin       string
	deckFonts string
	figFonts  string
	timeout   time.Duration
}

// PNGScale is how many PNG pixels a poster unit is: twice, so a 1536-wide
// poster is sharp when zoomed into on a phone or shown full screen on a
// laptop.
const PNGScale = 2

// NewRasteriser checks that resvg runs and that figFontDir holds fonts,
// and writes the deck's faces into workDir (which must exist and be the
// rasteriser's own). timeout bounds one PNG.
func NewRasteriser(resvg, figFontDir, workDir string, timeout time.Duration) (*Rasteriser, error) {
	switch {
	case resvg == "":
		return nil, errors.New("posterrender: no resvg binary named")
	case figFontDir == "":
		return nil, errors.New("posterrender: no figure font directory named")
	case workDir == "":
		return nil, errors.New("posterrender: no work directory named")
	case timeout <= 0:
		return nil, fmt.Errorf("posterrender: the timeout must be positive, got %s", timeout)
	}
	if out, err := exec.Command(resvg, "--version").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("posterrender: %s --version: %w: %s", resvg, err, strings.TrimSpace(string(out)))
	}
	if fonts, _ := filepath.Glob(filepath.Join(figFontDir, "*.ttf")); len(fonts) == 0 {
		return nil, fmt.Errorf("posterrender: no .ttf fonts in %s (figures-go's fonts/ directory)", figFontDir)
	}
	deck := filepath.Join(workDir, "deck-fonts")
	if err := os.MkdirAll(deck, 0o755); err != nil {
		return nil, fmt.Errorf("posterrender: %w", err)
	}
	if err := deckrender.WriteFonts(deck); err != nil {
		return nil, fmt.Errorf("posterrender: writing the deck's faces to %s: %w", deck, err)
	}
	return &Rasteriser{bin: resvg, deckFonts: deck, figFonts: figFontDir, timeout: timeout}, nil
}

// maxPNGBytes bounds a PNG read back: a 3072 by 2048 poster is a few
// megabytes at most.
const maxPNGBytes = 32 << 20

// PNG draws a poster's SVG (Drawn.SVG) as a PNG PNGScale times its size.
func (r *Rasteriser) PNG(ctx context.Context, d Drawn) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.bin,
		"--width", strconv.Itoa(int(d.Width*PNGScale)),
		"--skip-system-fonts", "--use-fonts-dir", r.deckFonts, "--use-fonts-dir", r.figFonts,
		"--font-family", "Carlito", "--sans-serif-family", "FreeSans", "--serif-family", "Caladea",
		"--shape-rendering", "geometricPrecision", "--text-rendering", "optimizeLegibility",
		"-", "-c")
	cmd.Stdin = strings.NewReader(d.SVG)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	// resvg reads nothing from HOME, but an empty environment keeps the
	// server's own from reaching it.
	cmd.Env = []string{"HOME=" + os.TempDir()}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("posterrender: resvg: %w", ctx.Err())
		}
		return nil, fmt.Errorf("posterrender: resvg: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	png := out.Bytes()
	if !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) || len(png) > maxPNGBytes {
		return nil, fmt.Errorf("posterrender: resvg wrote %d bytes that are not a PNG", len(png))
	}
	return png, nil
}
