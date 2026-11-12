// Package folderdeletion is the job that removes a deleted folder's
// documents and then the folders themselves.
//
// # The message
//
// `delete_folder_documents_background`, as the API tier's folder delete
// publishes it (soloclef_api/internal/folders): the documents to remove as
// the one positional argument, and by keyword the folders of the subtree
// (`folder_subtree_ids`) and the account they belong to (`user_id`). The
// route has already marked every document "deleting", so they are out of
// search before this runs.
//
// Every statement names the account: an id in the lists that is not its
// own removes nothing.
//
// # When it fails
//
// A document that cannot be removed now (the vector store or the bucket
// did not answer) is tried again, a few times, inside the job. If it still
// fails, the job stops before the folders are deleted: the folder is still
// there, and deleting it again marks what is left and finishes the job.
package folderdeletion

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
)

// Task is the message's name.
const Task = "delete_folder_documents_background"

// Remover removes one of an account's documents (documentremoval.Removal).
type Remover interface {
	Remove(ctx context.Context, userID string, documentID int64) error
}

// Job runs delete_folder_documents_background messages.
type Job struct {
	Pool    *pgxpool.Pool
	Remover Remover
	// Attempts and Backoff bound the retries of one document.
	Attempts int
	Backoff  time.Duration
}

// Run is the jobs.Handler.
func (j *Job) Run(ctx context.Context, msg *celery.Message) error {
	userID, documents, folders, err := arguments(msg)
	if err != nil {
		return err
	}
	for _, documentID := range documents {
		if err := retry(ctx, j.Attempts, j.Backoff, func() error { return j.Remover.Remove(ctx, userID, documentID) }); err != nil {
			return fmt.Errorf("folderdeletion: stopped before deleting the folders: %w", err)
		}
	}
	if len(folders) == 0 {
		return nil
	}
	// The subtree's own folders cascade from their parents too; naming them
	// all is what makes a subtree whose parent link was cut still go.
	if _, err := j.Pool.Exec(ctx, `DELETE FROM folders WHERE id = ANY($1) AND user_id = $2::uuid`, folders, userID); err != nil {
		return fmt.Errorf("folderdeletion: deleting %d folders: %w", len(folders), err)
	}
	return nil
}

func retry(ctx context.Context, attempts int, backoff time.Duration, work func() error) error {
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err = work(); err == nil || ctx.Err() != nil {
			return err
		}
		if attempt < attempts {
			select {
			case <-time.After(backoff * time.Duration(attempt)):
			case <-ctx.Done():
				return errors.Join(err, ctx.Err())
			}
		}
	}
	return err
}

func arguments(msg *celery.Message) (string, []int64, []int64, error) {
	if len(msg.Args) != 1 {
		return "", nil, nil, fmt.Errorf("folderdeletion: %s takes the document ids as its one positional argument, got %d", Task, len(msg.Args))
	}
	userID, isText := msg.Kwargs["user_id"].(string)
	if !isText || userID == "" {
		return "", nil, nil, errors.New("folderdeletion: the message names no account (user_id)")
	}
	documents, err := ids(msg.Args[0])
	if err != nil {
		return "", nil, nil, fmt.Errorf("folderdeletion: the documents: %w", err)
	}
	var folders []int64
	if listed, found := msg.Kwargs["folder_subtree_ids"]; found && listed != nil {
		if folders, err = ids(listed); err != nil {
			return "", nil, nil, fmt.Errorf("folderdeletion: the folders: %w", err)
		}
	}
	return userID, documents, folders, nil
}

func ids(value any) ([]int64, error) {
	list, isList := value.([]any)
	if !isList {
		return nil, fmt.Errorf("%T is not a list", value)
	}
	out := make([]int64, 0, len(list))
	for _, item := range list {
		number, isNumber := item.(float64)
		if !isNumber || number < 1 || number != math.Trunc(number) || number > math.MaxInt64/2 {
			return nil, fmt.Errorf("%v is not an id", item)
		}
		out = append(out, int64(number))
	}
	return out, nil
}
