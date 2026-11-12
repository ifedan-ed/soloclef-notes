package deliverables

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// The limits of a deck. They are the ones the web viewer enforces when it
// opens the file (rendering/formats/flashcards/schema.ts) and the ones
// internal/artifacts reads a deck under; a deck saved past them would be an
// artifact in the list that no viewer will open.
const (
	minCards      = 5
	maxCards      = 100
	maxFrontChars = 4000
	maxBackChars  = 12000
	maxHintChars  = 2000
)

// Card is one flashcard. A card has no id: it is identified by its place in
// the deck, which is what the study state stored beside the artifact keys on.
type Card struct {
	Front string `json:"front_text"`
	Back  string `json:"back_text"`
	// Hint is left out of the file when empty, never written as null: the
	// viewer's schema takes a string or nothing.
	Hint string `json:"hint_text,omitempty"`
}

// Deck is a flashcard deck as the file stores it.
type Deck struct {
	SchemaVersion int    `json:"schema_version"`
	Title         string `json:"title"`
	Cards         []Card `json:"cards"`
}

// deckSchemaVersion is the only version there is.
const deckSchemaVersion = 1

// NewDeck checks a title and cards and returns the deck, or every problem
// found. All of them at once, because the reader is a model that will fix
// what it is told and submit again: one problem per round trip would spend a
// step on each.
func NewDeck(title string, cards []Card) (Deck, error) {
	var problems []string
	title = checkTitle(&problems, title)
	if len(cards) < minCards || len(cards) > maxCards {
		problems = append(problems, fmt.Sprintf("a deck has %d to %d cards; this one has %d", minCards, maxCards, len(cards)))
	}
	same := newSameness()
	seen := make(map[string]int, len(cards))
	clean := make([]Card, 0, len(cards))
	for i, card := range cards {
		which := "card " + strconv.Itoa(i+1)
		card = Card{Front: strings.TrimSpace(card.Front), Back: strings.TrimSpace(card.Back), Hint: strings.TrimSpace(card.Hint)}
		checkStudyText(&problems, which, "front_text", card.Front, maxFrontChars, true)
		checkStudyText(&problems, which, "back_text", card.Back, maxBackChars, true)
		checkStudyText(&problems, which, "hint_text", card.Hint, maxHintChars, false)
		// Two cards asking the same thing are one card.
		if card.Front != "" {
			key := same.key(card.Front)
			if first, dup := seen[key]; dup {
				problems = append(problems, fmt.Sprintf("%s has the same front as card %d", which, first))
			} else {
				seen[key] = i + 1
			}
		}
		clean = append(clean, card)
	}
	if len(problems) > 0 {
		return Deck{}, refusal("the deck was not saved", problems)
	}
	return Deck{SchemaVersion: deckSchemaVersion, Title: title, Cards: clean}, nil
}

// File is the deck as the bytes that are stored: one JSON object, UTF-8, no
// HTML escaping (a card about "a < b" must read that way in the file).
func (d Deck) File() ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(d); err != nil {
		return nil, err
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}

// Markdown is the deck as text: what is stored as the artifact's document,
// and so what the account's search finds and what the model reads when it
// is asked about the deck later.
func (d Deck) Markdown() string {
	var b strings.Builder
	b.WriteString("# " + d.Title + "\n")
	for i, card := range d.Cards {
		b.WriteString("\n## Card " + strconv.Itoa(i+1) + "\n\n### Front\n\n" + card.Front + "\n\n### Back\n\n" + card.Back + "\n")
		if card.Hint != "" {
			b.WriteString("\n### Hint\n\n" + card.Hint + "\n")
		}
	}
	return b.String()
}
