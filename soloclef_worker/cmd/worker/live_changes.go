package main

import (
	"context"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
	"git.danvics.com/danvics/soloclef/soloclef_studio/artifactsave"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/drivesync"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/notebookname"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/podcast"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/videojob"
)

// The jobs' database seams, each wrapped so that a write a person's open
// screen shows is told to them once it is committed (soloclef_notify/
// livechange): a podcast or a video moving through its phases, a notebook
// naming itself, a Drive file arriving or failing.
//
// They wrap the seam rather than live in each job's package because telling
// is this process's wiring, like the queue and the pool: the jobs and their
// stores stay about their work, and each is tested without a bus. Only a
// write that succeeded and changed a row is told; a read is passed through
// by the embedded interface.

// livePodcasts is podcast.Rows, telling the owner's Studio panel.
type livePodcasts struct {
	podcast.Rows
	changes livechange.Publisher
}

func (r livePodcasts) AttachTranscript(ctx context.Context, podcastID int64, userID string, transcript []byte) (bool, error) {
	attached, err := r.Rows.AttachTranscript(ctx, podcastID, userID, transcript)
	if err == nil && attached {
		r.changes.Publish(ctx, userID, livechange.Change{Kind: livechange.Outputs})
	}
	return attached, err
}

func (r livePodcasts) Deliver(ctx context.Context, delivery podcast.Delivery) (int64, bool, error) {
	documentID, delivered, err := r.Rows.Deliver(ctx, delivery)
	if err == nil && delivered {
		// Ready, and its transcript is a document of the account's.
		r.changes.Publish(ctx, delivery.Run.UserID, livechange.Change{Kind: livechange.Outputs},
			livechange.Change{Kind: livechange.Documents, ID: documentID})
	}
	return documentID, delivered, err
}

func (r livePodcasts) Fail(ctx context.Context, podcastID int64, userID string, reason string) error {
	err := r.Rows.Fail(ctx, podcastID, userID, reason)
	if err == nil {
		r.changes.Publish(ctx, userID, livechange.Change{Kind: livechange.Outputs})
	}
	return err
}

// liveVideos is videojob.Rows, telling the owner's Studio panel each phase.
type liveVideos struct {
	videojob.Rows
	changes livechange.Publisher
}

func (r liveVideos) told(ctx context.Context, job videojob.Claimed) {
	r.changes.Publish(ctx, job.UserID, livechange.Change{Kind: livechange.Outputs})
}

func (r liveVideos) Claim(ctx context.Context, jobID int64, taskID string, rules videojob.ClaimRules) (videojob.Claim, error) {
	claim, err := r.Rows.Claim(ctx, jobID, taskID, rules)
	// A claim that found nothing to do names no owner and moved nothing.
	if err == nil && claim.Job.UserID != "" {
		r.told(ctx, claim.Job)
	}
	return claim, err
}

// Heartbeat is the phase and progress the card shows; it is written when
// the render moves on (videojob's beat), not on a timer, so each is told.
func (r liveVideos) Heartbeat(ctx context.Context, job videojob.Claimed, phase string, progress int) (bool, error) {
	ours, err := r.Rows.Heartbeat(ctx, job, phase, progress)
	if err == nil && ours {
		r.told(ctx, job)
	}
	return ours, err
}

func (r liveVideos) Fail(ctx context.Context, job videojob.Claimed, code, internal string) error {
	err := r.Rows.Fail(ctx, job, code, internal)
	if err == nil {
		r.told(ctx, job)
	}
	return err
}

func (r liveVideos) Cancelled(ctx context.Context, job videojob.Claimed) error {
	err := r.Rows.Cancelled(ctx, job)
	if err == nil {
		r.told(ctx, job)
	}
	return err
}

// Release gives the job back to the queue, or fails it when it has been
// taken over too often: either way the card moves.
func (r liveVideos) Release(ctx context.Context, job videojob.Claimed, rules videojob.ClaimRules) (bool, error) {
	released, err := r.Rows.Release(ctx, job, rules)
	if err == nil {
		r.told(ctx, job)
	}
	return released, err
}

func (r liveVideos) Deliver(ctx context.Context, job videojob.Claimed, artifact artifactsave.Rows) (artifactsave.Created, bool, error) {
	created, delivered, err := r.Rows.Deliver(ctx, job, artifact)
	if err == nil && delivered {
		r.changes.Publish(ctx, job.UserID, livechange.Change{Kind: livechange.Outputs},
			livechange.Change{Kind: livechange.Documents})
	}
	return created, delivered, err
}

// liveNaming is notebookname.Rows: a notebook that names itself renames its
// outputs and their documents with it.
type liveNaming struct {
	notebookname.Rows
	changes livechange.Publisher
}

func (r liveNaming) Rename(ctx context.Context, notebook notebookname.Notebook, naming notebookname.Naming) (bool, error) {
	renamed, err := r.Rows.Rename(ctx, notebook, naming)
	if err == nil && renamed {
		r.changes.Publish(ctx, notebook.UserID,
			livechange.Change{Kind: livechange.Notebooks, ID: notebook.ID},
			livechange.Change{Kind: livechange.Outputs, NotebookID: notebook.ID},
			livechange.Change{Kind: livechange.Documents})
	}
	return renamed, err
}

// liveDrive is drivesync.Rows: a connection's sync state, and each Drive
// file's document as it is queued, downloaded, renamed, gone or failed.
type liveDrive struct {
	drivesync.Rows
	changes livechange.Publisher
}

func (r liveDrive) connection(ctx context.Context, userID string, connectorID int64, err error) error {
	if err == nil {
		r.changes.Publish(ctx, userID, livechange.Change{Kind: livechange.Connectors, ID: connectorID})
	}
	return err
}

func (r liveDrive) document(ctx context.Context, userID string, documentID int64, err error) error {
	if err == nil {
		r.changes.Publish(ctx, userID, livechange.Change{Kind: livechange.Documents, ID: documentID})
	}
	return err
}

func (r liveDrive) MarkExpired(ctx context.Context, userID string, connectorID int64) error {
	return r.connection(ctx, userID, connectorID, r.Rows.MarkExpired(ctx, userID, connectorID))
}

func (r liveDrive) Synced(ctx context.Context, userID string, connectorID int64) error {
	return r.connection(ctx, userID, connectorID, r.Rows.Synced(ctx, userID, connectorID))
}

func (r liveDrive) Requeue(ctx context.Context, userID string, documentID int64) (bool, error) {
	requeued, err := r.Rows.Requeue(ctx, userID, documentID)
	if requeued {
		err = r.document(ctx, userID, documentID, err)
	}
	return requeued, err
}

func (r liveDrive) MarkGone(ctx context.Context, userID string, documentID int64, at time.Time) error {
	return r.document(ctx, userID, documentID, r.Rows.MarkGone(ctx, userID, documentID, at))
}

func (r liveDrive) MarkBack(ctx context.Context, userID string, documentID int64) error {
	return r.document(ctx, userID, documentID, r.Rows.MarkBack(ctx, userID, documentID))
}

func (r liveDrive) Rename(ctx context.Context, userID string, documentID int64, oldName, newName string) error {
	return r.document(ctx, userID, documentID, r.Rows.Rename(ctx, userID, documentID, oldName, newName))
}

func (r liveDrive) RecordDownloaded(ctx context.Context, file drivesync.Downloaded) (bool, error) {
	wanted, err := r.Rows.RecordDownloaded(ctx, file)
	if wanted {
		err = r.document(ctx, file.UserID, file.DocumentID, err)
	}
	return wanted, err
}

func (r liveDrive) MarkFailed(ctx context.Context, userID string, documentID int64, reason string) error {
	return r.document(ctx, userID, documentID, r.Rows.MarkFailed(ctx, userID, documentID, reason))
}

func (r liveDrive) MarkQueueingFailed(ctx context.Context, userID string, documentID int64, reason string) error {
	return r.document(ctx, userID, documentID, r.Rows.MarkQueueingFailed(ctx, userID, documentID, reason))
}
