package deckrender

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Rasteriser turns a slide's SVG into a PNG with resvg (linebender/resvg,
// Apache-2.0 or MIT): the same renderer figures-go uses, chosen for the
// same reasons. It draws SVG exactly and quickly with no browser, and it
// reads fonts from one directory and nothing else, so a frame is drawn in
// the faces its text was measured in, the same on every server.
type Rasteriser struct {
	binary string
	fonts  string
}

// NewRasteriser checks that binary runs and writes the slide fonts into
// fontDir (which must exist and be the rasteriser's own).
func NewRasteriser(binary, fontDir string) (*Rasteriser, error) {
	if binary == "" {
		return nil, errors.New("deckrender: no resvg binary named")
	}
	out, err := exec.Command(binary, "--version").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("deckrender: %s --version: %w: %s", binary, err, strings.TrimSpace(string(out)))
	}
	if err := WriteFonts(fontDir); err != nil {
		return nil, fmt.Errorf("deckrender: writing the slide fonts to %s: %w", fontDir, err)
	}
	return &Rasteriser{binary: binary, fonts: fontDir}, nil
}

// PNG draws svg into the file out. A picture the SVG refers to by a
// relative name (a video's cross-fade lays the previous slide's picture
// over the next) is found beside out.
func (r *Rasteriser) PNG(ctx context.Context, svg, out string) error {
	// --skip-system-fonts: only the slide fonts, so a server's own fonts
	// can never change how a frame looks.
	cmd := exec.CommandContext(ctx, r.binary, "--skip-system-fonts", "--use-fonts-dir", r.fonts,
		"--sans-serif-family", "Carlito", "--serif-family", "Caladea", "--font-family", "Carlito",
		"--resources-dir", filepath.Dir(out), "-", out)
	cmd.Stdin = strings.NewReader(svg)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("resvg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if info, err := os.Stat(out); err != nil || info.Size() == 0 {
		return fmt.Errorf("resvg wrote no picture to %s", out)
	}
	return nil
}
