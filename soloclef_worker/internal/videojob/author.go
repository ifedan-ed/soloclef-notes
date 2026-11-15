package videojob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender"
	"git.danvics.com/danvics/soloclef/soloclef_studio/deckspec"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/llm"
)

// Authored is the video as written: a checked, narrated deck, and its
// slides laid out (the layout is what decided the deck's text fits, so it
// is kept rather than done twice).
type Authored struct {
	Deck   deckspec.Deck
	Scenes []deckrender.Scene
}

// spoken is each slide's narration as it is voiced.
func (a Authored) spoken() []string {
	out := make([]string, len(a.Deck.Slides))
	for i, s := range a.Deck.Slides {
		out[i] = deckspec.Spoken(s)
	}
	return out
}

// author asks the account's chat model to write and to shorten.
type author struct {
	model   model.Model
	timeout time.Duration
}

// encode is JSON for a model to read: characters as they are, not escaped
// for HTML.
func encode(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSpace(buffer.String()), nil
}

// deckFixes is how many rounds of correction a video's deck gets: two.
//
// More than one, because a deck is many independent parts and the model
// writes it whole again each round: a round that fixes what it was told
// can break another part (a value reworded longer, a passage over its
// limit), and a second round, which sees the whole exchange (llm.AskJSON
// keeps it), catches that. On 14 Nov 2026 a video failed on one stats
// slide after a single round.
//
// Not more, because each round rewrites the whole deck, a minute or more of
// a job a person is waiting on, and the problems sent back are measured
// and name the word and the room (deckrender.FitProblem): a model that two
// such corrections did not move is not converging, and the job then fails
// saying exactly what was wrong rather than spending more.
const deckFixes = 2

// narrationFixes is how many rounds a shortened narration gets: one. It is
// a list of passages with a count to keep, which one correction fixes.
const narrationFixes = 1

// write is the deck, checked and laid out. A reply that does not fit the
// schema, breaks a rule or has text too long for its slide is sent back
// with every problem, up to deckFixes times (llm.AskJSON); the problems are
// the same words a chat's deck is refused with.
func (a author) write(ctx context.Context, title string, request Request) (Authored, error) {
	payload := struct {
		Title            string   `json:"title"`
		Brief            string   `json:"brief"`
		SourceReferences []string `json:"source_references"`
		SourceMaterial   string   `json:"source_material,omitempty"`
		SourceNote       string   `json:"source_note,omitempty"`
	}{Title: title, Brief: request.Brief, SourceReferences: request.SourceReferences, SourceMaterial: request.SourceMaterial}
	if payload.SourceReferences == nil {
		payload.SourceReferences = []string{}
	}
	if request.SourceCondensed && request.SourceMaterial != "" {
		payload.SourceNote = condensedNote
	}
	content, err := encode(payload)
	if err != nil {
		return Authored{}, err
	}
	return llm.AskJSON(ctx, a.model, a.timeout, deckFixes, []model.Message{
		{Role: model.RoleSystem, Content: authorPrompt(request.limitSeconds())},
		{Role: model.RoleUser, Content: content},
	}, readDeck)
}

// readDeck reads, checks and lays out a narrated deck, or rejects it with
// everything to fix.
func readDeck(raw []byte) (Authored, error) {
	deck, err := deckspec.Decode(raw)
	if err != nil {
		return Authored{}, llm.Reject("%s", err.Error())
	}
	deck, err = deckspec.Check(deck, deckspec.Options{MinSlides: minSlides, MaxSlides: maxSlides, Narrated: true})
	if err != nil {
		var problems deckspec.Problems
		if errors.As(err, &problems) {
			return Authored{}, llm.Reject("%s", problems.Error())
		}
		return Authored{}, err
	}
	scenes, fits := deckrender.Layout(deck)
	if len(fits) > 0 {
		words := make([]string, len(fits))
		for i, f := range fits {
			words[i] = f.String()
		}
		return Authored{}, llm.Reject("%s", strings.Join(words, "; "))
	}
	return Authored{Deck: deck, Scenes: scenes}, nil
}

// shorten rewrites the narration only, to the target the measurement
// names; the slides stay as they are, so nothing is laid out again.
func (a author) shorten(ctx context.Context, video Authored, measurement map[string]any) (Authored, error) {
	slides := make([]map[string]any, len(video.Deck.Slides))
	for i, s := range video.Deck.Slides {
		slides[i] = map[string]any{"title": s.Title, "kind": s.Kind, "narration": s.Narration}
	}
	payload := map[string]any{"slide_count": len(slides), "slides": slides}
	for key, value := range measurement {
		payload[key] = value
	}
	content, err := encode(payload)
	if err != nil {
		return Authored{}, err
	}
	narration, err := llm.AskJSON(ctx, a.model, a.timeout, narrationFixes, []model.Message{
		{Role: model.RoleSystem, Content: shortenPrompt},
		{Role: model.RoleUser, Content: content},
	}, readShortened(video.Deck))
	if err != nil {
		return Authored{}, err
	}
	deck := video.Deck
	deck.Slides = append([]deckspec.Slide(nil), video.Deck.Slides...)
	for i := range deck.Slides {
		deck.Slides[i].Narration = narration[i]
	}
	return Authored{Deck: deck, Scenes: video.Scenes}, nil
}

// readShortened reads a shortened narration: one entry per slide, each
// with as many passages as the slide had (they are when its parts come in),
// each checked as a passage is.
func readShortened(deck deckspec.Deck) func([]byte) ([][]string, error) {
	return func(raw []byte) ([][]string, error) {
		var reply struct {
			Slides []struct {
				Narration []string `json:"narration"`
			} `json:"slides"`
		}
		if err := json.Unmarshal(raw, &reply); err != nil {
			return nil, llm.Reject("the reply is not an object with a slides array: %v", err)
		}
		if len(reply.Slides) != len(deck.Slides) {
			return nil, llm.Reject("expected %d slides, got %d: return exactly one entry for each supplied slide, in the same order", len(deck.Slides), len(reply.Slides))
		}
		check := deck
		check.Slides = append([]deckspec.Slide(nil), deck.Slides...)
		var problems []string
		for i, s := range reply.Slides {
			if len(s.Narration) != len(deck.Slides[i].Narration) {
				problems = append(problems, fmt.Sprintf("slide %d has %d passages and needs %d", i+1, len(s.Narration), len(deck.Slides[i].Narration)))
			}
			check.Slides[i].Narration = s.Narration
		}
		if len(problems) > 0 {
			return nil, llm.Reject("%s", strings.Join(problems, "; "))
		}
		checked, err := deckspec.Check(check, deckspec.Options{MinSlides: minSlides, MaxSlides: maxSlides, Narrated: true})
		if err != nil {
			return nil, llm.Reject("%s", err.Error())
		}
		out := make([][]string, len(checked.Slides))
		for i, s := range checked.Slides {
			out[i] = s.Narration
		}
		return out, nil
	}
}

// clipRunes cuts text to at most limit characters.
func clipRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}
