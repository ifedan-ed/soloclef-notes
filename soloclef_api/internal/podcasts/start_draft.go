package podcasts

import (
	"context"
	"errors"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// ErrNotAtBriefGate is a podcast StartDraft could not approve: it is not
// the caller's, is not waiting at its brief gate, or has no usable brief.
var ErrNotAtBriefGate = errors.New("podcasts: the podcast is not waiting at its brief gate with a brief to draft from")

// StartDraft approves a podcast's brief and queues its transcript draft,
// for a caller that is not the approve route: a notebook's studio, whose
// dialog has already asked the person everything the brief card would.
//
// It is the approve route's own steps -- the same decision (approve), the
// same guarded move, the same task -- so a podcast started from the studio
// is drafted exactly as one approved on its card. As there, a draft that
// cannot be queued does not leave the podcast `drafting` with nothing
// drafting it: the move is undone and the queue's error returned.
func StartDraft(ctx context.Context, rows Database, queue celeryqueue.Enqueuer, userID string, podcastID int64, logContext produce.LogContext) error {
	var cameFrom, target string
	err := rows.Write(ctx, func(ctx context.Context, writing Writing) error {
		podcast, err := writing.Podcast(ctx, userID, podcastID)
		if err != nil {
			return err
		}
		if podcast == nil {
			return ErrNotAtBriefGate
		}
		next, refused := approve(podcast)
		if refused != nil {
			return ErrNotAtBriefGate
		}
		moved, err := writing.Move(ctx, userID, podcastID, podcast.Status, next)
		if err != nil {
			return err
		}
		if !moved {
			return ErrNotAtBriefGate
		}
		cameFrom, target = podcast.Status, next
		return nil
	})
	if err != nil {
		return err
	}
	message := produce.Delay(DraftTask, podcastID)
	message.Context = logContext
	if _, err := queue.Enqueue(ctx, message); err != nil {
		undo := rows.Write(ctx, func(ctx context.Context, writing Writing) error {
			_, err := writing.Move(ctx, userID, podcastID, target, cameFrom)
			return err
		})
		return errors.Join(err, undo)
	}
	return nil
}
