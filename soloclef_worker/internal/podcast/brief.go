package podcast

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Brief is the approved brief (`podcast_runs.spec`): what the transcript is
// drafted to and the voices it is spoken in. The API tier validated it on
// the way in and approves only a brief it can read (internal/podcasts,
// spec.go); it is read strictly here all the same, because a brief that
// cannot be read is a transcript drafted from a guess.
type Brief struct {
	Language string    `json:"language"`
	Style    string    `json:"style"`
	Speakers []Speaker `json:"speakers"`
	Duration Duration  `json:"duration"`
	Focus    *string   `json:"focus"`
}

// Speaker is one voice of the brief. Slot is the key a transcript turn
// names its speaker by.
type Speaker struct {
	Slot    int    `json:"slot"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	VoiceID string `json:"voice_id"`
	// Delivery is how this speaker should sound, for a speech model that
	// takes a speaking style. Empty uses the deployment's.
	Delivery *string `json:"delivery"`
}

// Duration is the length asked for, as a range of seconds.
type Duration struct {
	MinSeconds int `json:"min_seconds"`
	MaxSeconds int `json:"max_seconds"`
}

// ReadBrief reads the column. A brief from before 2026-09 states its
// length in minutes, and is still read: old rows hold it.
func ReadBrief(column []byte) (Brief, error) {
	if len(column) == 0 || bytes.Equal(bytes.TrimSpace(column), []byte("null")) {
		return Brief{}, errors.New("the podcast has no brief")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(column, &raw); err != nil {
		return Brief{}, fmt.Errorf("the brief is not an object: %w", err)
	}
	if duration, found := raw["duration"]; found {
		var legacy struct {
			MinMinutes *int `json:"min_minutes"`
			MaxMinutes *int `json:"max_minutes"`
			MinSeconds *int `json:"min_seconds"`
		}
		if json.Unmarshal(duration, &legacy) == nil && legacy.MinSeconds == nil && legacy.MinMinutes != nil && legacy.MaxMinutes != nil {
			raw["duration"], _ = json.Marshal(Duration{MinSeconds: *legacy.MinMinutes * 60, MaxSeconds: *legacy.MaxMinutes * 60})
		}
	}
	normalised, err := json.Marshal(raw)
	if err != nil {
		return Brief{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(normalised))
	decoder.DisallowUnknownFields()
	var brief Brief
	if err := decoder.Decode(&brief); err != nil {
		return Brief{}, fmt.Errorf("the brief cannot be read: %w", err)
	}
	if brief.Style == "" {
		brief.Style = "conversational"
	}
	return brief, brief.check()
}

func (b Brief) check() error {
	switch {
	case strings.TrimSpace(b.Language) == "":
		return errors.New("the brief names no language")
	case len(b.Speakers) == 0:
		return errors.New("the brief has no speakers")
	case b.Duration.MinSeconds <= 0 || b.Duration.MaxSeconds < b.Duration.MinSeconds:
		return fmt.Errorf("the brief's length %d-%ds is not a range", b.Duration.MinSeconds, b.Duration.MaxSeconds)
	}
	seen := map[int]bool{}
	for _, speaker := range b.Speakers {
		if seen[speaker.Slot] {
			return fmt.Errorf("the brief gives slot %d to two speakers", speaker.Slot)
		}
		seen[speaker.Slot] = true
		if strings.TrimSpace(speaker.Name) == "" || strings.TrimSpace(speaker.VoiceID) == "" {
			return fmt.Errorf("speaker %d has no name or no voice", speaker.Slot)
		}
	}
	return nil
}

// Speaker returns the speaker in a slot.
func (b Brief) speaker(slot int) (Speaker, bool) {
	for _, speaker := range b.Speakers {
		if speaker.Slot == slot {
			return speaker, true
		}
	}
	return Speaker{}, false
}

// focus is the brief's focus, or "".
func (b Brief) focus() string {
	if b.Focus == nil {
		return ""
	}
	return strings.TrimSpace(*b.Focus)
}
