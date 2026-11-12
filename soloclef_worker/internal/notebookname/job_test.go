package notebookname

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
)

type memoryRows struct {
	notebook Notebook
	sources  Sources
	renamed  *Naming
	// personRenamed makes the compare-and-set lose, as a rename by the
	// person while the model was thinking would.
	personRenamed bool
}

func (m *memoryRows) Notebook(context.Context, int64) (Notebook, error) { return m.notebook, nil }
func (m *memoryRows) ReadySources(context.Context, Notebook) (Sources, error) {
	return m.sources, nil
}
func (m *memoryRows) Rename(_ context.Context, _ Notebook, naming Naming) (bool, error) {
	if m.personRenamed {
		return false, nil
	}
	m.renamed = &naming
	return true, nil
}

type replies struct {
	mu      sync.Mutex
	answers []string
	systems []string
}

func (r *replies) Generate(_ context.Context, request model.Request, _ func(model.Chunk)) (model.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.systems = append(r.systems, request.Messages[0].Content)
	answer := r.answers[0]
	r.answers = r.answers[1:]
	return model.Response{Message: model.Message{Content: answer}}, nil
}

func newJob(rows *memoryRows, answers ...string) (*Job, *replies) {
	model := &replies{answers: answers}
	return &Job{Rows: rows, Model: model, CallTimeout: time.Second,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC) }}, model
}

func TestANotebookIsNamedFromItsSourcesInTheirLanguage(t *testing.T) {
	rows := &memoryRows{
		notebook: Notebook{ID: 3, UserID: "u", Title: "Untitled notebook", TitleSource: "auto"},
		sources:  Sources{Ready: 2, First: []excerpt{{title: "sepsis-review.pdf", text: "La sepsis neonatal es una de las causas de muerte en los recién nacidos y de la"}}},
	}
	job, model := newJob(rows, `{"title":"Sepsis neonatal","icon":"🩺"}`)
	if err := job.Run(context.Background(), &celery.Message{Task: Task, Args: []any{float64(3), "source", nil}}); err != nil {
		t.Fatal(err)
	}
	if rows.renamed == nil || rows.renamed.Title != "Sepsis neonatal" || rows.renamed.Icon != "🩺" || rows.renamed.ExpectedRuns != 0 {
		t.Fatalf("renamed %+v", rows.renamed)
	}
	if !strings.Contains(model.systems[0], "entire reply in Spanish") {
		t.Fatalf("the model was not told the language: %s", model.systems[0])
	}
	if !strings.Contains(string(rows.renamed.AutoTitle), `"sources":2`) {
		t.Fatalf("the column does not record what the name used: %s", rows.renamed.AutoTitle)
	}
}

func TestAReplyInTheWrongScriptIsAskedForOnceMore(t *testing.T) {
	rows := &memoryRows{notebook: Notebook{ID: 3, UserID: "u", TitleSource: "auto"}}
	job, model := newJob(rows, `{"title":"新生儿败血症"}`, `{"title":"Neonatal sepsis care"}`)
	if err := job.Run(context.Background(), &celery.Message{Task: Task, Args: []any{float64(3), "question", "How is neonatal sepsis treated in the first week?"}}); err != nil {
		t.Fatal(err)
	}
	if rows.renamed == nil || rows.renamed.Title != "Neonatal Sepsis Care" {
		t.Fatalf("renamed %+v", rows.renamed)
	}
	if len(model.systems) != 2 || !strings.Contains(model.systems[1], "was not in English") {
		t.Fatalf("the second ask did not say what was wrong: %v", model.systems)
	}
}

func TestARenameByThePersonWins(t *testing.T) {
	rows := &memoryRows{notebook: Notebook{ID: 3, UserID: "u", TitleSource: "auto"}, sources: Sources{Ready: 1, First: []excerpt{{title: "a.pdf", text: "text"}}}, personRenamed: true}
	job, _ := newJob(rows, `{"title":"A Topic"}`)
	if err := job.Run(context.Background(), &celery.Message{Task: Task, Args: []any{float64(3), "source", nil}}); err != nil {
		t.Fatal(err)
	}
	if rows.renamed != nil {
		t.Fatal("the automatic name was written over the person's")
	}
}

func TestANotebookThatMayNotNameItselfAsksNoModel(t *testing.T) {
	rows := &memoryRows{notebook: Notebook{ID: 3, UserID: "u", TitleSource: "user"}}
	job, model := newJob(rows)
	if err := job.Run(context.Background(), &celery.Message{Task: Task, Args: []any{float64(3), "question", "anything"}}); err != nil {
		t.Fatal(err)
	}
	if len(model.systems) != 0 {
		t.Fatal("the model was asked to name a notebook the person named")
	}
}

func TestTheMessageIsRead(t *testing.T) {
	for _, args := range [][]any{nil, {"3"}, {float64(3), "later"}, {float64(3), "question", float64(1)}} {
		if _, _, _, err := arguments(&celery.Message{Args: args}); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}
