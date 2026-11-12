package deliverablejobs

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// RetryPath is the route.
const RetryPath = "/api/v1/deliverable-jobs/{job_id}/retry"

// publishTimeout bounds the publish, which runs outside the request's
// context: the row is committed by then, and a person who closes the tab
// should still get the video they asked for. A broker that does not answer
// in this long is down, and the reconciler publishes the job when it is
// back.
const publishTimeout = 10 * time.Second

// RetryRoute serves the Retry button on a video that failed or was
// cancelled.
//
// The job keeps its identity (its card, its thread, its request) and
// becomes its next attempt: queued, under a new task id, published to the
// worker. The row is committed first and is its own outbox, exactly as when
// the chat first queued it (soloclef_assistant/deliverables/video.go): a publish
// that fails is logged and the job is still answered as queued, because it
// is, and the worker's reconciler publishes it within minutes
// (soloclef_worker/internal/jobreconcile).
//
// What is better than the route it replaces:
//
//   - The retry is one guarded statement, not a read, a check and a write
//     in three: two clicks at once make one new attempt, never two.
//   - Asking again for a job that is already queued answers it as it is and
//     publishes nothing. The old route published a second message for every
//     click, each of which a worker then had to take and discard.
//   - Only a kind a worker makes can be retried. The old route raised a 500
//     on a kind it did not know.
type RetryRoute struct {
	Database DBTX
	// Publish puts the attempt's message on the queue under its task id
	// (PublishVideo).
	Publish func(ctx context.Context, jobID int64, taskID string) error
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

// Check reports what is missing. Called once at start-up.
func (r RetryRoute) Check() error {
	if r.Database == nil || r.Publish == nil || r.Logger == nil {
		return errors.New("deliverablejobs: the retry route is missing its database, its publisher or its logger")
	}
	return nil
}

// Register mounts the route.
func (r RetryRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "retry-deliverable-job", Method: http.MethodPost, Path: RetryPath,
		Summary: "Try a failed or cancelled deliverable again", Tags: []string{"studio"},
	}, func(_ context.Context, _ *struct {
		JobID string `path:"job_id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r RetryRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := func(reason string, cause error) {
		r.Logger.ErrorContext(ctx.Context(), "a deliverable job could not be retried",
			slog.String("request_id", requestID), slog.String("reason", reason), slog.Any("error", cause))
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
	}
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		fail("the route ran without a caller", nil)
		return
	}
	id, err := strconv.ParseInt(ctx.Param("job_id"), 10, 64)
	if err != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{{
			Location: []string{"path", "job_id"}, Message: "Input should be a valid integer"}})
		return
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	q := New(r.Database)
	// The write first: the usual request is a job that can be retried, and
	// it is then one statement. Only a refusal reads the row, to say why.
	retried, err := q.RetryJob(ctx.Context(), RetryJobParams{Now: now, ID: id, UserID: caller.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		current, err := q.Job(ctx.Context(), JobParams{ID: id, UserID: caller.UserID})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			r.Errors.Write(response, http.StatusNotFound, "Deliverable job not found", requestID)
		case err != nil:
			fail("could not read the job", err)
		case current.Status == "queued":
			// Already waiting for a worker, by this button or by another
			// click of it: that is what was asked for.
			send(response, current)
		default:
			r.Errors.Write(response, http.StatusConflict, "This deliverable is not retryable", requestID)
		}
		return
	}
	if err != nil {
		fail("could not queue the job again", err)
		return
	}

	publishing, done := context.WithTimeout(context.WithoutCancel(ctx.Context()), publishTimeout)
	defer done()
	if err := r.Publish(publishing, retried.ID, retried.TaskID); err != nil {
		// Not a failure of the request: the queued row is the outbox, and
		// the reconciler republishes it under the same task id.
		r.Logger.WarnContext(ctx.Context(), "a retried deliverable job was not published; the reconciler will publish it",
			slog.String("request_id", requestID), slog.Int64("job_id", retried.ID),
			slog.String("task_id", retried.TaskID), slog.Any("error", err))
	}
	send(response, JobRow{
		ID: retried.ID, Kind: retried.Kind, Title: retried.Title, Status: retried.Status, Phase: retried.Phase,
		Progress: retried.Progress, FailureCode: retried.FailureCode, ArtifactID: retried.ArtifactID,
		ThreadID: retried.ThreadID, CreatedAt: retried.CreatedAt, UpdatedAt: retried.UpdatedAt,
	})
}
