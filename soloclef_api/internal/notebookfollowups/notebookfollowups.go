// Package notebookfollowups serves the question cards under a notebook
// chat's answer:
//
//	POST /api/v1/notebooks/{notebook_id}/followups   {"answer": "..."}
//
// It answers {"suggestions": [...]}: up to three questions the person could
// ask next, written by the account's chat model from the answer and the
// titles of the notebook's ticked sources. Nothing is stored: every answer
// is different and a client asks once per answer.
//
// # What it does better than the Python
//
//   - **A failure is said.** The Python answered an empty list when it had
//     no model, when the model failed and when the model wrote something
//     that was not a list, so "no ideas" and "broken" looked the same. Here
//     a model that cannot be reached is a 503 and a reply that holds no
//     questions is a 502, each with a sentence; an empty list is never
//     invented.
//   - **One statement** checks the notebook is the caller's and reads its
//     source titles, where the Python loaded the notebook, its permissions
//     and the whole source listing.
//   - **The model call is bounded** in time, and in what it is sent: the
//     answer and the titles are cut by characters, never mid-character.
package notebookfollowups

import (
	"net/http"
	"time"
)

// Path is the route.
const Path = "/api/v1/notebooks/{notebook_id}/followups"

// Served is every route as a "METHOD /path" pair.
func Served() []string { return []string{http.MethodPost + " " + Path} }

const (
	notebookAbsent = "Notebook not found"
	// modelUnavailable is said when the account's model cannot be opened or
	// does not answer; the cause is logged, not shown.
	modelUnavailable = "Follow-up questions are not available right now because the model could not be reached. Please try again."
	// noQuestions is said when the model answered something that holds no
	// questions.
	noQuestions = "The model did not answer with follow-up questions. Please try again."

	// The request's limits: the answer as the Python accepted it, and a
	// body generous enough to hold one in JSON escapes.
	maxAnswerChars = 50_000
	maxLocaleChars = 35
	maxBodyBytes   = 512 << 10

	// What the model is shown: the start of the answer says what it is
	// about, and twenty titles name the notebook without filling the
	// prompt.
	shownAnswerChars = 6_000
	shownTitles      = 20
	shownTitleChars  = 200

	// What comes back: three cards fit under an answer, and a question
	// longer than a line and a half is not a card.
	questionCount    = 3
	maxQuestionChars = 160

	// A handful of short questions takes a model a few seconds; one that
	// has not answered in thirty is not going to while the person is still
	// looking at the answer.
	modelTimeout = 30 * time.Second
)
