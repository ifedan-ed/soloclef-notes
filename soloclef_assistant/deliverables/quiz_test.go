package deliverables

import (
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
)

func at(i int) *int { return &i }

// question is a sound question, distinct for each n.
func question(n int) Question {
	id := strconv.Itoa(n)
	return Question{
		Text:        "What is fact " + id + "?",
		Options:     []string{"Alpha " + id, "Beta " + id, "Gamma " + id, "Delta " + id},
		Correct:     at(n % 4),
		Explanation: "Because of " + id + ".",
		Rationales:  []string{"Why alpha.", "Why beta.", "Why gamma.", "Why delta."},
	}
}

func questions(n int) []Question {
	out := make([]Question, n)
	for i := range out {
		out[i] = question(i + 1)
	}
	return out
}

// with is five sound questions whose last has been changed.
func with(change func(*Question)) []Question {
	out := questions(5)
	change(&out[4])
	return out
}

func TestAQuizIsCheckedAgainstTheWholeSchemaAtOnce(t *testing.T) {
	good := questions(5)
	good[0].Text, good[0].Options[2] = "  What is \\(x^2\\)?  ", " Gamma 1 "
	quiz, err := NewQuiz(" Algebra ", good)
	if err != nil {
		t.Fatal(err)
	}
	if quiz.Title != "Algebra" || quiz.SchemaVersion != 1 || quiz.Questions[0].Text != `What is \(x^2\)?` || quiz.Questions[0].Options[2] != "Gamma 1" {
		t.Fatalf("quiz = %+v", quiz)
	}
	// Checking never writes to what it was given: the model's arguments
	// are also what the loop keeps as the call's record.
	if good[0].Options[2] != " Gamma 1 " {
		t.Error("the caller's options were trimmed in place")
	}

	for name, c := range map[string]struct {
		title     string
		questions []Question
		says      string
	}{
		"too few questions":  {"T", questions(4), "5 to 30 questions; this one has 4"},
		"too many questions": {"T", questions(31), "this one has 31"},
		"no title":           {" ", questions(5), "the title is empty"},
		"three options":      {"T", with(func(q *Question) { q.Options = q.Options[:3] }), "question 5: options must be exactly 4; there are 3"},
		"five options":       {"T", with(func(q *Question) { q.Options = append(q.Options, "Epsilon") }), "there are 5"},
		"an empty option":    {"T", with(func(q *Question) { q.Options[1] = " " }), "question 5: option 2 is empty"},
		"two options the same, in another case and spacing": {"T", with(func(q *Question) { q.Options[3] = "ALPHA  5" }), "question 5: option 4 is the same as option 1"},
		"all of the above":           {"T", with(func(q *Question) { q.Options[3] = "All of the above." }), `question 5: option 4 is "All of the above."; write a real choice instead`},
		"no answer named":            {"T", with(func(q *Question) { q.Correct = nil }), "question 5: correct_option_index is missing"},
		"an answer past the options": {"T", with(func(q *Question) { q.Correct = at(4) }), "correct_option_index is 4; it is the place of the right option, counted from 0 to 3"},
		"a negative answer":          {"T", with(func(q *Question) { q.Correct = at(-1) }), "correct_option_index is -1"},
		"no explanation":             {"T", with(func(q *Question) { q.Explanation = "" }), "question 5: explanation_text is empty"},
		"no rationales":              {"T", with(func(q *Question) { q.Rationales = nil }), "question 5: option_rationales must be exactly 4, one per option in option order; there are 0"},
		"an empty rationale":         {"T", with(func(q *Question) { q.Rationales[2] = "" }), "question 5: rationale 3 is empty"},
		"a rationale too long":       {"T", with(func(q *Question) { q.Rationales[0] = strings.Repeat("r", 2001) }), "rationale 1 is longer than 2000"},
		"a question too long":        {"T", with(func(q *Question) { q.Text = strings.Repeat("q", 4001) }), "question_text is longer than 4000"},
		"an explanation too long":    {"T", with(func(q *Question) { q.Explanation = strings.Repeat("e", 12001) }), "explanation_text is longer than 12000"},
		"a control character":        {"T", with(func(q *Question) { q.Options[0] = "a\x00b" }), "option 1 contains a control character (U+0000)"},
		"mathematics left open":      {"T", with(func(q *Question) { q.Explanation = `so \[ x = 1` }), `explanation_text has a LaTeX delimiter \[ that is never closed`},
		"the same question twice":    {"T", with(func(q *Question) { q.Text = "what is  FACT 1?" }), "question 5 has the same text as question 1"},
	} {
		_, err := NewQuiz(c.title, c.questions)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// A limit is counted in characters, not bytes: 4000 two-byte letters fit.
	if _, err := NewQuiz("T", with(func(q *Question) { q.Text = strings.Repeat("é", 4000) })); err != nil {
		t.Errorf("a question of exactly 4000 characters: %v", err)
	}
	// Every problem in one answer, and a flood of them cut short.
	_, err = NewQuiz("", make([]Question, 6))
	for _, says := range []string{"the quiz was not saved: the title is empty", "question 1: question_text is empty", "and ", " more problems"} {
		if !strings.Contains(err.Error(), says) {
			t.Errorf("missing %q in %v", says, err)
		}
	}
}

// The file is what internal/artifacts and the web viewer read: this exact
// shape, every key the viewer's strict schema names and no other.
func TestTheQuizFileAndItsMarkdown(t *testing.T) {
	list := questions(5)
	list[0] = Question{
		Text: "Is a < b?", Options: []string{"Yes", "No", "Sometimes", "Never"}, Correct: at(0),
		Explanation: `Since \(a < b\).`, Rationales: []string{"It is.", "It is not.", "Always.", "Always so."},
	}
	quiz, err := NewQuiz("Order", list)
	if err != nil {
		t.Fatal(err)
	}
	file, err := quiz.File()
	if err != nil {
		t.Fatal(err)
	}
	wantStart := `{"schema_version":1,"title":"Order","questions":[{"question_text":"Is a < b?","options":["Yes","No","Sometimes","Never"],"correct_option_index":0,` +
		`"explanation_text":"Since \\(a < b\\).","option_rationales":["It is.","It is not.","Always.","Always so."]},{"question_text":"What is fact 2?"`
	if !strings.HasPrefix(string(file), wantStart) || strings.HasSuffix(string(file), "\n") {
		t.Fatalf("file\n got %s\nwant %s...", file, wantStart)
	}
	// Read back the way the viewer's schema reads it: no key it does not
	// know, and an index that is a whole number.
	var read struct {
		SchemaVersion int `json:"schema_version"`
		Title         string
		Questions     []map[string]json.RawMessage
	}
	decoder := json.NewDecoder(strings.NewReader(string(file)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&read); err != nil || read.SchemaVersion != 1 || len(read.Questions) != 5 {
		t.Fatalf("read back: %+v, %v", read, err)
	}
	for i, q := range read.Questions {
		if len(q) != 5 || string(q["correct_option_index"]) != strconv.Itoa(*quiz.Questions[i].Correct) {
			t.Errorf("question %d has keys %v", i+1, q)
		}
	}
	markdown := quiz.Markdown()
	want := "# Order\n\n## Question 1\n\nIs a < b?\n\n### Options\n\n- A. Yes\n- B. No\n- C. Sometimes\n- D. Never\n\n### Correct answer\n\nA. Yes\n\n" +
		"### Explanation\n\nSince \\(a < b\\).\n\n### Why each option\n\n- A. It is.\n- B. It is not.\n- C. Always.\n- D. Always so.\n\n## Question 2\n"
	if !strings.HasPrefix(markdown, want) || strings.Count(markdown, "### Correct answer") != 5 {
		t.Fatalf("markdown:\n%s", markdown)
	}
}

func quizArgs(t testing.TB, title string, list []Question) string {
	t.Helper()
	args, err := json.Marshal(map[string]any{"format": "quiz", "title": title, "questions": list})
	if err != nil {
		t.Fatal(err)
	}
	return string(args)
}

// The specialist handed a quiz: it loads the quiz's instructions, has a bad
// quiz refused with what is wrong, and saves the corrected one as a quiz.
func TestTheSpecialistMakesAQuizEndToEnd(t *testing.T) {
	l := &ledger{}
	// A question with no answer named: the arguments simply lack the key.
	unanswered := strings.Replace(quizArgs(t, "Cells", questions(5)), `"correct_option_index":1,`, ``, 1)
	script := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "i1", Name: InstructionsTool, Args: `{"artifact_type":"quiz"}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "s1", Name: SaveTool, Args: unanswered}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "s2", Name: SaveTool, Args: quizArgs(t, "Cells", questions(6))}}},
		modeltest.Reply{Text: "Saved a quiz of 6 questions titled Cells."},
	)
	s, files := specialist(t, script, l)
	owner := Owner{ThreadID: 3, UserID: "u-1"}
	delegate, err := s.Delegate(owner)
	if err != nil {
		t.Fatal(err)
	}
	var saved string
	result, err := delegate.Agent.Start(context.Background(), "t", []model.Message{model.User("Make a quiz on cells from: ...")}, func(ev event.Event) {
		if ev.Kind == event.ToolResult && ev.Tool == SaveTool && !ev.IsError {
			saved = ev.Text
		}
	})
	if err != nil || result.Answer != "Saved a quiz of 6 questions titled Cells." {
		t.Fatalf("result %+v, err %v", result, err)
	}
	requests := script.Requests()
	if told := requests[1].Messages[len(requests[1].Messages)-1].Content; !strings.Contains(told, `format "quiz"`) || !strings.Contains(told, "option_rationales is required") {
		t.Errorf("the quiz instructions were %q", told)
	}
	if told := requests[2].Messages[len(requests[2].Messages)-1].Content; !strings.Contains(told, "question 1: correct_option_index is missing") {
		t.Errorf("after a question with no answer the model was told %q", told)
	}
	if len(l.created) != 1 {
		t.Fatalf("created = %d artifacts", len(l.created))
	}
	made := l.created[0]
	want := `{"status":"saved","artifact_id":41,"generation":1,"title":"Cells","format":"quiz","files":[{"file_id":241,"role":"primary","filename":"cells.json","mime_type":"application/json","size_bytes":` + strconv.Itoa(len(made.Data)) + `}]}`
	if saved != want {
		t.Fatalf("save result\n got %s\nwant %s", saved, want)
	}
	if made.Owner != owner || made.Format != "quiz" || made.ToolCallID == "" || made.MimeType != "application/json" ||
		!strings.HasPrefix(made.Markdown, "# Cells\n\n## Question 1\n") || string(made.Metadata) != `{"verification": {"verified": true, "reason": null}}` {
		t.Errorf("artifact = %+v", made)
	}
	if l.keys[0] != "artifacts/u-1/41/primary/0123456789abcdef0123456789abcdef.json" {
		t.Errorf("key = %s", l.keys[0])
	}
	body, err := files.Open(context.Background(), l.keys[0])
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(body)
	body.Close()
	var quiz Quiz
	if err := json.Unmarshal(stored, &quiz); err != nil || quiz.SchemaVersion != 1 || len(quiz.Questions) != 6 || *quiz.Questions[1].Correct != 2 {
		t.Fatalf("stored quiz = %s (%v)", stored, err)
	}
	if len(l.indexed) != 1 || l.indexed[0] != 141 {
		t.Errorf("indexing was asked for %v", l.indexed)
	}
}

// What the model may get wrong in the arguments themselves, before any rule
// about a quiz is reached.
func TestAQuizWhoseArgumentsDoNotFit(t *testing.T) {
	l := &ledger{}
	s, _ := specialist(t, modeltest.NewScript(), l)
	save := s.saveTool(Owner{UserID: "u-1"})
	good := quizArgs(t, "T", questions(5))
	for name, c := range map[string]struct{ args, says string }{
		"an answer written as text":    {strings.Replace(good, `"correct_option_index":1`, `"correct_option_index":"1"`, 1), "do not fit the schema"},
		"an answer that is a fraction": {strings.Replace(good, `"correct_option_index":1`, `"correct_option_index":1.5`, 1), "do not fit the schema"},
		"an invented field":            {strings.Replace(good, `"question_text"`, `"difficulty":"hard","question_text"`, 1), "difficulty"},
	} {
		if _, err := save.Run(context.Background(), toolCall(c.args)); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(l.created) != 0 {
		t.Fatal("something was saved")
	}
}
