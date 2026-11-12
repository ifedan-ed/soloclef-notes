package artifacts

import (
	"net/http"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// deckStudio is a studio holding a deck of four cards at generation 3.
func deckStudio(t *testing.T) *studio {
	t.Helper()
	s := newStudio(t)
	s.put(t, deckStorageKey, []byte(deckOf(4)))
	s.source = &StudySource{Format: "flashcards", Generation: 3,
		File: &StoredFile{ID: 88, Role: "primary", Backend: "local", Key: deckStorageKey}}
	return s
}

// quizStudio is a studio holding a quiz of five questions at generation 2,
// whose right answers are 3, 1, 0, 1, 2.
func quizStudio(t *testing.T) *studio {
	t.Helper()
	s := newStudio(t)
	s.put(t, deckStorageKey, []byte(quizOf(3, 1, 0, 1, 2)))
	s.source = &StudySource{Format: "quiz", Generation: 2,
		File: &StoredFile{ID: 88, Role: "primary", Backend: "local", Key: deckStorageKey}}
	return s
}

func (s *studio) write(t *testing.T, verb, path, body string) answered {
	t.Helper()
	return s.call(t, principal.MethodSession, verb, path, nil, body)
}

func mine(state string) string { return `{"` + callerID + `":` + state + `}` }

func TestStudyingADeck(t *testing.T) {
	s := deckStudio(t)

	// Mark a card. The answer is the whole state, which the web app puts
	// straight into its manifest.
	got := s.write(t, http.MethodPatch, progressURL, `{"generation": 3, "card_index": 2, "mark": "good"}`)
	const afterOne = `{"generation":3,"marks":{"2":"good"},"order":[0,1,2,3]}`
	if got.status != 200 || string(got.raw) != afterOne {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}
	if got.header.Get("Content-Type") != "application/json" || got.header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("headers %v", got.header)
	}
	if s.sections["flashcards"] != mine(afterOne) {
		t.Errorf("the row holds %s", s.sections["flashcards"])
	}

	// Another, then shuffle; the marks follow their cards.
	s.write(t, http.MethodPatch, progressURL, `{"generation": 3, "card_index": 0, "mark": "again"}`)
	got = s.write(t, http.MethodPut, orderURL, `{"generation": 3, "order": [3, 1, 0, 2]}`)
	const shuffled = `{"generation":3,"marks":{"0":"again","2":"good"},"order":[3,1,0,2]}`
	if got.status != 200 || string(got.raw) != shuffled || s.sections["flashcards"] != mine(shuffled) {
		t.Fatalf("status %d: %s; the row holds %s", got.status, got.raw, s.sections["flashcards"])
	}

	// Take a mark off.
	got = s.write(t, http.MethodPatch, progressURL, `{"generation": 3, "card_index": 2, "mark": null}`)
	if got.status != 200 || string(got.raw) != `{"generation":3,"marks":{"0":"again"},"order":[3,1,0,2]}` {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}

	// Start over: the marks go and the shuffle stays.
	got = s.write(t, http.MethodDelete, resetURL, "")
	const cleared = `{"generation":3,"marks":{},"order":[3,1,0,2]}`
	if got.status != 200 || string(got.raw) != cleared || s.sections["flashcards"] != mine(cleared) {
		t.Fatalf("status %d: %s; the row holds %s", got.status, got.raw, s.sections["flashcards"])
	}

	// Back to the deck's own order with nothing marked is the state of
	// somebody who never started, and nothing is kept for it.
	s.write(t, http.MethodPut, orderURL, `{"generation": 3, "order": [0, 1, 2, 3]}`)
	got = s.write(t, http.MethodDelete, resetURL, "")
	if got.status != 200 || string(got.raw) != `{"generation":3,"marks":{},"order":[0,1,2,3]}` {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}
	if _, kept := s.sections["flashcards"]; kept {
		t.Errorf("an empty state was left on the row: %s", s.sections["flashcards"])
	}
}

// Two people study the same deck on the same row.
func TestOnePersonsStudyDoesNotTouchAnothers(t *testing.T) {
	s := deckStudio(t)
	const theirs = `{"generation":3,"marks":{"1":"good"},"order":[1,0,3,2]}`
	s.sections["flashcards"] = `{"` + otherUser + `":` + theirs + `}`

	got := s.write(t, http.MethodPatch, progressURL, `{"generation": 3, "card_index": 0, "mark": "again"}`)
	// The caller starts fresh; they are not shown the other person's marks.
	if got.status != 200 || string(got.raw) != `{"generation":3,"marks":{"0":"again"},"order":[0,1,2,3]}` {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}
	want := `{"` + callerID + `":{"generation":3,"marks":{"0":"again"},"order":[0,1,2,3]},"` + otherUser + `":` + theirs + `}`
	if s.sections["flashcards"] != want {
		t.Errorf("the row holds %s", s.sections["flashcards"])
	}

	// And a reset removes only the caller.
	s.write(t, http.MethodDelete, resetURL, "")
	if s.sections["flashcards"] != `{"`+otherUser+`":`+theirs+`}` {
		t.Errorf("after the reset the row holds %s", s.sections["flashcards"])
	}
}

// Marks made against an older deck mean nothing against this one.
func TestAStateFromAnOlderGenerationIsStartedAfresh(t *testing.T) {
	s := deckStudio(t)
	s.sections["flashcards"] = mine(`{"generation":2,"marks":{"0":"good","1":"good"},"order":[1,0,3,2]}`)
	got := s.write(t, http.MethodPatch, progressURL, `{"generation": 3, "card_index": 3, "mark": "good"}`)
	if got.status != 200 || string(got.raw) != `{"generation":3,"marks":{"3":"good"},"order":[0,1,2,3]}` {
		t.Errorf("status %d: %s", got.status, got.raw)
	}
}

func TestAFlashcardWriteThatIsRefused(t *testing.T) {
	const mark = `{"generation": 3, "card_index": 0, "mark": "good"}`
	for name, test := range map[string]struct {
		arrange    func(s *studio)
		verb, path string
		body       string
		status     int
		told       string
	}{
		"a card past the end of this deck": {
			nil, http.MethodPatch, progressURL, `{"generation": 3, "card_index": 4, "mark": "good"}`,
			422, "card_index is outside the current flashcard deck"},
		"an order naming a card twice": {
			nil, http.MethodPut, orderURL, `{"generation": 3, "order": [0, 1, 1, 3]}`,
			422, "order must contain every flashcard index exactly once"},
		"an order missing a card": {
			nil, http.MethodPut, orderURL, `{"generation": 3, "order": [0, 1, 2]}`,
			422, "order must contain every flashcard index exactly once"},
		"an order for a longer deck": {
			nil, http.MethodPut, orderURL, `{"generation": 3, "order": [0, 1, 2, 3, 4]}`,
			422, "order must contain every flashcard index exactly once"},
		"the deck was revised while it was open": {
			nil, http.MethodPatch, progressURL, `{"generation": 2, "card_index": 0, "mark": "good"}`,
			409, "This flashcard deck was revised. Refresh before updating it."},
		"a reset of a generation that is gone": {
			nil, http.MethodDelete, progressURL + "?generation=2", "",
			409, "This flashcard deck was revised. Refresh before updating it."},
		"the deck is revised between the read and the lock": {
			func(s *studio) { s.meanwhile = func(s *studio) { s.source.Generation = 4 } },
			http.MethodPatch, progressURL, mark,
			409, "This flashcard deck was revised. Refresh before updating it."},
		"the artifact is deleted between the read and the lock": {
			func(s *studio) { s.meanwhile = func(s *studio) { s.source = nil } },
			http.MethodPatch, progressURL, mark,
			409, "This flashcard deck was revised. Refresh before updating it."},
		"no such artifact": {
			func(s *studio) { s.source = nil }, http.MethodPatch, progressURL, mark,
			404, "Flashcard artifact not found"},
		"an artifact that is not a deck": {
			func(s *studio) { s.source.Format = "quiz" }, http.MethodPatch, progressURL, mark,
			404, "Flashcard artifact not found"},
		"a deck with no file": {
			func(s *studio) { s.source.File = nil }, http.MethodPatch, progressURL, mark,
			422, "This flashcard deck cannot be studied: it has no file."},
		"a deck whose file is not a deck": {
			func(s *studio) { s.put(t, deckStorageKey, []byte(`{"schema_version": 1, "cards": "none"}`)) },
			http.MethodPatch, progressURL, mark,
			422, "This flashcard deck cannot be studied: the file's cards are not a list."},
		"a deck whose file is cut short": {
			func(s *studio) { s.put(t, deckStorageKey, []byte(deckOf(4)[:100])) },
			http.MethodPut, orderURL, `{"generation": 3, "order": [0, 1, 2, 3]}`,
			422, "This flashcard deck cannot be studied: the file ends before it is complete."},
		"a deck whose file is gone from the store": {
			func(s *studio) { s.source.File.Key = "artifacts/7/31/primary/gone.json" },
			http.MethodPatch, progressURL, mark,
			500, "An internal error occurred. Please try again or report this issue if it persists."},
		"a deck stored in a backend this tier does not read": {
			func(s *studio) { s.source.File.Backend = "s3" }, http.MethodPatch, progressURL, mark,
			500, "An internal error occurred. Please try again or report this issue if it persists."},
		"the database goes down before the write": {
			func(s *studio) { s.meanwhile = func(s *studio) { s.databaseDown = true } },
			http.MethodPatch, progressURL, mark,
			500, "An internal error occurred. Please try again or report this issue if it persists."},
	} {
		t.Run(name, func(t *testing.T) {
			s := deckStudio(t)
			const before = `{"generation":3,"marks":{"1":"good"},"order":[0,1,2,3]}`
			s.sections["flashcards"] = mine(before)
			if test.arrange != nil {
				test.arrange(s)
			}
			got := s.write(t, test.verb, test.path, test.body)
			if got.status != test.status || got.message(t) != test.told {
				t.Fatalf("status %d, told %q; want %d, %q", got.status, got.message(t), test.status, test.told)
			}
			// A refused write leaves the row exactly as it was.
			if s.writes != 0 || s.sections["flashcards"] != mine(before) {
				t.Errorf("%d writes; the row holds %s", s.writes, s.sections["flashcards"])
			}
			// The web app reads a 409's reason from `detail`.
			if test.status == 409 && !strings.Contains(string(got.raw), `"detail":"`+test.told+`"`) {
				t.Errorf("no detail in %s", got.raw)
			}
		})
	}
}

// A body is read strictly, and every mistake in it is reported at once.
func TestAStudyBodyThatIsMalformed(t *testing.T) {
	for name, test := range map[string]struct {
		verb, path, body string
		fields           []string
	}{
		"nothing sent": {http.MethodPatch, progressURL, ``,
			[]string{"body: Field required"}},
		"a list": {http.MethodPatch, progressURL, `[1, 2]`,
			[]string{"body: Input should be a valid dictionary or object to extract fields from"}},
		"null": {http.MethodPatch, progressURL, `null`,
			[]string{"body: Field required"}},
		"an empty object": {http.MethodPatch, progressURL, `{}`,
			[]string{"body.generation: Field required", "body.card_index: Field required", "body.mark: Field required"}},
		"mark left out is not the same as null": {http.MethodPatch, progressURL, `{"generation": 3, "card_index": 0}`,
			[]string{"body.mark: Field required"}},
		"numbers as text": {http.MethodPatch, progressURL, `{"generation": "3", "card_index": "0", "mark": "good"}`,
			[]string{"body.generation: Input should be a whole number", "body.card_index: Input should be a whole number"}},
		"numbers as decimals": {http.MethodPatch, progressURL, `{"generation": 3.0, "card_index": 1.5, "mark": "good"}`,
			[]string{"body.generation: Input should be a whole number", "body.card_index: Input should be a whole number"}},
		"numbers as truths": {http.MethodPatch, progressURL, `{"generation": true, "card_index": false, "mark": "good"}`,
			[]string{"body.generation: Input should be a whole number", "body.card_index: Input should be a whole number"}},
		"a generation of zero": {http.MethodPatch, progressURL, `{"generation": 0, "card_index": 0, "mark": "good"}`,
			[]string{"body.generation: Input should be at least 1"}},
		"a generation no artifact could reach": {http.MethodPatch, progressURL, `{"generation": 2147483648, "card_index": 0, "mark": "good"}`,
			[]string{"body.generation: Input should be at most 2147483647"}},
		"a generation past any integer": {http.MethodPatch, progressURL, `{"generation": 99999999999999999999999, "card_index": 0, "mark": "good"}`,
			[]string{"body.generation: Input should be at most 2147483647"}},
		"a negative card": {http.MethodPatch, progressURL, `{"generation": 3, "card_index": -1, "mark": "good"}`,
			[]string{"body.card_index: Input should be at least 0"}},
		"a card past any integer, negative": {http.MethodPatch, progressURL, `{"generation": 3, "card_index": -99999999999999999999999, "mark": "good"}`,
			[]string{"body.card_index: Input should be at least 0"}},
		"a card no deck could have": {http.MethodPatch, progressURL, `{"generation": 3, "card_index": 100, "mark": "good"}`,
			[]string{"body.card_index: Input should be at most 99"}},
		"a mark that is not one": {http.MethodPatch, progressURL, `{"generation": 3, "card_index": 0, "mark": "easy"}`,
			[]string{"body.mark: Input should be 'good', 'again' or null"}},
		"a mark that is a number": {http.MethodPatch, progressURL, `{"generation": 3, "card_index": 0, "mark": 1}`,
			[]string{"body.mark: Input should be 'good', 'again' or null"}},
		"a bad path and a bad body, together": {http.MethodPatch, "/api/v1/artifacts/y/flashcard-progress", `{"generation": 3, "card_index": 0, "mark": "?"}`,
			[]string{
				"path.artifact_id: Input should be a valid integer, unable to parse string as an integer",
				"body.mark: Input should be 'good', 'again' or null"}},

		"no order": {http.MethodPut, orderURL, `{"generation": 3}`,
			[]string{"body.order: Field required"}},
		"an order that is not a list": {http.MethodPut, orderURL, `{"generation": 3, "order": "0,1,2,3"}`,
			[]string{"body.order: Input should be a list"}},
		"an empty order": {http.MethodPut, orderURL, `{"generation": 3, "order": []}`,
			[]string{"body.order: Input should have between 1 and 100 items"}},
		"an order longer than any deck": {http.MethodPut, orderURL, `{"generation": 3, "order": [` + strings.Repeat("0,", 100) + `0]}`,
			[]string{"body.order: Input should have between 1 and 100 items"}},
		"an order with entries that are not cards": {http.MethodPut, orderURL, `{"generation": 3, "order": [0, "1", 2.0, -3, 100]}`,
			[]string{
				"body.order.1: Input should be a whole number",
				"body.order.2: Input should be a whole number",
				"body.order.3: Input should be at least 0",
				"body.order.4: Input should be at most 99"}},

		"a reset with no generation": {http.MethodDelete, progressURL, ``,
			[]string{"query.generation: Field required"}},
		"a reset with a generation that is not a number": {http.MethodDelete, progressURL + "?generation=three", ``,
			[]string{"query.generation: Input should be a valid integer, unable to parse string as an integer"}},

		"an option that does not exist": {http.MethodPut, quizAnswerURL, `{"generation": 2, "question_index": 0, "selected_option_index": 4}`,
			[]string{"body.selected_option_index: Input should be at most 3"}},
		"a question no quiz could have": {http.MethodPut, quizAnswerURL, `{"generation": 2, "question_index": 30, "selected_option_index": 0}`,
			[]string{"body.question_index: Input should be at most 29"}},
		"an answer with nothing in it": {http.MethodPut, quizAnswerURL, `{}`,
			[]string{"body.generation: Field required", "body.question_index: Field required", "body.selected_option_index: Field required"}},
		"a skip with no question": {http.MethodPut, quizSkipURL, `{"generation": 2}`,
			[]string{"body.question_index: Field required"}},
		"a retake with no mode": {http.MethodPost, quizRetakeURL, `{"generation": 2}`,
			[]string{"body.mode: Field required"}},
		"a retake in a mode that does not exist": {http.MethodPost, quizRetakeURL, `{"generation": 2, "mode": "wrong"}`,
			[]string{"body.mode: Input should be 'all' or 'missed'"}},
		"a retake whose mode is null": {http.MethodPost, quizRetakeURL, `{"generation": 2, "mode": null}`,
			[]string{"body.mode: Input should be 'all' or 'missed'"}},
	} {
		t.Run(name, func(t *testing.T) {
			s := deckStudio(t)
			got := s.write(t, test.verb, test.path, test.body)
			if got.status != 422 {
				t.Fatalf("status %d: %s", got.status, got.raw)
			}
			if fields := got.fields(t); strings.Join(fields, "\n") != strings.Join(test.fields, "\n") {
				t.Errorf("got\n  %s\nwant\n  %s", strings.Join(fields, "\n  "), strings.Join(test.fields, "\n  "))
			}
			if s.writes != 0 {
				t.Error("a malformed request wrote to the row")
			}
		})
	}
}

// A field the server does not know is not a reason to refuse a write: the
// web app must be able to send one before the server learns what it is for.
func TestAnUnknownFieldIsIgnored(t *testing.T) {
	s := deckStudio(t)
	got := s.write(t, http.MethodPatch, progressURL,
		`{"generation": 3, "card_index": 1, "mark": "good", "client_time": "2026-03-04T05:06:07Z"}`)
	if got.status != 200 || string(got.raw) != `{"generation":3,"marks":{"1":"good"},"order":[0,1,2,3]}` {
		t.Errorf("status %d: %s", got.status, got.raw)
	}
}

// Bytes that are not JSON at all are refused by the body reader in front of
// every route, before the route runs; this pins that the arrangement still
// holds for these paths.
func TestABodyThatIsNotJSONNeverReachesTheRoute(t *testing.T) {
	s := deckStudio(t)
	got := s.write(t, http.MethodPatch, progressURL, `{"generation": 3,`)
	if got.status != http.StatusUnprocessableEntity || s.writes != 0 {
		t.Errorf("status %d, %d writes; want 422 and none", got.status, s.writes)
	}
}

func TestTakingAQuiz(t *testing.T) {
	s := quizStudio(t)
	answer := func(question, option string) answered {
		return s.write(t, http.MethodPut, quizAnswerURL,
			`{"generation": 2, "question_index": `+question+`, "selected_option_index": `+option+`}`)
	}

	// The right answers are 3, 1, 0, 1, 2. Right, wrong, right; skip; wrong.
	got := answer("0", "3")
	if got.status != 200 || string(got.raw) !=
		`{"generation":2,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{"0":3},"skipped_question_indices":[]}` {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}
	if got.header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("Cache-Control %q", got.header.Get("Cache-Control"))
	}
	answer("1", "0")
	answer("2", "0")

	// Not finished yet: no retake.
	got = s.write(t, http.MethodPost, quizRetakeURL, `{"generation": 2, "mode": "missed"}`)
	if got.status != 409 || got.message(t) != "Finish the current quiz run before retaking it." {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}

	got = s.write(t, http.MethodPut, quizSkipURL, `{"generation": 2, "question_index": 3}`)
	if got.status != 200 || !strings.HasSuffix(string(got.raw), `"skipped_question_indices":[3]}`) {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}
	got = answer("4", "0")
	const finished = `{"generation":2,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{"0":3,"1":0,"2":0,"4":0},"skipped_question_indices":[3]}`
	if got.status != 200 || string(got.raw) != finished || s.sections["quiz"] != mine(finished) {
		t.Fatalf("status %d: %s; the row holds %s", got.status, got.raw, s.sections["quiz"])
	}

	// An answer cannot be changed once given; the same one again is a retry.
	if got := answer("1", "1"); got.status != 409 ||
		got.message(t) != "This question was already answered. Retake the quiz to change it." {
		t.Errorf("status %d: %s", got.status, got.raw)
	}
	if got := answer("1", "0"); got.status != 200 || string(got.raw) != finished {
		t.Errorf("status %d: %s", got.status, got.raw)
	}

	// Retake what was missed: the wrong ones and the skipped one. This is
	// what the answer key in the file is read for.
	got = s.write(t, http.MethodPost, quizRetakeURL, `{"generation": 2, "mode": "missed"}`)
	const second = `{"generation":2,"mode":"missed","active_question_indices":[1,3,4],"answers":{"0":3,"2":0},"skipped_question_indices":[]}`
	if got.status != 200 || string(got.raw) != second || s.sections["quiz"] != mine(second) {
		t.Fatalf("status %d: %s; the row holds %s", got.status, got.raw, s.sections["quiz"])
	}
	// A question got right in the first run is not part of this one.
	if got := answer("0", "3"); got.status != 422 || got.message(t) != "question_index is outside the active quiz run" {
		t.Errorf("status %d: %s", got.status, got.raw)
	}

	// All three right this time, and then there is nothing left to miss.
	answer("1", "1")
	answer("3", "1")
	answer("4", "2")
	got = s.write(t, http.MethodPost, quizRetakeURL, `{"generation": 2, "mode": "missed"}`)
	if got.status != 409 || got.message(t) != "There are no missed questions to retake." {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}
	got = s.write(t, http.MethodPost, quizRetakeURL, `{"generation": 2, "mode": "all"}`)
	if got.status != 200 || string(got.raw) !=
		`{"generation":2,"mode":"all","active_question_indices":[0,1,2,3,4],"answers":{},"skipped_question_indices":[]}` {
		t.Fatalf("status %d: %s", got.status, got.raw)
	}
}

func TestAQuizWriteThatIsRefused(t *testing.T) {
	const answer = `{"generation": 2, "question_index": 0, "selected_option_index": 0}`
	for name, test := range map[string]struct {
		arrange    func(s *studio)
		verb, path string
		body       string
		status     int
		told       string
	}{
		"a question past the end of this quiz": {
			nil, http.MethodPut, quizAnswerURL, `{"generation": 2, "question_index": 5, "selected_option_index": 0}`,
			422, "question_index is outside the active quiz run"},
		"a skip past the end of this quiz": {
			nil, http.MethodPut, quizSkipURL, `{"generation": 2, "question_index": 5}`,
			422, "question_index is outside the active quiz run"},
		"the quiz was revised while it was open": {
			nil, http.MethodPut, quizAnswerURL, `{"generation": 1, "question_index": 0, "selected_option_index": 0}`,
			409, "This quiz was revised. Refresh before updating it."},
		"the quiz is revised between the read and the lock": {
			func(s *studio) { s.meanwhile = func(s *studio) { s.source.Generation = 3 } },
			http.MethodPut, quizSkipURL, `{"generation": 2, "question_index": 0}`,
			409, "This quiz was revised. Refresh before updating it."},
		"no such artifact": {
			func(s *studio) { s.source = nil }, http.MethodPut, quizAnswerURL, answer,
			404, "Quiz artifact not found"},
		"an artifact that is not a quiz": {
			func(s *studio) { s.source.Format = "flashcards" }, http.MethodPost, quizRetakeURL, `{"generation": 2, "mode": "all"}`,
			404, "Quiz artifact not found"},
		"a quiz whose file is a deck": {
			func(s *studio) { s.put(t, deckStorageKey, []byte(deckOf(5))) },
			http.MethodPut, quizAnswerURL, answer,
			422, "This quiz cannot be studied: the file has no questions."},
		"a quiz with a question that has no right answer": {
			func(s *studio) {
				s.put(t, deckStorageKey, []byte(`{"schema_version": 1, "questions": [`+
					`{"options": ["a", "b", "c", "d"], "correct_option_index": 0}, {"options": ["a", "b", "c", "d"]}]}`))
			},
			http.MethodPut, quizAnswerURL, answer,
			422, "This quiz cannot be studied: question 2 does not name one of its four options as correct."},
		"a quiz whose file is gone from the store": {
			func(s *studio) { s.source.File.Key = "artifacts/7/31/primary/gone.json" },
			http.MethodPut, quizAnswerURL, answer,
			500, "An internal error occurred. Please try again or report this issue if it persists."},
	} {
		t.Run(name, func(t *testing.T) {
			s := quizStudio(t)
			if test.arrange != nil {
				test.arrange(s)
			}
			got := s.write(t, test.verb, test.path, test.body)
			if got.status != test.status || got.message(t) != test.told {
				t.Fatalf("status %d, told %q; want %d, %q", got.status, got.message(t), test.status, test.told)
			}
			if s.writes != 0 {
				t.Errorf("a refused write reached the row: %v", s.sections)
			}
		})
	}
}

// A refusal leaves a caller able to read why: the envelope, with the CORS
// headers a browser needs to be shown it.
func TestARefusalCanBeReadByTheBrowser(t *testing.T) {
	s := quizStudio(t)
	got := s.write(t, http.MethodPost, quizRetakeURL, `{"generation": 2, "mode": "all"}`)
	if got.status != 409 || got.header.Get("Access-Control-Allow-Origin") != origin ||
		got.header.Get("X-Request-ID") != "req_test" {
		t.Errorf("status %d, headers %v", got.status, got.header)
	}
	s.source.File.Key = "artifacts/7/31/primary/gone.json"
	failed := s.write(t, http.MethodPost, quizRetakeURL, `{"generation": 2, "mode": "all"}`)
	if failed.status != 500 || failed.header.Get("Access-Control-Allow-Origin") != origin {
		t.Errorf("status %d, headers %v", failed.status, failed.header)
	}
}
