package artifacts

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// Every deck and quiz in these tests is written here.

// deckOf is a well-formed deck of n cards.
func deckOf(n int) string {
	cards := make([]string, n)
	for index := range cards {
		cards[index] = fmt.Sprintf(`{"front_text": "Front %d", "back_text": "Back %d", "hint_text": "Hint"}`, index, index)
	}
	return `{"schema_version": 1, "title": "Invented deck", "cards": [` + strings.Join(cards, ", ") + `]}`
}

// quizOf is a well-formed quiz whose right answers are the given options.
func quizOf(correct ...int) string {
	questions := make([]string, len(correct))
	for index, answer := range correct {
		questions[index] = fmt.Sprintf(
			`{"question_text": "Question %d?", "options": ["a", "b", "c", "d"], `+
				`"correct_option_index": %d, "explanation_text": "Because.", `+
				`"option_rationales": ["w", "x", "y", "z"]}`, index, answer)
	}
	return `{"schema_version": 1, "title": "Invented quiz", "questions": [` + strings.Join(questions, ", ") + `]}`
}

func TestReadDeck(t *testing.T) {
	for name, test := range map[string]struct {
		file string
		// cards is the count; zero means the file is refused.
		cards int
	}{
		"five cards":          {deckOf(5), 5},
		"one card":            {deckOf(1), 1},
		"the most a deck has": {deckOf(100), 100},
		"keys in another order, and one nobody knows": {
			`{"cards": [{}, {}], "made_by": {"tool": ["x", {"y": [1, 2]}]}, "title": "T", "schema_version": 1}`, 2},
		"text that looks like structure": {
			`{"schema_version": 1, "cards": [{"front_text": "a ] } \" , [ {", "back_text": "]"}]}`, 1},
		"surrounding whitespace": {"\n  " + deckOf(3) + "\n\n", 3},

		"one card too many":         {deckOf(101), 0},
		"no cards":                  {`{"schema_version": 1, "title": "T", "cards": []}`, 0},
		"cards missing":             {`{"schema_version": 1, "title": "T"}`, 0},
		"cards that are not a list": {`{"schema_version": 1, "cards": {"0": {}}}`, 0},
		"a card that is not a card": {`{"schema_version": 1, "cards": [{}, "front", {}]}`, 0},
		"a card that is a list":     {`{"schema_version": 1, "cards": [[]]}`, 0},
		"cards named twice":         {`{"schema_version": 1, "cards": [{}], "cards": [{}, {}]}`, 0},
		"the version named twice":   {`{"schema_version": 1, "schema_version": 1, "cards": [{}]}`, 0},
		"no schema version":         {`{"title": "T", "cards": [{}]}`, 0},
		"a later schema version":    {`{"schema_version": 2, "cards": [{}]}`, 0},
		"the version as text":       {`{"schema_version": "1", "cards": [{}]}`, 0},
		"the version as a decimal":  {`{"schema_version": 1.0, "cards": [{}]}`, 0},
		"the version as a truth":    {`{"schema_version": true, "cards": [{}]}`, 0},
		"a list, not an object":     {`[{"schema_version": 1, "cards": [{}]}]`, 0},
		"a quiz":                    {quizOf(0, 1, 2, 3, 0), 0},
		"not JSON":                  {`this is a deck, honest`, 0},
		"an unquoted key":           {`{schema_version: 1, "cards": [{}]}`, 0},
		"a trailing comma":          {`{"schema_version": 1, "cards": [{},]}`, 0},
		"cut off in the middle":     {deckOf(5)[:120], 0},
		"cut off before the last }": {strings.TrimSuffix(deckOf(5), "}"), 0},
		"two documents":             {deckOf(5) + deckOf(5), 0},
		"something after the deck":  {deckOf(5) + ` "more"`, 0},
		"a stray bracket after":     {deckOf(5) + `]`, 0},
		"empty":                     {``, 0},
		"only whitespace":           {"  \n ", 0},
		"null":                      {`null`, 0},
	} {
		t.Run(name, func(t *testing.T) {
			cards, err := readDeck(strings.NewReader(test.file))
			if test.cards > 0 {
				if err != nil || cards != test.cards {
					t.Fatalf("read %d cards (%v), want %d", cards, err, test.cards)
				}
				return
			}
			var problem unreadable
			if !errors.As(err, &problem) {
				t.Fatalf("read %d cards (%v), want the file refused as unreadable", cards, err)
			}
			// What the person is told names the fault and never the file's
			// own text.
			if problem.reason == "" || strings.Contains(problem.reason, "Front") {
				t.Errorf("the reason is %q", problem.reason)
			}
		})
	}
}

func TestReadQuiz(t *testing.T) {
	question := func(body string) string {
		return `{"schema_version": 1, "title": "T", "questions": [` + body + `]}`
	}
	const options = `"options": ["a", "b", "c", "d"]`
	for name, test := range map[string]struct {
		file    string
		correct []int
	}{
		"five questions":      {quizOf(0, 3, 1, 2, 2), []int{0, 3, 1, 2, 2}},
		"one question":        {quizOf(3), []int{3}},
		"the most a quiz has": {quizOf(make([]int, 30)...), make([]int, 30)},
		"the answer before the options": {
			question(`{"correct_option_index": 2, ` + options + `}`), []int{2}},
		"options that are not text are still four options": {
			question(`{"options": [1, {"a": [2]}, null, "d"], "correct_option_index": 1}`), []int{1}},

		"one question too many":       {quizOf(make([]int, 31)...), nil},
		"no questions":                {question(``), nil},
		"questions missing":           {`{"schema_version": 1, "title": "T"}`, nil},
		"a deck":                      {deckOf(5), nil},
		"a question that is a list":   {question(`[]`), nil},
		"three options":               {question(`{"options": ["a", "b", "c"], "correct_option_index": 0}`), nil},
		"five options":                {question(`{"options": ["a", "b", "c", "d", "e"], "correct_option_index": 0}`), nil},
		"no options":                  {question(`{"correct_option_index": 0}`), nil},
		"options that are not a list": {question(`{"options": "abcd", "correct_option_index": 0}`), nil},
		"no right answer":             {question(`{` + options + `}`), nil},
		"a right answer of 4":         {question(`{` + options + `, "correct_option_index": 4}`), nil},
		"a negative right answer":     {question(`{` + options + `, "correct_option_index": -1}`), nil},
		"the right answer as text":    {question(`{` + options + `, "correct_option_index": "2"}`), nil},
		"the right answer as 2.0":     {question(`{` + options + `, "correct_option_index": 2.0}`), nil},
		"the right answer as a truth": {question(`{` + options + `, "correct_option_index": true}`), nil},
		"the right answer as null":    {question(`{` + options + `, "correct_option_index": null}`), nil},
		"two right answers":           {question(`{` + options + `, "correct_option_index": 1, "correct_option_index": 2}`), nil},
		"two lists of options":        {question(`{` + options + `, ` + options + `, "correct_option_index": 1}`), nil},
		"the second question is bad":  {question(`{` + options + `, "correct_option_index": 1}, {` + options + `}`), nil},
		"cut off in the middle":       {quizOf(0, 1, 2)[:200], nil},
	} {
		t.Run(name, func(t *testing.T) {
			correct, err := readQuiz(strings.NewReader(test.file))
			if test.correct != nil {
				if err != nil || fmt.Sprint(correct) != fmt.Sprint(test.correct) {
					t.Fatalf("read %v (%v), want %v", correct, err, test.correct)
				}
				return
			}
			var problem unreadable
			if !errors.As(err, &problem) || correct != nil {
				t.Fatalf("read %v (%v), want the file refused as unreadable", correct, err)
			}
		})
	}
}

// The reason says which question, counting as people do.
func TestABadQuestionIsNamedByItsNumber(t *testing.T) {
	file := `{"schema_version": 1, "questions": [` +
		`{"options": ["a", "b", "c", "d"], "correct_option_index": 0}, ` +
		`{"options": ["a", "b", "c", "d"], "correct_option_index": 1}, ` +
		`{"options": ["a", "b"], "correct_option_index": 1}]}`
	_, err := readQuiz(strings.NewReader(file))
	if err == nil || err.Error() != "question 3 does not have exactly four options" {
		t.Errorf("got %v", err)
	}
}

// failing is a file store that goes away mid-read.
type failing struct {
	head string
	err  error
}

func (f *failing) Read(buffer []byte) (int, error) {
	if f.head == "" {
		return 0, f.err
	}
	read := copy(buffer, f.head)
	f.head = f.head[read:]
	return read, nil
}

// "Your deck is malformed" must never be said about a disk that failed.
func TestAStoreFailureIsNotBlamedOnTheFile(t *testing.T) {
	broken := errors.New("input/output error")
	for name, read := range map[string]func(io.Reader) error{
		"a deck": func(source io.Reader) error { _, err := readDeck(source); return err },
		"a quiz": func(source io.Reader) error { _, err := readQuiz(source); return err },
	} {
		t.Run(name, func(t *testing.T) {
			err := read(&failing{head: deckOf(5)[:90], err: broken})
			var problem unreadable
			if !errors.Is(err, broken) || errors.As(err, &problem) {
				t.Errorf("got %v", err)
			}
		})
	}
}

// padding is n spaces, produced without holding them: whitespace between
// JSON tokens, so a file can be made any size and stay valid.
type padding struct{ left int64 }

func (p *padding) Read(buffer []byte) (int, error) {
	if p.left == 0 {
		return 0, io.EOF
	}
	if int64(len(buffer)) > p.left {
		buffer = buffer[:p.left]
	}
	for index := range buffer {
		buffer[index] = ' '
	}
	p.left -= int64(len(buffer))
	return len(buffer), nil
}

func TestAStudyFileHasASizeLimit(t *testing.T) {
	deck := deckOf(5)
	padded := func(total int64) io.Reader {
		return io.MultiReader(&padding{left: total - int64(len(deck))}, strings.NewReader(deck))
	}

	if cards, err := readDeck(padded(studyFileLimit)); err != nil || cards != 5 {
		t.Errorf("a file of exactly the limit: %d cards, %v", cards, err)
	}
	_, err := readDeck(padded(studyFileLimit + 1))
	if !errors.Is(err, errTooLarge) {
		t.Errorf("a file one byte over the limit: %v", err)
	}
	// And one that never ends is given up on at the limit, not read forever.
	_, err = readDeck(&padding{left: 1 << 40})
	if !errors.Is(err, errTooLarge) {
		t.Errorf("an endless file: %v", err)
	}
}

// ---------------------------------------------------------------------------
// `go test ./internal/artifacts/ -bench Study -benchmem`
//
// What a study write pays to learn the size of a deck. The fixture is a deck
// at every one of the format's limits -- 100 cards, each with a 4,000
// character front, a 12,000 character back and a 2,000 character hint: about
// 1.8 MB of JSON, and far larger than any deck the Studio writes.

func largestDeck() string {
	card := `{"front_text": "` + strings.Repeat("f", 4000) + `", "back_text": "` +
		strings.Repeat("b", 12000) + `", "hint_text": "` + strings.Repeat("h", 2000) + `"}`
	cards := make([]string, 100)
	for index := range cards {
		cards[index] = card
	}
	return `{"schema_version": 1, "title": "Largest", "cards": [` + strings.Join(cards, ",") + `]}`
}

func BenchmarkStudyFileLargestDeck(b *testing.B) {
	deck := largestDeck()
	b.SetBytes(int64(len(deck)))
	b.ReportAllocs()
	for b.Loop() {
		if cards, err := readDeck(strings.NewReader(deck)); err != nil || cards != 100 {
			b.Fatal(cards, err)
		}
	}
}

// A deck of the size the Studio actually makes: 30 cards of a sentence each.
func BenchmarkStudyFileOrdinaryDeck(b *testing.B) {
	deck := deckOf(30)
	b.SetBytes(int64(len(deck)))
	b.ReportAllocs()
	for b.Loop() {
		if cards, err := readDeck(strings.NewReader(deck)); err != nil || cards != 30 {
			b.Fatal(cards, err)
		}
	}
}

func BenchmarkStudyFileLargestQuiz(b *testing.B) {
	quiz := quizOf(make([]int, 30)...)
	b.SetBytes(int64(len(quiz)))
	b.ReportAllocs()
	for b.Loop() {
		if correct, err := readQuiz(strings.NewReader(quiz)); err != nil || len(correct) != 30 {
			b.Fatal(correct, err)
		}
	}
}
