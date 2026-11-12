package artifacts

import (
	"net/http"
	"slices"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// flashcardState is one person's progress through one generation of a deck.
//
// On the wire and in the row it is
//
//	{"generation": 3, "marks": {"0": "good", "4": "again"}, "order": [2, 0, 1, 4, 3]}
//
// which is the shape the web app validates (FlashcardStudyStateSchema).
type flashcardState struct {
	generation int64
	// marks is what the person said about a card after turning it over:
	// "good" (knew it) or "again" (did not). A card with no entry has not
	// been seen.
	marks map[int]string
	// order is the cards in the order this person studies them: every index
	// of the deck exactly once. It is the deck's own order until they
	// shuffle.
	order []int
}

// The two marks.
const (
	markGood  = "good"
	markAgain = "again"
)

// What a flashcard write refuses with.
const (
	cardOutsideDeck = "card_index is outside the current flashcard deck"
	orderIncomplete = "order must contain every flashcard index exactly once"
)

// newFlashcardState is the state of somebody who has not started: nothing
// marked, the deck's own order.
func newFlashcardState(generation int64, cards int) flashcardState {
	return flashcardState{generation: generation, marks: map[int]string{}, order: countingFrom0(cards)}
}

func (s flashcardState) render() *pythonjson.Object {
	object := pythonjson.NewObject()
	object.Set("generation", s.generation)
	object.Set("marks", byIndex(s.marks))
	object.Set("order", numberList(s.order))
	return object
}

// readFlashcardState reads one stored state, and accepts it only if it is a
// state of *this* deck: the current generation, and an order that is a
// permutation of exactly this many cards. Marks are filtered rather than
// judged -- one that is not "good" or "again", or sits on a card the deck
// does not have, is dropped and the rest of the state stands.
func readFlashcardState(value any, generation int64, cards int) (flashcardState, bool) {
	stored, isObject := value.(*pythonjson.Object)
	if !isObject || !storedGeneration(stored, generation) {
		return flashcardState{}, false
	}
	order, isList := indices(stored.GetOrNil("order"), cards)
	rawMarks, hasMarks := stored.GetOrNil("marks").(*pythonjson.Object)
	if !isList || !hasMarks || !permutation(order, cards) {
		return flashcardState{}, false
	}
	marks := map[int]string{}
	for index := 0; index < cards; index++ {
		mark, _ := rawMarks.GetOrNil(strconv.Itoa(index)).(string)
		if mark == markGood || mark == markAgain {
			marks[index] = mark
		}
	}
	return flashcardState{generation: generation, marks: marks, order: order}, true
}

// deckSizeIn is how many cards a stored state says its deck has: the length
// of its order, which names every card once. It is for the one caller that
// cannot count the deck itself (the manifest of a deck whose file could not
// be read, studyFileUnread); a state is otherwise always held to the real
// count. A value that is not a state, or an order longer than any deck may
// be, implies no deck: zero.
func deckSizeIn(value any) int {
	stored, isObject := value.(*pythonjson.Object)
	if !isObject {
		return 0
	}
	order, isList := stored.GetOrNil("order").([]any)
	if !isList || len(order) > maxCards {
		return 0
	}
	return len(order)
}

// permutation says the numbers are 0..count-1, each exactly once, in any
// order. This is the duplicate check that matters to someone studying: an
// order naming a card twice shows it twice and never shows another.
func permutation(numbers []int, count int) bool {
	if len(numbers) != count {
		return false
	}
	seen := make([]bool, count)
	for _, number := range numbers {
		if number < 0 || number >= count || seen[number] {
			return false
		}
		seen[number] = true
	}
	return true
}

// flashcardStates is everybody's valid state for the deck as it is now.
func flashcardStates(raw []byte, generation int64, cards int) map[string]flashcardState {
	return readStates(raw, func(value any) (flashcardState, bool) {
		return readFlashcardState(value, generation, cards)
	})
}

// stateOf is one person's state, or a fresh one.
func stateOf(states map[string]flashcardState, userID string, generation int64, cards int) flashcardState {
	if state, studying := states[userID]; studying {
		return state
	}
	return newFlashcardState(generation, cards)
}

// markFlashcard records what the person said about one card, or forgets it
// when mark is nil (the "unmark" a second press of the same button sends).
// Marking is idempotent: the same mark twice is the same state.
func markFlashcard(state flashcardState, cards, cardIndex int, mark *string) (flashcardState, *Refusal) {
	if cardIndex < 0 || cardIndex >= cards {
		return state, &Refusal{Status: http.StatusUnprocessableEntity, Message: cardOutsideDeck}
	}
	if mark == nil {
		delete(state.marks, cardIndex)
	} else {
		state.marks[cardIndex] = *mark
	}
	return state, nil
}

// reorderFlashcards replaces the study order. The marks are kept: they are
// about cards, not about positions.
func reorderFlashcards(state flashcardState, cards int, order []int) (flashcardState, *Refusal) {
	if !permutation(order, cards) {
		return state, &Refusal{Status: http.StatusUnprocessableEntity, Message: orderIncomplete}
	}
	state.order = slices.Clone(order)
	return state, nil
}

// resetFlashcards forgets every mark and keeps the order, so "start over"
// does not also undo a shuffle. It reports whether anything is left worth
// storing: a state with no marks in the deck's own order is the state of
// somebody who never started, and is removed from the row rather than kept.
func resetFlashcards(state flashcardState, cards int) (flashcardState, bool) {
	state.marks = map[int]string{}
	return state, !slices.Equal(state.order, countingFrom0(cards))
}
