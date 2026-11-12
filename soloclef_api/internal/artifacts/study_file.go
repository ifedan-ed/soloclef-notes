package artifacts

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
)

// This file reads a study file -- a flashcard deck or a quiz, stored as one
// JSON document -- for the two things a study route needs from it: how many
// cards or questions there are, and, for a quiz, which option of each
// question is right.
//
// # What a study file looks like
//
//	{"schema_version": 1, "title": "...",
//	 "cards": [{"front_text": "...", "back_text": "...", "hint_text": "..."}, ...]}
//
//	{"schema_version": 1, "title": "...",
//	 "questions": [{"question_text": "...", "options": ["..", "..", "..", ".."],
//	                "correct_option_index": 2, "explanation_text": "..."}, ...]}
//
// # What is checked here, and what is not
//
// The Python tier re-ran the *authoring* validator on every study request:
// a full model of the file with a dozen rules -- text lengths, control
// characters, LaTeX delimiters, and a search for duplicate fronts and
// duplicate options after NFKC normalisation and case folding. Those rules
// decide whether the agent may **save** a deck, and they did, once, before
// the file was written; the file is immutable afterwards and its checksum is
// on its row. Checking them again when somebody flips a card protects
// nobody. Its only possible effect is harmful: tighten a rule next year and
// every deck saved under the old one answers 422 to its own owner.
//
// What a study route does depend on is the file's *shape*, because the
// numbers it takes from it bound what is written to the database:
//
//   - it is one JSON object, in this schema version, and nothing follows it;
//   - no key this code reads appears twice -- with two "cards" lists, which
//     one is the deck? (Go's decoder would quietly take the last; the viewer
//     in the browser might take either);
//   - the list is there, is a list of objects, and is not empty;
//   - it is no longer than the format allows (100 cards, 30 questions),
//     which is what keeps one person's stored state to a few hundred bytes;
//   - for a quiz, every question has exactly four options and names one of
//     them as correct.
//
// # How it is read
//
// As a stream of tokens, straight off the file store. A deck's text is
// skipped as it goes by and never assembled, so the memory used is that of
// the longest single string in the file, not of the file.

// The format's limits (app/artifacts/verification/formats).
const (
	maxCards        = 100
	maxQuestions    = 30
	optionsPerQuiz  = 4
	schemaVersion   = "1"
	cardsKey        = "cards"
	questionsKey    = "questions"
	correctIndexKey = "correct_option_index"
	optionsKey      = "options"
	schemaKey       = "schema_version"
)

// studyFileLimit is the largest study file a route will read: fifteen
// megabytes, which is the limit the browser's own deck and quiz viewers put
// on what they will open (FLASHCARDS_MAX_VIEWER_BYTES, QUIZ_MAX_VIEWER_BYTES
// in the web app). A file the viewer refuses cannot be studied, so there is
// nothing to keep state for. A deck at every one of the format's limits is
// about seven megabytes.
const studyFileLimit = 15 << 20

// unreadable is a study file that is not one. Its text is a sentence for
// the person looking at the deck, so it says what is wrong in their terms
// and never quotes the file.
type unreadable struct{ reason string }

func (u unreadable) Error() string { return u.reason }

// errTooLarge is returned by capped when a file runs past studyFileLimit.
var errTooLarge = unreadable{"the file is larger than a study file can be"}

// readDeck counts the cards of a flashcard deck.
//
// The error is an `unreadable` when the file is at fault, and anything else
// when the *read* failed -- the store went away mid-stream -- which a caller
// must not report as a bad deck.
func readDeck(source io.Reader) (int, error) {
	return readStudyFile(source, cardsKey, maxCards, func(decoder *json.Decoder, _ int) error {
		// A card is an object. What is in it is the viewer's business.
		return skipObject(decoder, "card")
	})
}

// readQuiz reads which option of each question is the right one, in
// question order. The length of the result is the number of questions.
func readQuiz(source io.Reader) ([]int, error) {
	correct := make([]int, 0, maxQuestions)
	_, err := readStudyFile(source, questionsKey, maxQuestions,
		func(decoder *json.Decoder, index int) error {
			answer, err := readQuestion(decoder, index)
			if err != nil {
				return err
			}
			correct = append(correct, answer)
			return nil
		})
	if err != nil {
		return nil, err
	}
	return correct, nil
}

// readStudyFile walks the document's top level and hands each element of
// the list under `listKey` to `each`, which must consume exactly that
// element. It returns how many there were.
func readStudyFile(
	source io.Reader,
	listKey string,
	limit int,
	each func(decoder *json.Decoder, index int) error,
) (int, error) {
	decoder := json.NewDecoder(&capped{source: source, left: studyFileLimit})
	// Numbers are kept as their digits: `1` and `1.0` are different answers
	// to "which schema version", and a float64 cannot tell them apart.
	decoder.UseNumber()

	if err := expectOpening(decoder, '{', "the file is not a JSON object"); err != nil {
		return 0, err
	}
	count, listed, versioned := 0, false, false
	seen := map[string]bool{}
	for decoder.More() {
		key, err := nextKey(decoder, seen)
		if err != nil {
			return 0, err
		}
		switch key {
		case schemaKey:
			token, err := nextToken(decoder)
			if err != nil {
				return 0, err
			}
			if number, isNumber := token.(json.Number); !isNumber || number.String() != schemaVersion {
				return 0, unreadable{"the file is not in a schema version this server reads"}
			}
			versioned = true
		case listKey:
			if err := expectOpening(decoder, '[', "the file's "+listKey+" are not a list"); err != nil {
				return 0, err
			}
			for decoder.More() {
				// Checked before the element is read, so a file with a
				// million entries costs the time to read the first hundred
				// and one.
				if count == limit {
					return 0, unreadable{"the file has more than " + strconv.Itoa(limit) + " " + listKey}
				}
				if err := each(decoder, count); err != nil {
					return 0, err
				}
				count++
			}
			if _, err := nextToken(decoder); err != nil { // the closing ]
				return 0, err
			}
			listed = true
		default:
			// `title`, and anything a later writer adds that this route has
			// no use for.
			if err := skipValue(decoder); err != nil {
				return 0, err
			}
		}
	}
	if _, err := nextToken(decoder); err != nil { // the closing }
		return 0, err
	}
	// Nothing may follow the object: two documents in one file is not a
	// study file, however valid the first.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if problem := fileProblem(err); problem != nil {
			return 0, problem
		}
		return 0, unreadable{"the file has content after its end"}
	}

	switch {
	case !versioned:
		return 0, unreadable{"the file does not say which schema version it is"}
	case !listed:
		return 0, unreadable{"the file has no " + listKey}
	case count == 0:
		return 0, unreadable{"the file has no " + listKey}
	}
	return count, nil
}

// readQuestion consumes one question and returns the index of its correct
// option.
func readQuestion(decoder *json.Decoder, index int) (int, error) {
	// People count from one.
	which := "question " + strconv.Itoa(index+1)
	if err := expectOpening(decoder, '{', which+" is not an object"); err != nil {
		return 0, err
	}
	correct, options := -1, -1
	seen := map[string]bool{}
	for decoder.More() {
		key, err := nextKey(decoder, seen)
		if err != nil {
			return 0, err
		}
		switch key {
		case correctIndexKey:
			token, err := nextToken(decoder)
			if err != nil {
				return 0, err
			}
			// A JSON integer from 0 to 3, written as one: not "2", not 2.0,
			// not true. The answer key decides what "missed" means, so it
			// is read exactly or not at all.
			number, isNumber := token.(json.Number)
			value, err := strconv.Atoi(number.String())
			if !isNumber || err != nil || value < 0 || value >= optionsPerQuiz ||
				number.String() != strconv.Itoa(value) {
				return 0, unreadable{which + " does not name one of its four options as correct"}
			}
			correct = value
		case optionsKey:
			if err := expectOpening(decoder, '[', which+"'s options are not a list"); err != nil {
				return 0, err
			}
			options = 0
			for decoder.More() {
				if err := skipValue(decoder); err != nil {
					return 0, err
				}
				options++
			}
			if _, err := nextToken(decoder); err != nil { // the closing ]
				return 0, err
			}
		default:
			if err := skipValue(decoder); err != nil {
				return 0, err
			}
		}
	}
	if _, err := nextToken(decoder); err != nil { // the closing }
		return 0, err
	}
	if options != optionsPerQuiz {
		return 0, unreadable{which + " does not have exactly four options"}
	}
	if correct < 0 {
		return 0, unreadable{which + " does not name one of its four options as correct"}
	}
	return correct, nil
}

// nextKey reads an object's next key and refuses one already seen in the
// same object.
func nextKey(decoder *json.Decoder, seen map[string]bool) (string, error) {
	token, err := nextToken(decoder)
	if err != nil {
		return "", err
	}
	// Inside an object the decoder only ever yields a string here; the
	// assertion is for the reader, not for a case that occurs.
	key, _ := token.(string)
	if seen[key] {
		return "", unreadable{"the file names " + strconv.Quote(key) + " twice in one object"}
	}
	seen[key] = true
	return key, nil
}

// skipObject consumes one value that must be an object.
func skipObject(decoder *json.Decoder, itemName string) error {
	if err := expectOpening(decoder, '{', "a "+itemName+" is not an object"); err != nil {
		return err
	}
	return skipRest(decoder)
}

// skipValue consumes one value of any kind without keeping it.
func skipValue(decoder *json.Decoder) error {
	token, err := nextToken(decoder)
	if err != nil {
		return err
	}
	if delimiter, opens := token.(json.Delim); opens && (delimiter == '{' || delimiter == '[') {
		return skipRest(decoder)
	}
	return nil
}

// skipRest consumes tokens up to and including the one that closes the
// object or list already opened.
func skipRest(decoder *json.Decoder) error {
	for depth := 1; depth > 0; {
		token, err := nextToken(decoder)
		if err != nil {
			return err
		}
		if delimiter, isDelimiter := token.(json.Delim); isDelimiter {
			if delimiter == '{' || delimiter == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

// expectOpening consumes one token, which must be the given opening bracket.
func expectOpening(decoder *json.Decoder, opening json.Delim, otherwise string) error {
	token, err := nextToken(decoder)
	if err != nil {
		return err
	}
	if delimiter, isDelimiter := token.(json.Delim); !isDelimiter || delimiter != opening {
		return unreadable{otherwise}
	}
	return nil
}

// nextToken is decoder.Token with its errors sorted into the two kinds the
// caller reports differently.
func nextToken(decoder *json.Decoder) (json.Token, error) {
	token, err := decoder.Token()
	if err == nil {
		return token, nil
	}
	if problem := fileProblem(err); problem != nil {
		return nil, problem
	}
	return nil, err
}

// fileProblem says whether a decoding error is the file's fault, and words
// it if so. The file is at fault when it is not JSON, when it stops in the
// middle of a value, and when it is too large. Any other error came from the
// reader underneath -- the file store -- and nil is returned so that it is
// passed on as what it is.
func fileProblem(err error) error {
	var syntax *json.SyntaxError
	var already unreadable
	switch {
	case errors.As(err, &already):
		// errTooLarge, on its way back up through the decoder.
		return already
	case errors.As(err, &syntax):
		return unreadable{"the file is not valid JSON"}
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return unreadable{"the file ends before it is complete"}
	}
	return nil
}

// capped passes through at most `left` bytes and then fails with
// errTooLarge, so that a file past the limit is reported as that and not as
// one that "ends before it is complete".
//
// The failure is remembered and given again on every later read. That is
// not tidiness: json.Decoder.More swallows a read error and answers false,
// and the caller's next Token then reads again. A reader that failed once
// and then reported a clean end of file would turn "too large" into
// "truncated".
type capped struct {
	source io.Reader
	left   int64
	over   bool
}

func (c *capped) Read(buffer []byte) (int, error) {
	if c.over {
		return 0, errTooLarge
	}
	if c.left == 0 {
		// Exactly at the limit is allowed; one byte more is not. So the
		// only way to know is to ask for one.
		var probe [1]byte
		read, err := c.source.Read(probe[:])
		if read > 0 {
			c.over = true
			return 0, errTooLarge
		}
		return 0, err
	}
	if int64(len(buffer)) > c.left {
		buffer = buffer[:c.left]
	}
	read, err := c.source.Read(buffer)
	c.left -= int64(read)
	return read, err
}
