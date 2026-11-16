package videojob

import (
	"math"
	"strings"
)

// How much narration fits in a video, in words an author can count. The
// prompts speak in words; the speech service turns them into seconds. These
// translate the limit into a word budget before anything is voiced, and a
// measured overrun back into a word target after
// (app/deliverables/video/narration_budget.py, with its measurements).
const (
	// wordsPerSecond is the narrating voice's rate, rounded down so pauses
	// stay inside it. It was 2.4, from the voices of the day (2.52 and 2.83
	// words a second); the voice now deployed, Kokoro af_heart, speaks
	// about 2.28 (137 words a minute, measured 15 Nov 2026).
	wordsPerSecond = 2.2
	// budgetSafety leaves room for a draft exactly on budget.
	budgetSafety = 0.9
	// overrunTarget is how far below the limit a shortened draft aims.
	overrunTarget = 0.95
	// draftShortenings bounds the word-count checks before anything is
	// voiced: one model call each and no speech, stopped at two so a model
	// that cannot count lets the measured length decide.
	draftShortenings = 2
)

func narrationWordBudget(limitSeconds int) int {
	return int(math.Floor(float64(limitSeconds) * wordsPerSecond * budgetSafety))
}

func countWords(transcripts []string) int {
	total := 0
	for _, transcript := range transcripts {
		total += len(strings.Fields(transcript))
	}
	return total
}

// overrunWords is the word count that lands a draft measured at measured
// seconds at the target share of the limit.
func overrunWords(words int, measured float64, limitSeconds int) int {
	ratio := float64(limitSeconds) * overrunTarget / measured
	return max(1, int(math.Floor(float64(words)*ratio)))
}
