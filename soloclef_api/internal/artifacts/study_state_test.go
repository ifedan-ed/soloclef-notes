package artifacts

import (
	"net/http"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// rendered is a state as the JSON the web app is sent.
func rendered(t *testing.T, state interface{ render() *pythonjson.Object }) string {
	t.Helper()
	body, err := pythonjson.Marshal(state.render())
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// stored is a value as it comes back out of the jsonb column.
func stored(t *testing.T, text string) any {
	t.Helper()
	value, err := pythonjson.Decode([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func statusOf(refusal *Refusal) int {
	if refusal == nil {
		return http.StatusOK
	}
	return refusal.Status
}

func TestAStoredFlashcardStateIsReadOnlyIfItFitsTheDeck(t *testing.T) {
	// A deck of four cards at generation 3.
	for name, test := range map[string]struct {
		stored string
		// want is the state as read; empty means it is not accepted.
		want string
	}{
		"a state of this deck": {
			`{"generation": 3, "marks": {"0": "good", "2": "again"}, "order": [3, 1, 0, 2]}`,
			`{"generation":3,"marks":{"0":"good","2":"again"},"order":[3,1,0,2]}`},
		"nothing marked": {
			`{"generation": 3, "marks": {}, "order": [0, 1, 2, 3]}`,
			`{"generation":3,"marks":{},"order":[0,1,2,3]}`},
		"marks that are not marks are dropped and the rest stands": {
			`{"generation": 3, "marks": {"0": "good", "1": "maybe", "2": 1, "3": null, "9": "good", "x": "good"}, "order": [0, 1, 2, 3]}`,
			`{"generation":3,"marks":{"0":"good"},"order":[0,1,2,3]}`},
		"keys nobody knows are not carried along": {
			`{"generation": 3, "marks": {}, "order": [0, 1, 2, 3], "streak": 9}`,
			`{"generation":3,"marks":{},"order":[0,1,2,3]}`},

		"an older generation":             {`{"generation": 2, "marks": {}, "order": [0, 1, 2, 3]}`, ""},
		"a generation that is text":       {`{"generation": "3", "marks": {}, "order": [0, 1, 2, 3]}`, ""},
		"a generation that is a decimal":  {`{"generation": 3.0, "marks": {}, "order": [0, 1, 2, 3]}`, ""},
		"a generation that is a truth":    {`{"generation": true, "marks": {}, "order": [0, 1, 2, 3]}`, ""},
		"no generation":                   {`{"marks": {}, "order": [0, 1, 2, 3]}`, ""},
		"an order for a shorter deck":     {`{"generation": 3, "marks": {}, "order": [0, 1, 2]}`, ""},
		"an order for a longer deck":      {`{"generation": 3, "marks": {}, "order": [0, 1, 2, 3, 4]}`, ""},
		"an order naming a card twice":    {`{"generation": 3, "marks": {}, "order": [0, 1, 1, 3]}`, ""},
		"an order naming a card it lacks": {`{"generation": 3, "marks": {}, "order": [0, 1, 2, 4]}`, ""},
		"an order with a negative":        {`{"generation": 3, "marks": {}, "order": [0, 1, 2, -1]}`, ""},
		"an order of text":                {`{"generation": 3, "marks": {}, "order": ["0", "1", "2", "3"]}`, ""},
		"an order of decimals":            {`{"generation": 3, "marks": {}, "order": [0.0, 1.0, 2.0, 3.0]}`, ""},
		"an order that is not a list":     {`{"generation": 3, "marks": {}, "order": {"0": 0}}`, ""},
		"no order":                        {`{"generation": 3, "marks": {}}`, ""},
		"marks that are a list":           {`{"generation": 3, "marks": [], "order": [0, 1, 2, 3]}`, ""},
		"no marks":                        {`{"generation": 3, "order": [0, 1, 2, 3]}`, ""},
		"a list":                          {`[3]`, ""},
		"null":                            {`null`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			state, valid := readFlashcardState(stored(t, test.stored), 3, 4)
			if !valid {
				if test.want != "" {
					t.Fatalf("not accepted, want %s", test.want)
				}
				return
			}
			if got := rendered(t, state); got != test.want {
				t.Errorf("got %s, want %s", got, test.want)
			}
		})
	}
}

func TestMarkingAFlashcard(t *testing.T) {
	good, again := markGood, markAgain
	for name, test := range map[string]struct {
		before string
		index  int
		mark   *string
		status int
		after  string
	}{
		"the first mark": {
			`{"generation": 3, "marks": {}, "order": [0, 1, 2, 3]}`, 2, &good, 200,
			`{"generation":3,"marks":{"2":"good"},"order":[0,1,2,3]}`},
		"changing a mark": {
			`{"generation": 3, "marks": {"2": "good"}, "order": [0, 1, 2, 3]}`, 2, &again, 200,
			`{"generation":3,"marks":{"2":"again"},"order":[0,1,2,3]}`},
		"the same mark again changes nothing": {
			`{"generation": 3, "marks": {"2": "good"}, "order": [0, 1, 2, 3]}`, 2, &good, 200,
			`{"generation":3,"marks":{"2":"good"},"order":[0,1,2,3]}`},
		"taking a mark off": {
			`{"generation": 3, "marks": {"1": "again", "2": "good"}, "order": [0, 1, 2, 3]}`, 2, nil, 200,
			`{"generation":3,"marks":{"1":"again"},"order":[0,1,2,3]}`},
		"taking off a mark that is not there": {
			`{"generation": 3, "marks": {}, "order": [0, 1, 2, 3]}`, 2, nil, 200,
			`{"generation":3,"marks":{},"order":[0,1,2,3]}`},
		"the order is left alone": {
			`{"generation": 3, "marks": {}, "order": [3, 2, 1, 0]}`, 0, &good, 200,
			`{"generation":3,"marks":{"0":"good"},"order":[3,2,1,0]}`},
		"the last card": {
			`{"generation": 3, "marks": {}, "order": [0, 1, 2, 3]}`, 3, &good, 200,
			`{"generation":3,"marks":{"3":"good"},"order":[0,1,2,3]}`},
		"one past the last card": {
			`{"generation": 3, "marks": {}, "order": [0, 1, 2, 3]}`, 4, &good, 422, ""},
		"a negative card": {
			`{"generation": 3, "marks": {}, "order": [0, 1, 2, 3]}`, -1, &good, 422, ""},
	} {
		t.Run(name, func(t *testing.T) {
			before, valid := readFlashcardState(stored(t, test.before), 3, 4)
			if !valid {
				t.Fatal("the starting state is not one")
			}
			after, refusal := markFlashcard(before, 4, test.index, test.mark)
			if statusOf(refusal) != test.status {
				t.Fatalf("status %d, want %d", statusOf(refusal), test.status)
			}
			if refusal == nil && rendered(t, after) != test.after {
				t.Errorf("got %s, want %s", rendered(t, after), test.after)
			}
		})
	}
}

// Marks come out in card order, not in the order "10" < "2" as text.
func TestMarksAreListedInCardOrder(t *testing.T) {
	state := newFlashcardState(1, 12)
	good := markGood
	for _, index := range []int{10, 2, 11, 0} {
		state, _ = markFlashcard(state, 12, index, &good)
	}
	const want = `{"generation":1,"marks":{"0":"good","2":"good","10":"good","11":"good"},"order":[0,1,2,3,4,5,6,7,8,9,10,11]}`
	if got := rendered(t, state); got != want {
		t.Errorf("got %s", got)
	}
}

func TestOrderingFlashcards(t *testing.T) {
	for name, test := range map[string]struct {
		order  []int
		status int
	}{
		"a shuffle":                {[]int{2, 0, 3, 1}, 200},
		"back to the deck's order": {[]int{0, 1, 2, 3}, 200},
		"a card twice":             {[]int{2, 0, 2, 1}, 422},
		"a card missing":           {[]int{2, 0, 1}, 422},
		"a card too many":          {[]int{0, 1, 2, 3, 4}, 422},
		"the right length, a card the deck lacks": {[]int{0, 1, 2, 4}, 422},
		"a negative": {[]int{0, 1, 2, -3}, 422},
		"nothing":    {nil, 422},
	} {
		t.Run(name, func(t *testing.T) {
			before, _ := readFlashcardState(stored(t,
				`{"generation": 3, "marks": {"1": "good"}, "order": [0, 1, 2, 3]}`), 3, 4)
			after, refusal := reorderFlashcards(before, 4, test.order)
			if statusOf(refusal) != test.status {
				t.Fatalf("status %d, want %d", statusOf(refusal), test.status)
			}
			if refusal != nil {
				return
			}
			// The marks are about cards, and survive.
			if len(after.marks) != 1 || after.marks[1] != markGood {
				t.Errorf("the marks changed: %v", after.marks)
			}
			// And the caller's slice is not aliased.
			test.order[0] = 99
			if after.order[0] == 99 {
				t.Error("the state shares the request's slice")
			}
		})
	}
}

func TestResettingFlashcards(t *testing.T) {
	shuffled, _ := readFlashcardState(stored(t,
		`{"generation": 3, "marks": {"1": "good", "3": "again"}, "order": [3, 1, 0, 2]}`), 3, 4)
	after, keep := resetFlashcards(shuffled, 4)
	if got := rendered(t, after); got != `{"generation":3,"marks":{},"order":[3,1,0,2]}` || !keep {
		t.Errorf("a shuffled deck: %s, kept %v", got, keep)
	}

	inOrder, _ := readFlashcardState(stored(t,
		`{"generation": 3, "marks": {"1": "good"}, "order": [0, 1, 2, 3]}`), 3, 4)
	after, keep = resetFlashcards(inOrder, 4)
	if got := rendered(t, after); got != `{"generation":3,"marks":{},"order":[0,1,2,3]}` || keep {
		t.Errorf("a deck in its own order: %s, kept %v", got, keep)
	}
}

func TestAStoredQuizStateIsReadOnlyIfItIsACoherentRun(t *testing.T) {
	// A quiz of five questions at generation 2.
	for name, test := range map[string]struct {
		stored string
		want   string
	}{
		"a first run, part way": {
			`{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {"0": 3, "2": 0}, "skipped_question_indices": [1]}`,
			`{"generation":2,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{"0":3,"2":0},"skipped_question_indices":[1]}`},
		"a retake of what was missed, keeping the right answers": {
			`{"generation": 2, "mode": "missed", "active_question_indices": [1, 4], "answers": {"0": 3, "2": 0, "3": 1}, "skipped_question_indices": []}`,
			`{"generation":2,"mode":"missed","active_question_indices":[1,4],"answers":{"0":3,"2":0,"3":1},"skipped_question_indices":[]}`},
		"answers that are not answers are dropped and the rest stands": {
			`{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {"0": 4, "1": -1, "2": "1", "3": 2, "7": 1, "4": true}, "skipped_question_indices": []}`,
			`{"generation":2,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{"3":2},"skipped_question_indices":[]}`},

		"an older generation":                    {`{"generation": 1, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {}, "skipped_question_indices": []}`, ""},
		"a mode that does not exist":             {`{"generation": 2, "mode": "some", "active_question_indices": [0, 1, 2, 3, 4], "answers": {}, "skipped_question_indices": []}`, ""},
		"an 'all' run missing a question":        {`{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 3, 4], "answers": {}, "skipped_question_indices": []}`, ""},
		"a run over nothing":                     {`{"generation": 2, "mode": "missed", "active_question_indices": [], "answers": {}, "skipped_question_indices": []}`, ""},
		"a run out of order":                     {`{"generation": 2, "mode": "missed", "active_question_indices": [3, 1], "answers": {}, "skipped_question_indices": []}`, ""},
		"a run naming a question twice":          {`{"generation": 2, "mode": "missed", "active_question_indices": [1, 1], "answers": {}, "skipped_question_indices": []}`, ""},
		"a run naming a question the quiz lacks": {`{"generation": 2, "mode": "missed", "active_question_indices": [1, 5], "answers": {}, "skipped_question_indices": []}`, ""},
		"skipped out of order":                   {`{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {}, "skipped_question_indices": [3, 1]}`, ""},
		"skipped twice":                          {`{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {}, "skipped_question_indices": [1, 1]}`, ""},
		"skipped and answered":                   {`{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {"1": 2}, "skipped_question_indices": [1]}`, ""},
		"skipped a question the quiz lacks":      {`{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {}, "skipped_question_indices": [9]}`, ""},
		"no answers":                             {`{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "skipped_question_indices": []}`, ""},
		"no skipped list":                        {`{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {}}`, ""},
		"text":                                   {`"all"`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			state, valid := readQuizState(stored(t, test.stored), 2, 5)
			if !valid {
				if test.want != "" {
					t.Fatalf("not accepted, want %s", test.want)
				}
				return
			}
			if got := rendered(t, state); got != test.want {
				t.Errorf("got %s, want %s", got, test.want)
			}
		})
	}
}

// quizAt is a five-question quiz state, generation 2.
func quizAt(t *testing.T, text string) quizState {
	t.Helper()
	state, valid := readQuizState(stored(t, text), 2, 5)
	if !valid {
		t.Fatalf("not a state: %s", text)
	}
	return state
}

const (
	freshRun  = `{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {}, "skipped_question_indices": []}`
	midRun    = `{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {"0": 3}, "skipped_question_indices": [1]}`
	missedRun = `{"generation": 2, "mode": "missed", "active_question_indices": [1, 4], "answers": {"0": 3, "2": 0, "3": 1}, "skipped_question_indices": []}`
)

func TestAnsweringAQuestion(t *testing.T) {
	for name, test := range map[string]struct {
		before           string
		question, option int
		status           int
		after            string
	}{
		"the first answer": {freshRun, 2, 1, 200,
			`{"generation":2,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{"2":1},"skipped_question_indices":[]}`},
		"the same answer sent again, as a retry does": {midRun, 0, 3, 200,
			`{"generation":2,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{"0":3},"skipped_question_indices":[1]}`},
		"a different answer to an answered question": {midRun, 0, 2, 409, ""},
		"an answer to a skipped question":            {midRun, 1, 2, 409, ""},
		"a question the quiz lacks":                  {freshRun, 5, 0, 422, ""},
		"a question outside a missed-only run":       {missedRun, 0, 3, 422, ""},
		"a question inside a missed-only run": {missedRun, 4, 2, 200,
			`{"generation":2,"mode":"missed","active_question_indices":[1,4],"answers":{"0":3,"2":0,"3":1,"4":2},"skipped_question_indices":[]}`},
	} {
		t.Run(name, func(t *testing.T) {
			after, refusal := answerQuestion(quizAt(t, test.before), test.question, test.option)
			if statusOf(refusal) != test.status {
				t.Fatalf("status %d, want %d", statusOf(refusal), test.status)
			}
			if refusal == nil && rendered(t, after) != test.after {
				t.Errorf("got %s, want %s", rendered(t, after), test.after)
			}
		})
	}
}

func TestSkippingAQuestion(t *testing.T) {
	for name, test := range map[string]struct {
		before   string
		question int
		status   int
		after    string
	}{
		"a skip, kept in question order": {midRun, 0 + 3, 200,
			`{"generation":2,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{"0":3},"skipped_question_indices":[1,3]}`},
		"a skip before the others": {
			`{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {}, "skipped_question_indices": [2, 4]}`, 0, 200,
			`{"generation":2,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{},"skipped_question_indices":[0,2,4]}`},
		"the same skip again": {midRun, 1, 200,
			`{"generation":2,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{"0":3},"skipped_question_indices":[1]}`},
		"skipping an answered question":        {midRun, 0, 409, ""},
		"a question the quiz lacks":            {freshRun, 5, 422, ""},
		"a question outside a missed-only run": {missedRun, 2, 422, ""},
	} {
		t.Run(name, func(t *testing.T) {
			after, refusal := skipQuestion(quizAt(t, test.before), test.question)
			if statusOf(refusal) != test.status {
				t.Fatalf("status %d, want %d", statusOf(refusal), test.status)
			}
			if refusal == nil && rendered(t, after) != test.after {
				t.Errorf("got %s, want %s", rendered(t, after), test.after)
			}
		})
	}
}

func TestRetakingAQuiz(t *testing.T) {
	// The right answers to the five questions.
	key := []int{3, 1, 0, 1, 2}
	const (
		// Right, wrong, right, skipped, wrong.
		finished = `{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {"0": 3, "1": 0, "2": 0, "4": 0}, "skipped_question_indices": [3]}`
		perfect  = `{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4], "answers": {"0": 3, "1": 1, "2": 0, "3": 1, "4": 2}, "skipped_question_indices": []}`
		// A missed-only run over 1 and 4, finished: 1 now right, 4 skipped.
		secondRun = `{"generation": 2, "mode": "missed", "active_question_indices": [1, 4], "answers": {"0": 3, "1": 1, "2": 0, "3": 1}, "skipped_question_indices": [4]}`
	)
	for name, test := range map[string]struct {
		before, mode string
		status       int
		after        string
	}{
		"everything, from the start": {finished, modeAll, 200,
			`{"generation":2,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{},"skipped_question_indices":[]}`},
		"only what was wrong or skipped; the right answers are kept": {finished, modeMissed, 200,
			`{"generation":2,"mode":"missed","active_question_indices":[1,3,4],"answers":{"0":3,"2":0},"skipped_question_indices":[]}`},
		"a second round of what is still missed": {secondRun, modeMissed, 200,
			`{"generation":2,"mode":"missed","active_question_indices":[4],"answers":{"0":3,"1":1,"2":0,"3":1},"skipped_question_indices":[]}`},
		"everything again after a missed-only run": {secondRun, modeAll, 200,
			`{"generation":2,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{},"skipped_question_indices":[]}`},
		"a perfect score may still be retaken in full": {perfect, modeAll, 200,
			`{"generation":2,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{},"skipped_question_indices":[]}`},
		"a perfect score has nothing missed":     {perfect, modeMissed, 409, ""},
		"a run that is not finished":             {midRun, modeAll, 409, ""},
		"a run that is not finished, missed":     {midRun, modeMissed, 409, ""},
		"a quiz not yet started":                 {freshRun, modeAll, 409, ""},
		"a missed-only run that is not finished": {missedRun, modeMissed, 409, ""},
	} {
		t.Run(name, func(t *testing.T) {
			after, refusal := retakeQuiz(quizAt(t, test.before), key, test.mode)
			if statusOf(refusal) != test.status {
				t.Fatalf("status %d, want %d", statusOf(refusal), test.status)
			}
			if refusal == nil && rendered(t, after) != test.after {
				t.Errorf("got %s, want %s", rendered(t, after), test.after)
			}
		})
	}
}

const (
	otherUser = "99999999-8888-4777-8666-555555555555"
	thirdUser = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

// What is read out of the row is what is written back, so what readStates
// drops is deleted. These are the entries it must and must not drop.
func TestOnlyValidStatesOfRealUsersSurviveAWrite(t *testing.T) {
	raw := []byte(`{` +
		`"` + callerID + `": {"generation": 3, "marks": {"0": "good"}, "order": [0, 1, 2, 3]},` +
		`"` + otherUser + `": {"generation": 3, "marks": {}, "order": [3, 2, 1, 0]},` +
		`"` + thirdUser + `": {"generation": 2, "marks": {"0": "good"}, "order": [0, 1, 2, 3]},` +
		`"AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE": {"generation": 3, "marks": {}, "order": [0, 1, 2, 3]},` +
		`"{` + otherUser + `}": {"generation": 3, "marks": {}, "order": [0, 1, 2, 3]},` +
		`"not-a-user": {"generation": 3, "marks": {}, "order": [0, 1, 2, 3]},` +
		`"": {"generation": 3, "marks": {}, "order": [0, 1, 2, 3]}` +
		`}`)
	states := flashcardStates(raw, 3, 4)
	if len(states) != 2 {
		t.Fatalf("%d states survived: %v", len(states), states)
	}
	encoded, err := writeStates(states, flashcardState.render)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"` + callerID + `":{"generation":3,"marks":{"0":"good"},"order":[0,1,2,3]},` +
		`"` + otherUser + `":{"generation":3,"marks":{},"order":[3,2,1,0]}}`
	if string(encoded) != want {
		t.Errorf("got  %s\nwant %s", encoded, want)
	}

	// Nobody left: the section is removed, not left as an empty object.
	if encoded, err := writeStates(map[string]flashcardState{}, flashcardState.render); encoded != nil || err != nil {
		t.Errorf("an empty set of states was written as %s (%v)", encoded, err)
	}
	// A column that is not an object holds nobody's state.
	for _, raw := range []string{``, `null`, `[]`, `"text"`, `7`, `{not json`} {
		if states := flashcardStates([]byte(raw), 3, 4); len(states) != 0 {
			t.Errorf("%q held %d states", raw, len(states))
		}
	}
}
