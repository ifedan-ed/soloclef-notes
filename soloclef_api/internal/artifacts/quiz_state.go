package artifacts

import (
	"net/http"
	"slices"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// quizState is one person's run through one generation of a quiz.
//
// On the wire and in the row it is
//
//	{"generation": 2, "mode": "all", "active_question_indices": [0, 1, 2, 3, 4],
//	 "answers": {"0": 2, "3": 1}, "skipped_question_indices": [1]}
//
// which is the shape the web app validates (QuizStateSchema).
//
// A quiz is taken in *runs*. The first run is every question ("all"). When
// a run is finished the person may retake everything, or only what they got
// wrong or skipped ("missed") -- and that run's `active` is just those
// questions, while the answers to the ones they got right stay in `answers`
// so the final score still counts them.
type quizState struct {
	generation int64
	mode       string
	// active is the questions of the current run, ascending.
	active []int
	// answers is the option chosen for each answered question, 0 to 3.
	answers map[int]int
	// skipped is the questions passed over in this run, ascending. A
	// question is answered or skipped, never both.
	skipped []int
}

// The two kinds of run.
const (
	modeAll    = "all"
	modeMissed = "missed"
)

// What a quiz write refuses with. The 409s are shown to the person by the
// web app, so they say what to do next.
const (
	questionOutsideRun = "question_index is outside the active quiz run"
	alreadySkipped     = "This question was already skipped. Retake the quiz to answer it."
	alreadyAnswered    = "This question was already answered. Retake the quiz to change it."
	answeredNotSkipped = "This question was already answered. Retake the quiz to skip it."
	runUnfinished      = "Finish the current quiz run before retaking it."
	nothingMissed      = "There are no missed questions to retake."
)

// newQuizState is the state of somebody who has not started: a run over
// every question, nothing answered.
func newQuizState(generation int64, questions int) quizState {
	return quizState{
		generation: generation, mode: modeAll, active: countingFrom0(questions),
		answers: map[int]int{}, skipped: []int{},
	}
}

func (s quizState) render() *pythonjson.Object {
	object := pythonjson.NewObject()
	object.Set("generation", s.generation)
	object.Set("mode", s.mode)
	object.Set("active_question_indices", numberList(s.active))
	object.Set("answers", byIndex(s.answers))
	object.Set("skipped_question_indices", numberList(s.skipped))
	return object
}

// readQuizState reads one stored state, and accepts it only if it is a
// coherent run of *this* quiz:
//
//   - the current generation, and a mode that exists;
//   - a run that is not empty, lists real questions, ascending, each once;
//   - an "all" run that really is every question;
//   - skipped questions that are real, ascending, each once, and not also
//     answered.
//
// Answers are filtered rather than judged, as flashcard marks are: one that
// is not 0 to 3, or belongs to a question the quiz does not have, is dropped
// and the rest of the state stands.
func readQuizState(value any, generation int64, questions int) (quizState, bool) {
	stored, isObject := value.(*pythonjson.Object)
	if !isObject || !storedGeneration(stored, generation) {
		return quizState{}, false
	}
	mode, _ := stored.GetOrNil("mode").(string)
	if mode != modeAll && mode != modeMissed {
		return quizState{}, false
	}
	active, activeIsList := indices(stored.GetOrNil("active_question_indices"), questions)
	skipped, skippedIsList := indices(stored.GetOrNil("skipped_question_indices"), questions)
	rawAnswers, hasAnswers := stored.GetOrNil("answers").(*pythonjson.Object)
	if !activeIsList || !skippedIsList || !hasAnswers {
		return quizState{}, false
	}
	if len(active) == 0 || !ascending(active) || !ascending(skipped) {
		return quizState{}, false
	}
	if mode == modeAll && len(active) != questions {
		// Ascending, in range and as long as the quiz: it can only be
		// 0..n-1. Anything shorter is not a run over "all".
		return quizState{}, false
	}
	answers := map[int]int{}
	for index := 0; index < questions; index++ {
		if answer, ok := smallInteger(rawAnswers.GetOrNil(strconv.Itoa(index)), optionsPerQuiz); ok {
			answers[index] = answer
		}
	}
	for _, index := range skipped {
		if _, answered := answers[index]; answered {
			return quizState{}, false
		}
	}
	return quizState{generation: generation, mode: mode, active: active, answers: answers, skipped: skipped}, true
}

// quizSizeIn is how many questions a stored state says its quiz has, for the
// one caller that cannot count them itself (the manifest of a quiz whose
// file could not be read, studyFileUnread).
//
// A run over "all" lists every question, so its length is the count. A run
// over "missed" lists only some, and then the count is not written anywhere:
// what the state does say is that the quiz is at least as long as the
// largest question it names, in its run, its skips or its answers. That is
// the size used -- the smallest quiz the state could be about -- and it
// keeps every answer the state holds. A value that is not a state, or one
// naming a question past the format's limit, implies no quiz: zero.
func quizSizeIn(value any) int {
	stored, isObject := value.(*pythonjson.Object)
	if !isObject {
		return 0
	}
	active, activeIsList := indices(stored.GetOrNil("active_question_indices"), maxQuestions)
	if !activeIsList {
		return 0
	}
	if mode, _ := stored.GetOrNil("mode").(string); mode != modeMissed {
		return min(len(active), maxQuestions)
	}
	skipped, skippedIsList := indices(stored.GetOrNil("skipped_question_indices"), maxQuestions)
	answers, hasAnswers := stored.GetOrNil("answers").(*pythonjson.Object)
	if !skippedIsList || !hasAnswers {
		return 0
	}
	size := 0
	for _, index := range slices.Concat(active, skipped) {
		size = max(size, index+1)
	}
	for _, key := range answers.Keys() {
		// A key that is not a question's number is not an answer, and is
		// dropped by readQuizState whatever the size.
		if index, err := strconv.Atoi(key); err == nil && index >= 0 && index < maxQuestions {
			size = max(size, index+1)
		}
	}
	return size
}

// quizStates is everybody's valid state for the quiz as it is now.
func quizStates(raw []byte, generation int64, questions int) map[string]quizState {
	return readStates(raw, func(value any) (quizState, bool) {
		return readQuizState(value, generation, questions)
	})
}

// runOf is one person's state, or a fresh one.
func runOf(states map[string]quizState, userID string, generation int64, questions int) quizState {
	if state, started := states[userID]; started {
		return state
	}
	return newQuizState(generation, questions)
}

func conflict(message string) *Refusal {
	return &Refusal{Status: http.StatusConflict, Message: message}
}

// answerQuestion records the option chosen for a question of the current
// run.
//
// An answer is final for the run: the quiz shows the right answer and the
// explanation the moment a question is answered, so letting it be changed
// afterwards would let anyone score 100%. Sending the *same* answer again is
// not a change and succeeds -- a request retried after a dropped connection
// must not look like cheating.
func answerQuestion(state quizState, questionIndex, option int) (quizState, *Refusal) {
	if !slices.Contains(state.active, questionIndex) {
		return state, &Refusal{Status: http.StatusUnprocessableEntity, Message: questionOutsideRun}
	}
	if slices.Contains(state.skipped, questionIndex) {
		return state, conflict(alreadySkipped)
	}
	if existing, answered := state.answers[questionIndex]; answered && existing != option {
		return state, conflict(alreadyAnswered)
	}
	state.answers[questionIndex] = option
	return state, nil
}

// skipQuestion passes over a question of the current run. Skipping one
// already skipped changes nothing and succeeds, for the same reason a
// repeated answer does.
func skipQuestion(state quizState, questionIndex int) (quizState, *Refusal) {
	if !slices.Contains(state.active, questionIndex) {
		return state, &Refusal{Status: http.StatusUnprocessableEntity, Message: questionOutsideRun}
	}
	if _, answered := state.answers[questionIndex]; answered {
		return state, conflict(answeredNotSkipped)
	}
	if !slices.Contains(state.skipped, questionIndex) {
		state.skipped = append(slices.Clone(state.skipped), questionIndex)
		slices.Sort(state.skipped)
	}
	return state, nil
}

// runComplete says every question of the current run was answered or
// skipped.
func runComplete(state quizState) bool {
	for _, index := range state.active {
		_, answered := state.answers[index]
		if !answered && !slices.Contains(state.skipped, index) {
			return false
		}
	}
	return true
}

// retakeQuiz starts the next run, which is only offered once the current
// one is finished.
//
// "all" forgets everything. "missed" is why this route needs the answer key
// and is the reason the quiz file is read at all: the next run is every
// question whose stored answer is not the right one -- wrong, skipped, or
// never reached -- and for exactly those the old answer and the skip are
// forgotten. The answers that were right are kept, so the score at the end
// of the new run is out of the whole quiz.
//
// `correct` is the right option for each question, in order; its length is
// the number of questions.
func retakeQuiz(state quizState, correct []int, mode string) (quizState, *Refusal) {
	if !runComplete(state) {
		return state, conflict(runUnfinished)
	}
	if mode == modeAll {
		return newQuizState(state.generation, len(correct)), nil
	}

	missed := make([]int, 0, len(correct))
	for index, right := range correct {
		if answer, answered := state.answers[index]; !answered || answer != right {
			missed = append(missed, index)
		}
	}
	if len(missed) == 0 {
		return state, conflict(nothingMissed)
	}
	answers := map[int]int{}
	for index, answer := range state.answers {
		if !slices.Contains(missed, index) {
			answers[index] = answer
		}
	}
	// Every skipped question has no answer, so every one of them is in
	// `missed` and none is still skipped in the new run.
	return quizState{
		generation: state.generation, mode: modeMissed, active: missed,
		answers: answers, skipped: []int{},
	}, nil
}
