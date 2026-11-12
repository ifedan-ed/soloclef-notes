package podcast

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/llm"
)

// Turn is one spoken line. Speaker is a brief slot.
type Turn struct {
	Speaker int    `json:"speaker"`
	Text    string `json:"text"`
}

// Transcript is what `podcast_runs.podcast_transcript` holds, in the shape
// the API tier reads (internal/podcasts/transcript.go).
type Transcript struct {
	Turns []Turn `json:"turns"`
}

// ReadTranscript reads the column.
func ReadTranscript(column []byte) (Transcript, error) {
	var transcript Transcript
	if err := json.Unmarshal(column, &transcript); err != nil {
		return Transcript{}, fmt.Errorf("the transcript cannot be read: %w", err)
	}
	if len(transcript.Turns) == 0 {
		return Transcript{}, fmt.Errorf("the transcript has no lines")
	}
	return transcript, nil
}

// outline is the plan the drafting follows: one beat of the conversation
// per segment, each drafted on its own.
type outline struct {
	Segments []segment
}

type segment struct {
	Title         string
	TalkingPoints []string
	TargetWords   int
}

// readOutline checks an outline reply. A segment sized at zero or less is a
// planning slip, not a reason to refuse the plan: it is drafted as short as
// it can be.
func readOutline(raw []byte) (outline, error) {
	var reply struct {
		Segments []struct {
			Title         string      `json:"title"`
			TalkingPoints []string    `json:"talking_points"`
			TargetWords   json.Number `json:"target_words"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return outline{}, llm.Reject("the reply does not have the outline's shape: %v", err)
	}
	if len(reply.Segments) == 0 {
		return outline{}, llm.Reject("- segments: at least one segment is required")
	}
	var problems []string
	plan := outline{}
	for index, written := range reply.Segments {
		words, err := written.TargetWords.Float64()
		if err != nil {
			problems = append(problems, fmt.Sprintf("- segments.%d.target_words: must be a number", index))
			continue
		}
		if strings.TrimSpace(written.Title) == "" {
			problems = append(problems, fmt.Sprintf("- segments.%d.title: must not be empty", index))
			continue
		}
		plan.Segments = append(plan.Segments, segment{
			Title: strings.TrimSpace(written.Title), TalkingPoints: written.TalkingPoints,
			TargetWords: int(math.Max(1, math.Trunc(words))),
		})
	}
	if len(problems) > 0 {
		return outline{}, llm.Reject("%s", strings.Join(problems, "\n"))
	}
	return plan, nil
}

// readTurns checks a segment's dialogue. Models decorate a turn with keys
// like `emotion` or `speaker_name`; those are ignored. A turn with no
// speaker number or no text refuses the reply, so that the model is asked
// once more rather than a line being dropped from the middle of a
// conversation.
func readTurns(raw []byte) ([]Turn, error) {
	var reply struct {
		Turns []map[string]json.RawMessage `json:"turns"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, llm.Reject("the reply does not have the segment's shape: %v", err)
	}
	var (
		turns    []Turn
		problems []string
	)
	for index, written := range reply.Turns {
		var speaker float64
		if json.Unmarshal(written["speaker"], &speaker) != nil || speaker < 0 || speaker != math.Trunc(speaker) {
			problems = append(problems, fmt.Sprintf("- turns.%d.speaker: must be a slot number of 0 or more", index))
			continue
		}
		var text string
		if json.Unmarshal(written["text"], &text) != nil || strings.TrimSpace(text) == "" {
			problems = append(problems, fmt.Sprintf("- turns.%d.text: must not be blank", index))
			continue
		}
		turns = append(turns, Turn{Speaker: int(speaker), Text: strings.TrimSpace(text)})
	}
	if len(problems) > 0 {
		if len(problems) > 12 {
			problems = append(problems[:12], fmt.Sprintf("- and %d more", len(problems)-12))
		}
		return nil, llm.Reject("%s", strings.Join(problems, "\n"))
	}
	return turns, nil
}

// markdown is the episode as text: the artifact's document, which search
// indexes and a screen reader is given. Speakers are named, where the
// Python tier wrote "Speaker 0".
func markdown(title string, brief Brief, transcript Transcript) string {
	var text strings.Builder
	text.WriteString("# " + title + "\n")
	for _, turn := range transcript.Turns {
		name := fmt.Sprintf("Speaker %d", turn.Speaker)
		if speaker, found := brief.speaker(turn.Speaker); found {
			name = speaker.Name
		}
		text.WriteString("\n**" + name + ":** " + turn.Text + "\n")
	}
	return text.String()
}
