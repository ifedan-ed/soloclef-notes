package podcast

import (
	"fmt"
	"math"
	"strings"
)

// The drafting is outline first: one call plans the episode's beats, sized
// to the brief's length, and each beat is then drafted on its own with a
// recap of what came before. A single call cannot hold a coherent long
// script; it can plan beats that are drafted against a shared plan. The
// words below are app/podcasts/generation/prompts, which the owner tuned.

const (
	// wordsPerMinute is the speaking rate that turns a length into words.
	wordsPerMinute = 150
	// wordsPerSegment is roughly one beat, used to suggest how many to plan.
	wordsPerSegment = 250
	// recapRunes is how much of the dialogue so far each beat is shown.
	recapRunes = 800
)

// roundHalfEven is Python's round(), which the sizes were tuned with.
func roundHalfEven(value float64) int { return int(math.RoundToEven(value)) }

// sizes are the outline's word targets. The ceiling and not the midpoint:
// an episode runs as long as its material supports, up to the maximum, and
// never pads to get there.
func sizes(brief Brief) (target, minimum, segments int) {
	target = roundHalfEven(float64(brief.Duration.MaxSeconds) * wordsPerMinute / 60)
	minimum = roundHalfEven(float64(brief.Duration.MinSeconds) * wordsPerMinute / 60)
	segments = max(1, roundHalfEven(float64(target)/wordsPerSegment))
	return target, minimum, segments
}

func roster(brief Brief) string {
	lines := make([]string, len(brief.Speakers))
	for index, speaker := range brief.Speakers {
		lines[index] = fmt.Sprintf("- slot %d — %s (role: %s)", speaker.Slot, speaker.Name, speaker.Role)
	}
	return strings.Join(lines, "\n")
}

func outlinePrompt(brief Brief) string {
	target, minimum, suggested := sizes(brief)
	focus := ""
	if brief.focus() != "" {
		focus = "\nThe user asked the episode to focus on:\n" + brief.focus() + "\n"
	}
	// The example is copied more often than the instructions are read, so
	// it carries a plausible size rather than a placeholder.
	example := max(1, target/max(1, suggested))
	floor := ""
	if minimum > 0 {
		floor = fmt.Sprintf(", ideally at least %d words if the source has that much to say", minimum)
	}
	return fmt.Sprintf(`You are a podcast showrunner planning the structure of an episode before any dialogue is written.

The episode language is %s. The format is %s.

Speakers (refer to them by these slots later):
%s
%s
Plan an outline that, when fully drafted, runs up to about %d words of spoken dialogue (up to about %d segments)%s. Use as much of that length as the source content genuinely supports: every segment must bring new substance from the source. Never pad, repeat a point, re-summarise, or stretch a topic to reach a length; when the material is exhausted, plan fewer segments and close there. Each segment is one coherent beat of the conversation: an opening, distinct topic areas grounded in the source content, and a closing.

For each segment provide:
- title: a short label for the beat
- talking_points: 2-5 concrete points to cover, drawn from the source content
- target_words: how many words of dialogue this segment should run (the sum across segments should approximate %d)

Respond with strict JSON and nothing else:
{"segments": [{"title": "...", "talking_points": ["..."], "target_words": %d}]}
`, brief.Language, brief.Style, roster(brief), focus, target, suggested, floor, target, example)
}

func segmentPrompt(brief Brief, beat segment, position, total int, recap string) string {
	points := make([]string, len(beat.TalkingPoints))
	for index, point := range beat.TalkingPoints {
		points[index] = "- " + point
	}
	opening := "\nThis is the opening segment; begin the conversation naturally.\n"
	if recap != "" {
		opening = "\nRecap of the conversation so far (continue from here, do not repeat it):\n" + recap + "\n"
	}
	return fmt.Sprintf(`You are scripting natural, engaging podcast dialogue for segment %d of %d.

Write entirely in %s. The format is %s.

Speakers — attribute every line using these exact slot numbers:
%s
%s
This segment is "%s". Cover these points using only facts grounded in the provided source content:
%s

Aim for about %d words of dialogue, but every line must add something: never repeat a point already made, recap at length, or add filler to reach the count; ending the segment short is better than padding it. Keep turns conversational and varied; speakers should react to each other rather than deliver monologues. Do not add greetings or sign-offs unless this is the first or last segment.

Respond with strict JSON and nothing else:
{"turns": [{"speaker": <slot>, "text": "..."}]}
`, position, total, brief.Language, brief.Style, roster(brief), opening, beat.Title, strings.Join(points, "\n"), beat.TargetWords)
}

// recap is the end of the dialogue so far, each line under its speaker's
// name, so a beat continues the conversation instead of restarting it.
func recap(brief Brief, turns []Turn) string {
	if len(turns) == 0 {
		return ""
	}
	lines := make([]string, len(turns))
	for index, turn := range turns {
		name := fmt.Sprint(turn.Speaker)
		if speaker, found := brief.speaker(turn.Speaker); found {
			name = speaker.Name
		}
		lines[index] = name + ": " + turn.Text
	}
	runes := []rune(strings.Join(lines, "\n"))
	if len(runes) > recapRunes {
		runes = runes[len(runes)-recapRunes:]
	}
	return string(runes)
}
