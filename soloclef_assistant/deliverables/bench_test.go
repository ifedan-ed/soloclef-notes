package deliverables

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
)

// The benchmarks measure what a save costs this process: the checks and the
// encoding. The database and the object store are not in them (the ledger
// and a local directory stand in), because those are measured where they
// live and would otherwise be all that showed.

// fullQuestions is a quiz at the size a real one has: thirty questions with
// a sentence or two in every field and some mathematics.
func fullQuestions() []Question {
	list := questions(30)
	for i := range list {
		list[i].Text += " Consider the reaction \\(6CO_2 + 6H_2O \\rightarrow C_6H_{12}O_6 + 6O_2\\) in the chloroplast."
		list[i].Explanation = strings.Repeat("The light reactions supply the ATP and NADPH the cycle uses. ", 4)
		for j := range list[i].Rationales {
			list[i].Rationales[j] = "This is the stage in which the named product is actually formed, which is what the question asks."
		}
	}
	return list
}

func BenchmarkNewQuiz(b *testing.B) {
	list := fullQuestions()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := NewQuiz("Thirty questions", list); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNewMindMap(b *testing.B) {
	// Sixty nodes over four levels, indented the way a model that ignored
	// the instructions would.
	var written strings.Builder
	written.WriteString("# Photosynthesis\n")
	for branch := 0; branch < 5; branch++ {
		written.WriteString("* Branch of the subject\n")
		for leaf := 0; leaf < 10; leaf++ {
			written.WriteString("    * A leaf with `code` and *emphasis*\n")
		}
	}
	written.WriteString("* One more\n    * Leaf\n        * Deeper\n        * Deeper still\n")
	markdown := written.String()
	b.ReportAllocs()
	for b.Loop() {
		if mindMap, err := NewMindMap("Photosynthesis", markdown); err != nil || mindMap.Nodes != 60 {
			b.Fatal(mindMap.Nodes, err)
		}
	}
}

func BenchmarkLaTeXCheck(b *testing.B) {
	// A long answer with a formula every few sentences: the worst field a
	// deck holds, close to its 12000-character limit.
	text := strings.Repeat(`The rate is \(k[A]^2\) and so \[\frac{d[A]}{dt} = -k[A]^{2}\] follows from it. `, 150)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		if problem := latexProblem(text); problem != "" {
			b.Fatal(problem)
		}
	}
}

// The whole of save_artifact for a quiz as the loop calls it: the arguments
// decoded, the quiz checked, its file and Markdown written, the file stored
// in a local directory (a real write to disk) and the result encoded.
func BenchmarkSaveAQuizThroughTheTool(b *testing.B) {
	l := &ledger{}
	s, _ := specialist(b, modeltest.NewScript(), l)
	// A fresh name per save, as in production: the store writes an object
	// once and refuses to replace it.
	saves := 0
	s.Saver.NewID = func() string {
		saves++
		return fmt.Sprintf("%032x", saves)
	}
	save := s.saveTool(Owner{ThreadID: 1, UserID: "u"})
	call := toolCall(quizArgs(b, "Thirty questions", fullQuestions()))
	b.SetBytes(int64(len(call.Args)))
	b.ReportAllocs()
	for b.Loop() {
		l.created, l.keys, l.indexed = l.created[:0], l.keys[:0], l.indexed[:0]
		if _, err := save.Run(context.Background(), call); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSaveAMindMapThroughTheTool(b *testing.B) {
	l := &ledger{}
	s, _ := specialist(b, modeltest.NewScript(), l)
	save := s.saveTool(Owner{ThreadID: 1, UserID: "u"})
	call := toolCall(`{"format":"mindmap","title":"Photosynthesis","markdown":"# Photosynthesis\n\n- Light reactions\n  - Photosystem II\n  - Photosystem I\n- Calvin cycle\n  - Carbon fixation\n"}`)
	b.ReportAllocs()
	for b.Loop() {
		l.created, l.keys, l.indexed = l.created[:0], l.keys[:0], l.indexed[:0]
		if _, err := save.Run(context.Background(), call); err != nil {
			b.Fatal(err)
		}
	}
}

// Opening a podcast, less its one transaction: the roster read, the last
// brief read, the cast chosen and the brief encoded.
func BenchmarkOpenAPodcast(b *testing.B) {
	rows := &podcastRows{}
	rows.lastBrief = lastBrief(b, `{"language":"en","speakers":[{"voice_id":"kokoro:af_heart"},{"voice_id":"kokoro:am_adam"}]}`)
	open := opener(b, rows, kokoro(b, "af_heart", "am_adam"))
	owner := Owner{ThreadID: 1, UserID: "u"}
	request := PodcastRequest{Title: "Mitosis", Source: strings.Repeat("Mitosis has four phases. ", 400)}
	b.ReportAllocs()
	for b.Loop() {
		rows.created, rows.asked = rows.created[:0], rows.asked[:0]
		if _, err := open.Open(context.Background(), owner, request); err != nil {
			b.Fatal(err)
		}
	}
}

// The card is built for every finished call of every turn that makes an
// artifact, on the path that streams to the browser.
func BenchmarkToolCard(b *testing.B) {
	result := event.Event{Kind: event.ToolResult, Tool: SaveTool, Text: `{"status":"saved","artifact_id":41,"generation":1,"title":"Cells","format":"quiz","files":[{"file_id":241,"role":"primary","filename":"cells.json","mime_type":"application/json","size_bytes":18211}]}`}
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := ToolCard(result); !ok {
			b.Fatal("no card")
		}
	}
}
