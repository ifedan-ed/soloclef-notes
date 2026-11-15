package deliverables

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender"
	"git.danvics.com/danvics/soloclef/soloclef_studio/deckspec"
)

// A slide deck is made the way PedsHub Tools makes one: the model writes
// the deck's content as structured data (deckspec), as the argument of one
// save_artifact call, and Go draws it (deckrender) in a fixed theme, with
// every text sized to its box while it is drawn. Nothing the model writes
// is run.
//
// It replaces a deck built by the model's own python-pptx program in the
// sandbox: written, run, rendered, measured against the layout rules and
// repaired, over 17 to 60 model steps and several minutes, failing outright
// when the steps ran out (13 Nov 2026: two decks of three stopped at the
// step limit with nothing saved). Now it is one call that writes the deck,
// milliseconds of drawing, and one LibreOffice conversion for the preview.
//
// What can still go back to the model is only what it can fix in words: a
// field missing, a count out of range, a text too long for its place
// (measured, with how much to cut). The layout rules still run on what
// LibreOffice draws (deckpreview.go), as a check of the renderer rather
// than a loop: a finding there is a fault in the drawing, which the model
// cannot fix, so it is recorded with the deck and logged, not sent back.

// The slides a deck the model writes may have (the sources slide is drawn
// and not counted). Thirty is a long lecture; fewer than three is not a
// deck.
const (
	deckMinSlides = 3
	deckMaxSlides = 30
)

// slideDeckInstructions is what load_artifact_instructions answers for a
// deck.
var slideDeckInstructions = `Slide deck: a PowerPoint deck drawn by the application from content you write.

Pass the deck to save_artifact as: format "pptx", a title (the artifact's name), and deck, an object with the deck's content as described below. There is no code to write or run for a deck, and nothing to verify first: save_artifact draws it, checks it and stores it, or answers with every problem to fix (a missing field, a count out of range, a text too long for its place).

Make 3 to 30 slides, the number the task asks for when it says one.

` + deckspec.Instructions(false)

// saveSlideDeck checks a deck's content, draws it, has LibreOffice draw its
// preview and the layout rules read it, and stores it.
func (s Specialist) saveSlideDeck(ctx context.Context, owner Owner, toolCallID string, args saveArguments) (string, error) {
	var problems []string
	title := checkTitle(&problems, args.Title)
	if args.Cards != nil || args.Questions != nil || args.Markdown != nil || args.Path != nil || args.Representation != nil {
		problems = append(problems, "a deck is saved from deck alone; remove cards, questions, markdown, path and markdown_representation")
	}
	if len(args.Deck) == 0 || string(args.Deck) == "null" {
		problems = append(problems, "deck is missing: the deck's content as an object with title, theme and slides")
	}
	if len(problems) > 0 {
		return "", refusal("the deck was not saved", problems)
	}
	deck, err := deckspec.Decode(args.Deck)
	if err != nil {
		return "", refusal("the deck was not saved", []string{err.Error()})
	}
	if deck, err = deckspec.Check(deck, deckspec.Options{MinSlides: deckMinSlides, MaxSlides: deckMaxSlides}); err != nil {
		var found deckspec.Problems
		if errors.As(err, &found) {
			return "", refusal("the deck was not saved", found)
		}
		return "", err
	}
	scenes, fitProblems := deckrender.Layout(deck)
	if len(fitProblems) > 0 {
		words := make([]string, len(fitProblems))
		for i, p := range fitProblems {
			words[i] = p.String()
		}
		return "", refusal("the deck was not saved", words)
	}
	data, err := deckrender.PPTX(deck.Title, scenes, time.Now())
	if err == nil {
		if checked := checkPPTX(data); len(checked.findings) > 0 {
			err = errors.New(strings.Join(checked.findings, "; "))
		}
	}
	if err != nil {
		// The renderer wrote a file that does not read back: a fault here,
		// never the model's, and not one it can work around.
		s.Saver.Logger.ErrorContext(ctx, "a drawn deck is not a valid file", "error", err.Error(), "user_id", owner.UserID)
		s.Failures.note(FailureDrawing, err)
		return "", errors.New("the deck could not be drawn because of a fault in this application; it was not saved")
	}
	preview, findings, err := s.previewDeck(ctx, owner, data, len(scenes))
	if err != nil {
		return "", err
	}
	if len(findings) > 0 {
		s.Saver.Logger.WarnContext(ctx, "a drawn deck broke the layout rules in LibreOffice's rendering; it is kept, marked unverified",
			slog.String("user_id", owner.UserID), slog.String("theme", deck.Theme), slog.Any("findings", findings))
	}
	saved, err := s.Saver.SaveDocument(ctx, owner, toolCallID, BuiltDocument{
		Format: FormatPPTX, Title: title, Markdown: deckspec.Markdown(deck), Data: data, Preview: preview, LayoutFindings: findings,
	})
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		s.Saver.Logger.ErrorContext(ctx, "a deck could not be saved", "error", err.Error(), "user_id", owner.UserID)
		s.Failures.note(FailureStorage, err)
		return "", errors.New("the artifact could not be saved because of a storage error; it was not saved")
	}
	out, err := json.Marshal(saved)
	return string(out), err
}
