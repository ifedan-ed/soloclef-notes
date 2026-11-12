package folderdeletion

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
)

type recordingRemover struct {
	removed  []int64
	failures int
}

func (r *recordingRemover) Remove(_ context.Context, _ string, documentID int64) error {
	if r.failures > 0 {
		r.failures--
		return errors.New("the bucket did not answer")
	}
	r.removed = append(r.removed, documentID)
	return nil
}

func message(kwargs map[string]any, documents ...any) *celery.Message {
	return &celery.Message{Task: Task, Args: []any{documents}, Kwargs: kwargs}
}

func TestTheMessageMustNameItsAccount(t *testing.T) {
	job := &Job{Remover: &recordingRemover{}, Attempts: 1}
	err := job.Run(context.Background(), message(map[string]any{"folder_subtree_ids": []any{}}, float64(1)))
	if err == nil {
		t.Fatal("a deletion that names no account was run")
	}
	for _, bad := range []*celery.Message{
		{Args: []any{"1"}, Kwargs: map[string]any{"user_id": "u"}},
		{Args: []any{[]any{float64(0)}}, Kwargs: map[string]any{"user_id": "u"}},
		{Args: []any{[]any{}}, Kwargs: map[string]any{"user_id": "u", "folder_subtree_ids": "3"}},
	} {
		if _, _, _, err := arguments(bad); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}

// A document that fails twice and then goes is retried within the job; one
// that keeps failing stops the job before any folder is deleted, which a
// nil pool would show by panicking.
func TestADocumentThatWillNotGoStopsTheJobBeforeTheFolders(t *testing.T) {
	recovered := &recordingRemover{failures: 2}
	job := &Job{Remover: recovered, Attempts: 3, Backoff: time.Millisecond}
	if err := job.Run(context.Background(), message(map[string]any{"user_id": "u"}, float64(4), float64(5))); err != nil {
		t.Fatal(err)
	}
	if len(recovered.removed) != 2 {
		t.Fatalf("removed %v", recovered.removed)
	}
	stuck := &recordingRemover{failures: 99}
	job = &Job{Remover: stuck, Attempts: 2, Backoff: time.Millisecond}
	if err := job.Run(context.Background(), message(map[string]any{"user_id": "u", "folder_subtree_ids": []any{float64(7)}}, float64(4))); err == nil {
		t.Fatal("a job that could not remove a document reported success")
	}
}
