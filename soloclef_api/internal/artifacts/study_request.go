package artifacts

import (
	"math"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// This file reads the body of a study-state write.
//
// The bodies are small and written by one client, the web app's deck and
// quiz viewers, with JSON.stringify:
//
//	PATCH flashcard-progress  {"generation": 3, "card_index": 4, "mark": "good" | "again" | null}
//	PUT   flashcard-order     {"generation": 3, "order": [2, 0, 1, ...]}
//	PUT   quiz-answer         {"generation": 2, "question_index": 0, "selected_option_index": 3}
//	PUT   quiz-skip           {"generation": 2, "question_index": 0}
//	POST  quiz-retake         {"generation": 2, "mode": "all" | "missed"}
//
// They are read strictly: a number is a JSON integer and nothing that could
// be coerced into one. internal/requestbody's readers follow pydantic's lax
// rules (`"3"`, `3.0` and `true` are all 3 there), which exist to forgive
// form posts; a card index is not typed into a form, and a client that sends
// `"3"` has a bug it should hear about.
//
// A key the route does not know is ignored. The Python models refused one,
// which protected nothing and meant the web app could never add a field
// without the server being deployed first.

// What a field is refused with.
const (
	notAWholeNumber = "Input should be a whole number"
	notAList        = "Input should be a list"
	notJSON         = "The request body is not valid JSON"
	markExpected    = "Input should be 'good', 'again' or null"
	modeExpected    = "Input should be 'all' or 'missed'"
)

// studyBody is a decoded body and the list its failures are added to. A
// body that did not decode has no fields; reading from it returns zeros and
// adds nothing, because the one failure that matters -- the body itself --
// is already on the list.
type studyBody struct {
	fields   *pythonjson.Object
	failures *[]errorenvelope.Failure
}

// readStudyBody decodes the request's body.
//
// The bytes come from requestbody.Buffered: cmd/api reads every body-taking
// route's body once, before authentication, and leaves it in the context.
// These routes must therefore be listed in cmd/api's `withBodies`; a route
// that is not listed finds no body here and answers "Field required".
func readStudyBody(ctx huma.Context, failures *[]errorenvelope.Failure) studyBody {
	fields, failure, err := requestbody.Decode(ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	switch {
	case err != nil:
		*failures = append(*failures, errorenvelope.Failure{
			Location: []string{requestbody.BodyRoot}, Message: notJSON,
		})
	case failure != nil:
		*failures = append(*failures, *failure)
	}
	return studyBody{fields: fields, failures: failures}
}

func (b studyBody) reject(message string, path ...string) {
	*b.failures = append(*b.failures, errorenvelope.Failure{
		Location: append([]string{requestbody.BodyRoot}, path...),
		Message:  message,
	})
}

// field is a required field's value. A missing one is refused here.
func (b studyBody) field(name string) (any, bool) {
	if b.fields == nil {
		return nil, false
	}
	value, present := b.fields.Get(name)
	if !present {
		b.reject(requestbody.FieldRequired, name)
	}
	return value, present
}

// integer reads a required whole number in [minimum, maximum].
func (b studyBody) integer(name string, minimum, maximum int64) int64 {
	value, present := b.field(name)
	if !present {
		return 0
	}
	return b.integerAt(value, minimum, maximum, name)
}

// integerAt judges one value at one location: a field's own name, or a
// field and an index for an item of a list.
func (b studyBody) integerAt(value any, minimum, maximum int64, path ...string) int64 {
	digits, isInteger := value.(pythonjson.RawInt)
	if !isInteger {
		b.reject(notAWholeNumber, path...)
		return 0
	}
	number, err := strconv.ParseInt(string(digits), 10, 64)
	switch {
	case err != nil && len(digits) > 0 && digits[0] == '-', err == nil && number < minimum:
		b.reject("Input should be at least "+strconv.FormatInt(minimum, 10), path...)
		return 0
	case err != nil, number > maximum:
		// A number too wide for an int64 is, whatever else it is, more than
		// the maximum.
		b.reject("Input should be at most "+strconv.FormatInt(maximum, 10), path...)
		return 0
	}
	return number
}

// generation reads the generation the client believes the artifact is at.
// It is a positive 32-bit integer in the database; a value outside that
// could never match and is refused as malformed rather than answered with
// "the artifact was revised".
func (b studyBody) generation() int64 {
	return b.integer("generation", 1, math.MaxInt32)
}

// mark reads `mark`: "good", "again", or null to take a mark off. The key
// itself is required -- leaving it out is not the same request as sending
// null, and guessing which was meant would clear a mark by accident.
func (b studyBody) mark() *string {
	value, present := b.field("mark")
	if !present || value == nil {
		return nil
	}
	if mark, isText := value.(string); isText && (mark == markGood || mark == markAgain) {
		return &mark
	}
	b.reject(markExpected, "mark")
	return nil
}

// mode reads `mode`: which questions the next run of a quiz covers.
func (b studyBody) mode() string {
	value, present := b.field("mode")
	if !present {
		return ""
	}
	if mode, isText := value.(string); isText && (mode == modeAll || mode == modeMissed) {
		return mode
	}
	b.reject(modeExpected, "mode")
	return ""
}

// order reads `order`: card indices, at least one and at most as many as a
// deck can have cards. Whether it is a permutation of *this* deck is decided
// later, against the deck (reorderFlashcards); here only its form is.
func (b studyBody) order() []int {
	value, present := b.field("order")
	if !present {
		return nil
	}
	items, isList := value.([]any)
	if !isList {
		b.reject(notAList, "order")
		return nil
	}
	// The length is judged before any item, so a list of a million entries
	// costs one comparison.
	if len(items) == 0 || len(items) > maxCards {
		b.reject("Input should have between 1 and "+strconv.Itoa(maxCards)+" items", "order")
		return nil
	}
	order := make([]int, 0, len(items))
	for index, item := range items {
		order = append(order, int(b.integerAt(item, 0, maxCards-1, "order", strconv.Itoa(index))))
	}
	return order
}
