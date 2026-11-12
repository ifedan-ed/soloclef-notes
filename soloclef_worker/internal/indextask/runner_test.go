package indextask

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexing"
)

type indexer struct {
	asked  []int64
	result indexing.Result
	err    error
}

func (i *indexer) Index(_ context.Context, documentID int64) (indexing.Result, error) {
	i.asked = append(i.asked, documentID)
	return i.result, i.err
}

func TestTheDocumentIdIsReadFromEitherFormOfTheArgument(t *testing.T) {
	for name, msg := range map[string]*celery.Message{
		"positional": {Task: Task, Args: []any{float64(41)}},
		"keyword":    {Task: Task, Kwargs: map[string]any{"document_id": float64(41)}},
	} {
		job := &indexer{result: indexing.Result{Outcome: indexing.Indexed}}
		var observed []indexing.Outcome
		runner, err := New(job, func(result indexing.Result) { observed = append(observed, result.Outcome) }, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := runner.Run(context.Background(), msg); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if len(job.asked) != 1 || job.asked[0] != 41 {
			t.Errorf("%s: indexed %v", name, job.asked)
		}
		if len(observed) != 1 || observed[0] != indexing.Indexed {
			t.Errorf("%s: observed %v", name, observed)
		}
	}
}

func TestAMessageThatNamesNoDocumentIsRefusedWithoutIndexing(t *testing.T) {
	for name, msg := range map[string]*celery.Message{
		"no argument":         {Task: Task},
		"two arguments":       {Task: Task, Args: []any{float64(1), float64(2)}},
		"both forms":          {Task: Task, Args: []any{float64(1)}, Kwargs: map[string]any{"document_id": float64(1)}},
		"another keyword":     {Task: Task, Kwargs: map[string]any{"id": float64(1)}},
		"a string":            {Task: Task, Args: []any{"41"}},
		"a fraction":          {Task: Task, Args: []any{41.5}},
		"zero":                {Task: Task, Args: []any{float64(0)}},
		"past a 32-bit id":    {Task: Task, Args: []any{float64(1 << 40)}},
		"another task's name": {Task: "process_file_upload", Args: []any{float64(41)}},
	} {
		job := &indexer{}
		runner, _ := New(job, nil, nil)
		err := runner.Run(context.Background(), msg)
		if err == nil || len(job.asked) != 0 {
			t.Errorf("%s: err = %v, indexed %v", name, err, job.asked)
		}
		// Acknowledged, not put back: it would be refused the same way for
		// ever.
		if errors.Is(err, broker.ErrRequeue) {
			t.Errorf("%s: the message would be put back", name)
		}
	}
}

func TestOnlyARetryPutsTheMessageBack(t *testing.T) {
	job := &indexer{result: indexing.Result{Outcome: indexing.Retry}, err: fmt.Errorf("%w: the proxy is down", indexing.ErrRetry)}
	runner, _ := New(job, nil, nil)
	err := runner.Run(context.Background(), &celery.Message{Task: Task, Args: []any{float64(41)}})
	if !errors.Is(err, broker.ErrRequeue) {
		t.Fatalf("err = %v, want the message put back", err)
	}

	// A document that was marked failed is a finished job.
	job = &indexer{result: indexing.Result{Outcome: indexing.Failed}}
	runner, _ = New(job, nil, nil)
	if err := runner.Run(context.Background(), &celery.Message{Task: Task, Args: []any{float64(41)}}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestARunnerNeedsAnIndexer(t *testing.T) {
	if _, err := New(nil, nil, nil); err == nil {
		t.Fatal("a runner with no indexer was built")
	}
}

type readyLog struct{ documents []int64 }

func (r *readyLog) DocumentReady(_ context.Context, documentID int64) {
	r.documents = append(r.documents, documentID)
}

// Only a job that left its document ready tells of it: a document that was
// skipped, failed or handed back has nothing new for a notebook to read.
func TestOnlyADocumentLeftReadyIsToldOf(t *testing.T) {
	for outcome, err := range map[indexing.Outcome]error{
		indexing.Indexed: nil,
		indexing.Skipped: nil,
		indexing.Failed:  nil,
		indexing.Retry:   indexing.ErrRetry,
	} {
		ready := &readyLog{}
		runner, _ := New(&indexer{result: indexing.Result{Outcome: outcome}, err: err}, nil, ready)
		_ = runner.Run(context.Background(), &celery.Message{Task: Task, Args: []any{float64(7)}})
		want := 0
		if outcome == indexing.Indexed {
			want = 1
		}
		if len(ready.documents) != want {
			t.Errorf("%s: told of %v", outcome, ready.documents)
		}
	}
}
