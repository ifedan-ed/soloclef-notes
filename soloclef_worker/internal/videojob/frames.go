package videojob

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender"
)

// Rasteriser draws an SVG into a PNG file (deckrender.Rasteriser, resvg).
type Rasteriser interface {
	PNG(ctx context.Context, svg, out string) error
}

// draw draws every picture the film needs into dir, workers at a time, and
// returns each cut's file. First each slide's settled picture (what the
// next slide fades in over), then the frames.
func draw(ctx context.Context, raster Rasteriser, workers int, dir string, video Authored, shots []shot, film []cut) ([]string, error) {
	stills := make([]string, len(video.Scenes))
	var jobs []func(context.Context) error
	for i := range video.Scenes {
		stills[i] = filepath.Join(dir, fmt.Sprintf("still-%03d.png", i+1))
		if i == len(video.Scenes)-1 {
			// Nothing fades in over the last slide.
			continue
		}
		scene, out := video.Scenes[i], stills[i]
		jobs = append(jobs, func(ctx context.Context) error { return raster.PNG(ctx, scene.Still(), out) })
	}
	if err := runAll(ctx, workers, jobs); err != nil {
		return nil, err
	}
	paths := make([]string, len(film))
	jobs = jobs[:0]
	for n, c := range film {
		paths[n] = filepath.Join(dir, fmt.Sprintf("frame-%05d.png", n+1))
		p, out := c.Picture, paths[n]
		svg := video.Scenes[p.Slide].Frame(shots[p.Slide].Starts, p.At)
		if p.Fade > 0 {
			// The previous slide's picture over the new one, going: the
			// new slide's ground shows through as it fades, and its title
			// rises in under it.
			svg = strings.TrimSuffix(svg, "</svg>") + fmt.Sprintf(`<image href="%s" x="0" y="0" width="%g" height="%g" preserveAspectRatio="none" opacity="%.3f"/></svg>`,
				filepath.Base(stills[p.Slide-1]), deckrender.SlideW, deckrender.SlideH, p.Fade)
		}
		jobs = append(jobs, func(ctx context.Context) error { return raster.PNG(ctx, svg, out) })
	}
	if err := runAll(ctx, workers, jobs); err != nil {
		return nil, err
	}
	return paths, nil
}

// runAll runs jobs, workers at a time, and stops them all at the first
// failure, which it returns.
func runAll(ctx context.Context, workers int, jobs []func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		once  sync.Once
		first error
		wait  sync.WaitGroup
	)
	next := make(chan func(context.Context) error)
	for range max(1, workers) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for job := range next {
				if ctx.Err() != nil {
					continue
				}
				if err := job(ctx); err != nil {
					once.Do(func() { first = err })
					cancel()
				}
			}
		}()
	}
	for _, job := range jobs {
		next <- job
	}
	close(next)
	wait.Wait()
	if first != nil {
		return first
	}
	return ctx.Err()
}
