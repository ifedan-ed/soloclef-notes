package artifacts

import (
	"sort"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// This file is what flashcard state and quiz state have in common: where on
// the artifact's row they are kept, and how the object holding everyone's
// state is read and written.
//
// # Where study state lives
//
// On the artifact's own row, in its `metadata` column, one section per
// format:
//
//	{"flashcards": {"study_by_user": {"<user id>": {...state...}, ...}}}
//	{"quiz":       {"progress_by_user": {"<user id>": {...state...}, ...}}}
//
// A state belongs to one person and to one *generation* of the artifact.
// When the agent revises a deck the generation goes up and the cards are
// different cards, so marks made against the old ones mean nothing; a state
// from another generation is not migrated, it is dropped.

// The sections, and the key under each that maps a user to their state.
const (
	flashcardSection = "flashcards"
	flashcardHolder  = "study_by_user"
	quizSection      = "quiz"
	quizHolder       = "progress_by_user"

	flashcardsFormat = "flashcards"
	quizFormat       = "quiz"
)

// readStates reads the object mapping user id to state, keeping only the
// entries that are still valid for the artifact as it is now.
//
// Everything else is dropped, and since the caller writes back what this
// returns, dropped means deleted: a state from an older generation, a state
// that does not fit the deck (an order of 40 for a deck of 50), an entry
// under a key that is not a user id. That is what bounds the column -- each
// user has at most one small state per artifact, and stale ones are swept
// out by the next person who studies.
func readStates[State any](raw []byte, read func(value any) (State, bool)) map[string]State {
	states := map[string]State{}
	stored, isObject := decodeColumn(raw).(*pythonjson.Object)
	if !isObject {
		return states
	}
	for _, userID := range stored.Keys() {
		if !canonicalUUID(userID) {
			continue
		}
		if state, valid := read(stored.GetOrNil(userID)); valid {
			states[userID] = state
		}
	}
	return states
}

// writeStates renders the object for the database, or nil when nobody has
// any state left -- which the store reads as "remove the section".
//
// The users are written in sorted order. Postgres orders a jsonb object's
// keys itself, so this changes nothing in the row; it makes the bytes this
// function returns the same for the same states, which is what lets a test
// compare them.
func writeStates[State any](states map[string]State, render func(State) *pythonjson.Object) ([]byte, error) {
	if len(states) == 0 {
		return nil, nil
	}
	users := make([]string, 0, len(states))
	for userID := range states {
		users = append(users, userID)
	}
	sort.Strings(users)
	object := pythonjson.NewObject()
	for _, userID := range users {
		object.Set(userID, render(states[userID]))
	}
	return pythonjson.Marshal(object)
}

// canonicalUUID says the text is a UUID exactly as this application writes
// one: 36 characters, lower-case hex, hyphens after 8, 12, 16 and 20 digits.
// A user's state is found by comparing this text, so a second spelling of
// the same id (upper case, braces) would be a second, unreachable state.
func canonicalUUID(text string) bool {
	if len(text) != 36 {
		return false
	}
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch index {
		case 8, 13, 18, 23:
			if character != '-' {
				return false
			}
		default:
			isDigit := character >= '0' && character <= '9'
			isHex := character >= 'a' && character <= 'f'
			if !isDigit && !isHex {
				return false
			}
		}
	}
	return true
}

// smallInteger reads a stored JSON integer that must lie in [0, limit).
// Only an integer written as one counts: 3.0, "3" and true are how a state
// gets corrupted, not how one is written.
func smallInteger(value any, limit int) (int, bool) {
	digits, isInteger := integer(value)
	if !isInteger {
		return 0, false
	}
	number, err := strconv.Atoi(string(digits))
	if err != nil || number < 0 || number >= limit {
		return 0, false
	}
	return number, true
}

// storedGeneration reads a state's generation and says whether it is the
// artifact's current one.
func storedGeneration(state *pythonjson.Object, current int64) bool {
	digits, isInteger := integer(state.GetOrNil("generation"))
	if !isInteger {
		return false
	}
	generation, err := strconv.ParseInt(string(digits), 10, 64)
	return err == nil && generation == current
}

// indices reads a stored list of integers in [0, limit), or reports that
// the value is not one.
func indices(value any, limit int) ([]int, bool) {
	list, isList := value.([]any)
	if !isList {
		return nil, false
	}
	numbers := make([]int, 0, len(list))
	for _, entry := range list {
		number, ok := smallInteger(entry, limit)
		if !ok {
			return nil, false
		}
		numbers = append(numbers, number)
	}
	return numbers, true
}

// ascending says each number is larger than the one before: sorted, and
// with no number twice.
func ascending(numbers []int) bool {
	for index := 1; index < len(numbers); index++ {
		if numbers[index] <= numbers[index-1] {
			return false
		}
	}
	return true
}

// countingFrom0 is 0, 1, ... count-1: the cards in the order the deck has
// them, or every question of a quiz.
func countingFrom0(count int) []int {
	numbers := make([]int, count)
	for index := range numbers {
		numbers[index] = index
	}
	return numbers
}

// numberList renders integers as a JSON array.
func numberList(numbers []int) []any {
	list := make([]any, 0, len(numbers))
	for _, number := range numbers {
		list = append(list, number)
	}
	return list
}

// byIndex renders a sparse index-to-value map as the JSON object the web app
// reads -- `{"0": "good", "3": "again"}` -- with the indices in numeric
// order. (Sorting the keys as text would put "10" before "2".)
func byIndex[Value any](values map[int]Value) *pythonjson.Object {
	keys := make([]int, 0, len(values))
	for index := range values {
		keys = append(keys, index)
	}
	sort.Ints(keys)
	object := pythonjson.NewObject()
	for _, index := range keys {
		object.Set(strconv.Itoa(index), values[index])
	}
	return object
}
