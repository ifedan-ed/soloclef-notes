package deckspec

import (
	"fmt"
	"strings"
)

// Steps is how many parts of a slide come in one after another in a video:
// the first is the slide itself (its title, and anything that frames the
// rest), then each point, event, figure or stage in turn. A deck file has
// no builds (it is read on a screen, or presented with the speaker's own
// pace), so only a video uses this.
//
// It is also exactly how many narration passages a video's slide has: one
// per part, each voiced on its own, so the video knows when each passage
// really starts and brings its part in then (Check holds a video's slides
// to it; partsNamed words the count for a model).
func Steps(s Slide) int {
	switch s.Kind {
	case KindTitle:
		if s.Subtitle != "" {
			return 2
		}
		return 1
	case KindQuote:
		if s.Attribution != "" {
			return 2
		}
		return 1
	case KindBullets:
		return 1 + len(s.Bullets)
	case KindTwoColumn:
		return 3
	case KindComparison:
		return 1 + len(s.Rows)
	case KindTimeline:
		return 1 + len(s.Events)
	case KindStats:
		return 1 + len(s.Stats)
	case KindDiagram:
		return 1 + len(s.Steps)
	case KindChart:
		return 2
	}
	return 1
}

// partsNamed is what comes in after a slide's first step, in the words a
// refusal gives the model ("each of its 3 bullets", "its subtitle"), or ""
// for a slide that comes in whole. It follows Steps case by case, so the
// count a model is told and the count it is held to are one.
func partsNamed(s Slide) string {
	each := func(n int, name string) string { return fmt.Sprintf("one for each of its %d %s", n, name) }
	switch s.Kind {
	case KindTitle:
		if s.Subtitle != "" {
			return "one for its subtitle"
		}
	case KindQuote:
		if s.Attribution != "" {
			return "one for its attribution"
		}
	case KindBullets:
		return each(len(s.Bullets), "bullets")
	case KindTwoColumn:
		return "one for the left column and one for the right"
	case KindComparison:
		return each(len(s.Rows), "rows")
	case KindTimeline:
		return each(len(s.Events), "events")
	case KindStats:
		return each(len(s.Stats), "figures")
	case KindDiagram:
		return each(len(s.Steps), "stages")
	case KindChart:
		return "one as its bars come in"
	}
	return ""
}

// Spoken is a slide's narration as one text, the passages in order: what
// the slide's transcript and the video's document say was spoken over it.
// The passages are voiced one by one (each part comes in as its own
// passage starts), and their audio is joined into the slide's narration.
func Spoken(s Slide) string { return strings.Join(s.Narration, " ") }
