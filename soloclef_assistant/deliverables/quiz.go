package deliverables

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// FormatQuiz is a scored multiple-choice quiz. The string is the one the
// artifacts table and the web viewer registry use.
const FormatQuiz = "quiz"

// The limits of a quiz. They are the ones the web viewer enforces when it
// opens the file (rendering/formats/quiz/schema.ts) and the ones
// internal/artifacts reads a quiz under (thirty questions, four options);
// a quiz saved past them would be an artifact no viewer will open.
const (
	minQuestions      = 5
	maxQuestions      = 30
	quizOptions       = 4
	maxQuestionChars  = 4000
	maxOptionChars    = 4000
	maxExplainChars   = 12000
	maxRationaleChars = 2000
	quizSchemaVersion = 1
)

// Question is one question of a quiz, in the field names of the file.
type Question struct {
	Text    string   `json:"question_text"`
	Options []string `json:"options"`
	// Correct is the place of the right option, 0 to 3. It is a pointer so
	// that a question that does not say is told so: an int would read a
	// missing answer as "the first option", and a quiz that marks the wrong
	// option right teaches the wrong thing with a green tick.
	Correct     *int   `json:"correct_option_index"`
	Explanation string `json:"explanation_text"`
	// Rationales is one short "why" per option, in option order, shown
	// under each option once the question is answered.
	Rationales []string `json:"option_rationales"`
}

// Quiz is a quiz as the file stores it.
type Quiz struct {
	SchemaVersion int        `json:"schema_version"`
	Title         string     `json:"title"`
	Questions     []Question `json:"questions"`
}

// catchAlls are options a question may not offer. "All of the above" makes
// the right answer depend on the order of the options, and the viewer is
// free to be changed to shuffle them; both are also the classic way to pad
// a question that has only three real choices.
var catchAlls = map[string]bool{
	"all of the above": true, "none of the above": true,
	"all of these": true, "none of these": true,
}

// NewQuiz checks a title and questions and returns the quiz, or every
// problem found, all at once for the reason NewDeck gives.
//
// The file format lets a question go without option_rationales (quizzes
// made before they existed have none). A quiz made here must have them: the
// viewer shows them under each option, and a quiz with some questions
// explained per option and others not reads as broken.
func NewQuiz(title string, questions []Question) (Quiz, error) {
	var problems []string
	title = checkTitle(&problems, title)
	if len(questions) < minQuestions || len(questions) > maxQuestions {
		problems = append(problems, fmt.Sprintf("a quiz has %d to %d questions; this one has %d", minQuestions, maxQuestions, len(questions)))
	}
	same := newSameness()
	seen := make(map[string]int, len(questions))
	clean := make([]Question, 0, len(questions))
	for i, question := range questions {
		which := "question " + strconv.Itoa(i+1)
		question.Text = strings.TrimSpace(question.Text)
		question.Explanation = strings.TrimSpace(question.Explanation)
		checkStudyText(&problems, which, "question_text", question.Text, maxQuestionChars, true)
		checkStudyText(&problems, which, "explanation_text", question.Explanation, maxExplainChars, true)

		question.Options = trimmed(question.Options)
		if len(question.Options) != quizOptions {
			problems = append(problems, fmt.Sprintf("%s: options must be exactly %d; there are %d", which, quizOptions, len(question.Options)))
		}
		// Two options a reader would call the same are one option, and
		// then the question has two right answers or three choices.
		offered := make(map[string]int, quizOptions)
		for j, option := range question.Options {
			name := "option " + strconv.Itoa(j+1)
			checkStudyText(&problems, which, name, option, maxOptionChars, true)
			if option == "" {
				continue
			}
			key := same.key(option)
			if catchAlls[strings.TrimRight(key, ".")] {
				problems = append(problems, fmt.Sprintf("%s: %s is %q; write a real choice instead", which, name, option))
			}
			if first, dup := offered[key]; dup {
				problems = append(problems, fmt.Sprintf("%s: %s is the same as option %d", which, name, first))
			} else {
				offered[key] = j + 1
			}
		}

		switch {
		case question.Correct == nil:
			problems = append(problems, which+": correct_option_index is missing")
		case *question.Correct < 0 || *question.Correct >= quizOptions:
			problems = append(problems, fmt.Sprintf("%s: correct_option_index is %d; it is the place of the right option, counted from 0 to %d", which, *question.Correct, quizOptions-1))
		}

		question.Rationales = trimmed(question.Rationales)
		if len(question.Rationales) != quizOptions {
			problems = append(problems, fmt.Sprintf("%s: option_rationales must be exactly %d, one per option in option order; there are %d", which, quizOptions, len(question.Rationales)))
		}
		for j, rationale := range question.Rationales {
			checkStudyText(&problems, which, "rationale "+strconv.Itoa(j+1), rationale, maxRationaleChars, true)
		}

		// Two questions asking the same thing are one question.
		if question.Text != "" {
			key := same.key(question.Text)
			if first, dup := seen[key]; dup {
				problems = append(problems, fmt.Sprintf("%s has the same text as question %d", which, first))
			} else {
				seen[key] = i + 1
			}
		}
		clean = append(clean, question)
	}
	if len(problems) > 0 {
		return Quiz{}, refusal("the quiz was not saved", problems)
	}
	return Quiz{SchemaVersion: quizSchemaVersion, Title: title, Questions: clean}, nil
}

// trimmed is texts with the white space around each removed, in a slice of
// its own so that the caller's arguments are never written to.
func trimmed(texts []string) []string {
	out := make([]string, len(texts))
	for i, text := range texts {
		out[i] = strings.TrimSpace(text)
	}
	return out
}

// File is the quiz as the bytes that are stored: one JSON object, UTF-8, no
// HTML escaping (an option reading "a < b" must read that way in the file).
func (q Quiz) File() ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(q); err != nil {
		return nil, err
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}

// optionLetters label the options in the quiz's text form.
const optionLetters = "ABCD"

// Markdown is the quiz as text: what is stored as the artifact's document,
// and so what the account's search finds and what the model reads when it
// is asked about the quiz later. Options are a list because lines joined by
// one line break are one paragraph in Markdown, which ran A to D together.
func (q Quiz) Markdown() string {
	var b strings.Builder
	b.WriteString("# " + q.Title + "\n")
	for i, question := range q.Questions {
		b.WriteString("\n## Question " + strconv.Itoa(i+1) + "\n\n" + question.Text + "\n\n### Options\n\n")
		for j, option := range question.Options {
			b.WriteString("- " + optionLetters[j:j+1] + ". " + option + "\n")
		}
		right := *question.Correct
		b.WriteString("\n### Correct answer\n\n" + optionLetters[right:right+1] + ". " + question.Options[right] + "\n")
		b.WriteString("\n### Explanation\n\n" + question.Explanation + "\n\n### Why each option\n\n")
		for j, rationale := range question.Rationales {
			b.WriteString("- " + optionLetters[j:j+1] + ". " + rationale + "\n")
		}
	}
	return b.String()
}
