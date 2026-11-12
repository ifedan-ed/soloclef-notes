package podcasts

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// ActionRoute serves one of the four transitions that take no body:
//
//	POST /api/v1/podcasts/{podcast_id}/brief/approve
//	POST /api/v1/podcasts/{podcast_id}/transcript/regenerate
//	POST /api/v1/podcasts/{podcast_id}/regenerate/revert
//	POST /api/v1/podcasts/{podcast_id}/cancel
//
// They are one type because they are one shape: load the podcast with
// podcasts:update, check what the action needs, move the row, answer the
// detail. NewApproveRoute, NewRegenerateRoute, NewRevertRoute and
// NewCancelRoute fill in the difference; nothing else may construct one, so
// the path, the action and whether a draft is owed cannot be combined
// wrongly.
//
// None of them takes a body.
type ActionRoute struct {
	Podcasts Database
	// Queue is where the approval's draft goes. The other two enqueue
	// nothing and never touch it.
	Queue  celeryqueue.Enqueuer
	Errors errorenvelope.Writer
	Logger *slog.Logger

	path        string
	operationID string
	summary     string
	decide      action
	// drafts says the route enqueues the transcript draft once the
	// transition is committed. Only the approval does.
	drafts bool
	// what names the route in the log.
	what string
}

// NewApproveRoute approves the brief: awaiting_brief to drafting, and the
// transcript draft enqueued.
func NewApproveRoute(podcasts Database, queue celeryqueue.Enqueuer, errors errorenvelope.Writer, logger *slog.Logger) ActionRoute {
	return ActionRoute{
		Podcasts: podcasts, Queue: queue, Errors: errors, Logger: logger,
		path: ApprovePath, operationID: "approve-podcast-brief",
		summary: "Approve the brief and start drafting the transcript",
		decide:  approve, drafts: true,
		what: "the podcast's brief could not be approved",
	}
}

// NewRegenerateRoute sends a finished episode back to the brief gate. Nothing
// is enqueued: drafting waits for the brief to be approved again.
func NewRegenerateRoute(podcasts Database, errors errorenvelope.Writer, logger *slog.Logger) ActionRoute {
	return ActionRoute{
		Podcasts: podcasts, Errors: errors, Logger: logger,
		path: RegeneratePath, operationID: "regenerate-podcast-transcript",
		summary: "Reopen the brief gate for a fresh take",
		decide:  regenerate,
		what:    "the podcast could not be reopened for a new take",
	}
}

// NewRevertRoute backs out of a regeneration, to the episode still stored.
func NewRevertRoute(podcasts Database, errors errorenvelope.Writer, logger *slog.Logger) ActionRoute {
	return ActionRoute{
		Podcasts: podcasts, Errors: errors, Logger: logger,
		path: RevertPath, operationID: "revert-podcast-regeneration",
		summary: "Back out of a regeneration and return to the finished episode",
		decide:  revert,
		what:    "the podcast's regeneration could not be reverted",
	}
}

// NewCancelRoute abandons a podcast that has no episode yet.
//
// # What a cancel is, now that nothing is billed
//
// The Python route did two things: it marked the podcast `cancelled`, and it
// released the hold the draft had placed on the owner's balance.
// Billing is leaving the product, so the second half is gone and is not
// replaced: this route writes no ledger and holds nothing.
//
// What is left is the half that mattered to the podcast, done properly:
//
//   - The row is marked `cancelled`, and only from the status the decision
//     was made on. If the render finished in the same instant, the cancel
//     matches no row and is a 409 -- it never overwrites `ready`.
//   - Marking the row is what stops the job. Nothing is revoked on the
//     queue: a podcast's draft and its render are two tasks, the second
//     queued by the first, and this tier holds the id of neither. Instead
//     the tasks are the ones that must look. `cancelled` has no exits in the
//     lifecycle (lifecycle.go), so a task that re-reads the row before it
//     writes finds a transition it may not make, and stops. A task that has
//     not started finds the same and does nothing.
//   - Nothing is enqueued and nothing is deleted. The transcript drafted so
//     far stays on the row; the segment audio is in the worker's temporary
//     directory and goes with it.
//
// The worker's side of that bargain is not this package's, and as of
// 2026-11-11 the Python tasks do not keep it: they check the status of the
// row as they loaded it at the start, not as it is when they write
// (app/podcasts/tasks/draft.py, render.py). See the hand-over notes.
func NewCancelRoute(podcasts Database, errors errorenvelope.Writer, logger *slog.Logger) ActionRoute {
	return ActionRoute{
		Podcasts: podcasts, Errors: errors, Logger: logger,
		path: CancelPath, operationID: "cancel-podcast",
		summary: "Cancel a podcast that has no finished episode",
		decide:  cancel,
		what:    "the podcast could not be cancelled",
	}
}

// Register adds the operation; the path parameter is a string for the reason
// DetailRoute.Register gives.
func (r ActionRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: r.operationID,
		Method:      http.MethodPost,
		Path:        r.path,
		Summary:     r.summary,
		Tags:        []string{"podcasts"},
	}, func(ctx context.Context, _ *struct {
		PodcastID string `path:"podcast_id" doc:"The podcast's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ActionRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	answer := answerer{
		ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestcontext.ID(ctx.Context()),
		what:      r.what,
	}
	caller, id, proceed := answer.start()
	if !proceed {
		return
	}

	// One transaction: the read, the gate, the action's checks and the
	// UPDATE. Whatever refuses rolls it back.
	var podcast *Stored
	var cameFrom string
	err := r.Podcasts.Write(ctx.Context(), func(ctx context.Context, rows Writing) error {
		loaded, err := load(ctx, rows, caller, id)
		if err != nil {
			return err
		}
		target, refused := r.decide(loaded)
		if refused != nil {
			return refused
		}
		podcast = loaded
		if target == loaded.Status {
			// Already where the action takes it (a cancel of a cancelled
			// podcast). There is nothing to write.
			return nil
		}
		// Only from the status the decision was just made on. If another
		// request moved or deleted the podcast in between, this one must not
		// win as well.
		moved, err := rows.Move(ctx, caller.UserID, loaded.ID, loaded.Status, target)
		if err != nil {
			return err
		}
		if !moved {
			return conflict(changedMeanwhile)
		}
		cameFrom, loaded.Status = loaded.Status, target
		return nil
	})
	if err != nil {
		answer.refuse(err, "could not move the podcast")
		return
	}

	if r.drafts && !r.enqueueDraft(ctx, answer, caller.UserID, podcast, cameFrom) {
		return
	}

	body, err := detail(ctx.Context(), r.Logger, podcast)
	if err != nil {
		answer.fail("the transition is committed and its answer could not be rendered", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// enqueueDraft puts the transcript draft on the queue, after the commit: the
// task reads the row it is about, so it must not be able to run before the
// row says `drafting`.
//
// A draft that cannot be queued must not leave the podcast `drafting` with
// nothing drafting it -- from there the only way out is to cancel. So the
// approval is undone and the caller is told to try again. It reports whether
// the route should go on to answer.
func (r ActionRoute) enqueueDraft(ctx huma.Context, answer answerer, userID string, podcast *Stored, cameFrom string) bool {
	// The task's one argument is the podcast. Whose it is, the task reads
	// off the row: an owner handed over beside the id would be a second
	// source of the same fact, and the row is the one that cannot be wrong.
	// The log context carries the request id into the task's own log lines.
	message := produce.Delay(DraftTask, podcast.ID)
	message.Context = celeryqueue.LogContextFor(ctx.URL().Path, answer.requestID, userID)
	_, err := r.Queue.Enqueue(ctx.Context(), message)
	if err == nil {
		return true
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "could not queue a podcast's transcript draft",
		slog.String("request_id", answer.requestID),
		slog.Int64("podcast_id", podcast.ID),
		slog.String("error", err.Error()))

	undo := r.Podcasts.Write(ctx.Context(), func(ctx context.Context, rows Writing) error {
		// Guarded like any other move: if something else has already taken
		// the podcast out of `drafting`, that is not this request's to undo.
		_, err := rows.Move(ctx, userID, podcast.ID, podcast.Status, cameFrom)
		return err
	})
	if undo != nil {
		answer.fail("the draft was not queued and the approval could not be undone", undo)
		return false
	}
	r.Errors.Write(answer.response, http.StatusServiceUnavailable, queueFailed, answer.requestID)
	return false
}
